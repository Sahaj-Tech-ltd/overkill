package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPError_IsRetryable(t *testing.T) {
	tests := []struct {
		code     int
		expected bool
	}{
		{429, true},
		{500, true},
		{502, true},
		{503, true},
		{529, true},
		{400, false},
		{401, false},
		{403, false},
		{404, false},
		{418, false},
		{200, false},
		{301, false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("status_%d", tt.code), func(t *testing.T) {
			err := &HTTPError{StatusCode: tt.code, Body: "test"}
			assert.Equal(t, tt.expected, err.IsRetryable())
		})
	}
}

func TestHTTPError_Error(t *testing.T) {
	err := &HTTPError{StatusCode: 429, Body: "too many requests"}
	assert.Contains(t, err.Error(), "429")
	assert.Contains(t, err.Error(), "too many requests")
}

func TestRetry_Success(t *testing.T) {
	callCount := 0
	fn := func() (*Response, error) {
		callCount++
		return &Response{ID: "resp-1", Content: "hello"}, nil
	}

	resp, err := WithRetry(fn, func(err error) bool { return false })
	require.NoError(t, err)
	assert.Equal(t, "resp-1", resp.ID)
	assert.Equal(t, 1, callCount)
}

func TestRetry_RetryableError(t *testing.T) {
	callCount := 0
	fn := func() (*Response, error) {
		callCount++
		if callCount < 4 {
			return nil, &HTTPError{StatusCode: 500, Body: "server error"}
		}
		return &Response{ID: "resp-ok", Content: "recovered"}, nil
	}

	isRetryable := func(err error) bool {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) {
			return httpErr.IsRetryable()
		}
		return false
	}

	resp, err := withRetry(fn, isRetryable, retryConfig{baseDelay: time.Millisecond})
	require.NoError(t, err)
	assert.Equal(t, "resp-ok", resp.ID)
	assert.Equal(t, 4, callCount)
}

func TestRetry_NonRetryableError(t *testing.T) {
	callCount := 0
	fn := func() (*Response, error) {
		callCount++
		return nil, &HTTPError{StatusCode: 401, Body: "unauthorized"}
	}

	isRetryable := func(err error) bool {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) {
			return httpErr.IsRetryable()
		}
		return false
	}

	resp, err := WithRetry(fn, isRetryable)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, 1, callCount)
}

func TestRetry_MaxRetries(t *testing.T) {
	callCount := 0
	fn := func() (*Response, error) {
		callCount++
		return nil, &HTTPError{StatusCode: 500, Body: "always fails"}
	}

	isRetryable := func(err error) bool { return true }

	resp, err := withRetry(fn, isRetryable, retryConfig{baseDelay: time.Millisecond})
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, maxRetries, callCount)
}

func TestRetry_HonorRetryAfter(t *testing.T) {
	start := time.Now()
	callCount := 0

	fn := func() (*Response, error) {
		callCount++
		if callCount == 1 {
			return nil, &retryAfterError{after: 50 * time.Millisecond}
		}
		return &Response{ID: "resp-ok"}, nil
	}

	isRetryable := func(err error) bool { return true }

	resp, err := withRetry(fn, isRetryable, retryConfig{baseDelay: time.Millisecond})
	require.NoError(t, err)
	assert.Equal(t, "resp-ok", resp.ID)

	elapsed := time.Since(start)
	assert.True(t, elapsed >= 40*time.Millisecond, "should have waited for Retry-After duration, got %v", elapsed)
}

func TestRetry_BackoffIncreases(t *testing.T) {
	var mu sync.Mutex
	var delays []time.Duration
	callCount := 0
	lastAttempt := time.Now()

	fn := func() (*Response, error) {
		mu.Lock()
		defer mu.Unlock()
		callCount++
		if callCount > 1 {
			delays = append(delays, time.Since(lastAttempt))
		}
		lastAttempt = time.Now()
		return nil, &HTTPError{StatusCode: 500, Body: "fail"}
	}

	isRetryable := func(err error) bool { return true }

	cfg := retryConfig{baseDelay: 10 * time.Millisecond}
	_, _ = withRetry(fn, isRetryable, cfg)

	mu.Lock()
	defer mu.Unlock()
	if len(delays) >= 2 {
		assert.True(t, delays[1] >= delays[0]*15/10,
			"second delay (%v) should be >= 1.5x first delay (%v)", delays[1], delays[0])
	}
}

func TestRetryStream_Success(t *testing.T) {
	callCount := 0
	fn := func() (<-chan Chunk, error) {
		callCount++
		ch := make(chan Chunk, 2)
		ch <- Chunk{Content: "hello"}
		ch <- Chunk{Done: true}
		close(ch)
		return ch, nil
	}

	ch, err := WithRetryStream(fn, func(err error) bool { return false })
	require.NoError(t, err)

	var chunks []Chunk
	for c := range ch {
		chunks = append(chunks, c)
	}
	assert.Len(t, chunks, 2)
	assert.Equal(t, 1, callCount)
}

func TestRetryStream_RetryableError(t *testing.T) {
	callCount := 0
	fn := func() (<-chan Chunk, error) {
		callCount++
		if callCount < 3 {
			return nil, &HTTPError{StatusCode: 503, Body: "unavailable"}
		}
		ch := make(chan Chunk, 1)
		ch <- Chunk{Done: true}
		close(ch)
		return ch, nil
	}

	isRetryable := func(err error) bool {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) {
			return httpErr.IsRetryable()
		}
		return false
	}

	ch, err := withRetryStream(fn, isRetryable, retryConfig{baseDelay: time.Millisecond})
	require.NoError(t, err)

	for range ch {
	}
	assert.Equal(t, 3, callCount)
}

func TestFailover_PrimarySucceeds(t *testing.T) {
	primary := NewMockProvider("primary", nil, func(req Request) (Response, error) {
		return Response{ID: "primary-ok", Content: "from primary"}, nil
	})
	secondary := NewMockProvider("secondary", nil, func(req Request) (Response, error) {
		return Response{ID: "secondary-ok"}, nil
	})

	fc := NewFailoverChain(primary, secondary)
	resp, err := fc.Complete(context.Background(), Request{Model: "gpt-4o"})
	require.NoError(t, err)
	assert.Equal(t, "primary-ok", resp.ID)
}

func TestFailover_PrimaryFails(t *testing.T) {
	primary := NewMockProvider("primary", nil, func(req Request) (Response, error) {
		return Response{}, &HTTPError{StatusCode: 500, Body: "primary down"}
	})
	secondary := NewMockProvider("secondary", nil, func(req Request) (Response, error) {
		return Response{ID: "secondary-ok", Content: "from secondary"}, nil
	})

	fc := NewFailoverChain(primary, secondary)
	resp, err := fc.Complete(context.Background(), Request{Model: "gpt-4o"})
	require.NoError(t, err)
	assert.Equal(t, "secondary-ok", resp.ID)
}

func TestFailover_Cooldown(t *testing.T) {
	primary := NewMockProvider("primary", nil, func(req Request) (Response, error) {
		return Response{ID: "primary-ok"}, nil
	})
	secondary := NewMockProvider("secondary", nil, func(req Request) (Response, error) {
		return Response{ID: "secondary-ok"}, nil
	})

	fc := NewFailoverChain(primary, secondary)
	fc.MarkFailed("primary", 5*time.Minute)

	resp, err := fc.Complete(context.Background(), Request{Model: "gpt-4o"})
	require.NoError(t, err)
	assert.Equal(t, "secondary-ok", resp.ID)
}

func TestFailover_AllInCooldown(t *testing.T) {
	var primaryCalled atomic.Bool
	primary := NewMockProvider("primary", nil, func(req Request) (Response, error) {
		primaryCalled.Store(true)
		return Response{ID: "primary-ok"}, nil
	})
	var secondaryCalled atomic.Bool
	secondary := NewMockProvider("secondary", nil, func(req Request) (Response, error) {
		secondaryCalled.Store(true)
		return Response{ID: "secondary-ok"}, nil
	})

	fc := NewFailoverChain(primary, secondary)
	fc.MarkFailed("primary", 5*time.Minute)
	fc.MarkFailed("secondary", 5*time.Minute)

	resp, err := fc.Complete(context.Background(), Request{Model: "gpt-4o"})
	require.NoError(t, err)
	assert.True(t, resp.ID == "primary-ok" || resp.ID == "secondary-ok",
		"should have tried at least one provider, got: %s", resp.ID)
	assert.True(t, primaryCalled.Load() || secondaryCalled.Load(),
		"at least one provider should have been called")
}

func TestFailover_ResetCooldown(t *testing.T) {
	primary := NewMockProvider("primary", nil, func(req Request) (Response, error) {
		return Response{ID: "primary-ok"}, nil
	})
	secondary := NewMockProvider("secondary", nil, func(req Request) (Response, error) {
		return Response{ID: "secondary-ok"}, nil
	})

	fc := NewFailoverChain(primary, secondary)
	fc.MarkFailed("primary", 5*time.Minute)
	fc.ResetCooldown("primary")

	resp, err := fc.Complete(context.Background(), Request{Model: "gpt-4o"})
	require.NoError(t, err)
	assert.Equal(t, "primary-ok", resp.ID)
}

func TestFailover_Stream(t *testing.T) {
	primary := NewMockProvider("primary", nil, func(req Request) (Response, error) {
		return Response{}, &HTTPError{StatusCode: 500, Body: "primary down"}
	})
	secondary := NewMockProvider("secondary", nil, func(req Request) (Response, error) {
		return Response{ID: "secondary-ok"}, nil
	})

	fc := NewFailoverChain(primary, secondary)

	streamFn := func() (<-chan Chunk, error) {
		return secondary.Stream(context.Background(), Request{Model: "gpt-4o"})
	}

	ch, err := fc.Stream(context.Background(), Request{Model: "gpt-4o"})
	require.NoError(t, err)
	assert.NotNil(t, ch)

	_ = streamFn
}

func TestFailover_StreamPrimarySucceeds(t *testing.T) {
	primary := NewMockProvider("primary", nil, func(req Request) (Response, error) {
		return Response{ID: "primary-ok"}, nil
	})
	secondary := NewMockProvider("secondary", nil, func(req Request) (Response, error) {
		return Response{ID: "secondary-ok"}, nil
	})

	fc := NewFailoverChain(primary, secondary)
	ch, err := fc.Stream(context.Background(), Request{Model: "gpt-4o"})
	require.NoError(t, err)
	assert.NotNil(t, ch)
}

func TestMockProvider(t *testing.T) {
	models := []Model{
		{ID: "gpt-4o", Name: "GPT-4o", MaxTokens: 128000},
		{ID: "gpt-4o-mini", Name: "GPT-4o Mini", MaxTokens: 128000},
	}

	mp := NewMockProvider("mock-openai", models, func(req Request) (Response, error) {
		return Response{
			ID:      "mock-resp",
			Model:   req.Model,
			Content: "mock response",
		}, nil
	})

	assert.Equal(t, "mock-openai", mp.Name())
	assert.Len(t, mp.Models(), 2)
	assert.Equal(t, "gpt-4o", mp.Models()[0].ID)

	resp, err := mp.Complete(context.Background(), Request{Model: "gpt-4o"})
	require.NoError(t, err)
	assert.Equal(t, "mock-resp", resp.ID)
	assert.Equal(t, "gpt-4o", resp.Model)
	assert.Equal(t, "mock response", resp.Content)
}

func TestMockProvider_Streaming(t *testing.T) {
	mp := NewMockProvider("mock", nil, func(req Request) (Response, error) {
		return Response{Content: "hello world"}, nil
	})

	ch, err := mp.Stream(context.Background(), Request{Model: "test"})
	require.NoError(t, err)

	var collected string
	var done bool
	for chunk := range ch {
		if chunk.Done {
			done = true
		} else {
			collected += chunk.Content
		}
	}
	assert.Equal(t, "hello world", collected)
	assert.True(t, done)
}

func TestFailover_AllProvidersFail(t *testing.T) {
	primary := NewMockProvider("primary", nil, func(req Request) (Response, error) {
		return Response{}, &HTTPError{StatusCode: 500, Body: "primary down"}
	})
	secondary := NewMockProvider("secondary", nil, func(req Request) (Response, error) {
		return Response{}, &HTTPError{StatusCode: 503, Body: "secondary down"}
	})

	fc := NewFailoverChain(primary, secondary)
	_, err := fc.Complete(context.Background(), Request{Model: "gpt-4o"})
	assert.Error(t, err)
}

func TestFailover_StreamAllProvidersFail(t *testing.T) {
	primary := NewMockProvider("primary", nil, func(req Request) (Response, error) {
		return Response{}, &HTTPError{StatusCode: 500, Body: "primary down"}
	})
	secondary := NewMockProvider("secondary", nil, func(req Request) (Response, error) {
		return Response{}, &HTTPError{StatusCode: 503, Body: "secondary down"}
	})

	fc := NewFailoverChain(primary, secondary)
	_, err := fc.Stream(context.Background(), Request{Model: "gpt-4o"})
	assert.Error(t, err)
}

func TestFailover_CanceledContext(t *testing.T) {
	primary := NewMockProvider("primary", nil, func(req Request) (Response, error) {
		return Response{ID: "ok"}, nil
	})

	fc := NewFailoverChain(primary)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := fc.Complete(ctx, Request{Model: "gpt-4o"})
	assert.Error(t, err)
}

func TestRequest_Types(t *testing.T) {
	req := Request{
		Model:        "gpt-4o",
		MaxTokens:    4096,
		Temperature:  0.7,
		SystemPrompt: "You are helpful.",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi!", ToolCalls: []ToolCall{
				{ID: "tc-1", Name: "search", Arguments: `{"q": "test"}`},
			}},
			{Role: "tool", Content: `{"result": "found"}`, ToolCallID: "tc-1"},
		},
		Tools: []Tool{
			{Name: "search", Description: "Search the web", Parameters: json.RawMessage(`{"type":"object"}`)},
		},
		Metadata: map[string]any{"session_id": "abc123"},
	}

	assert.Equal(t, "gpt-4o", req.Model)
	assert.Len(t, req.Messages, 3)
	assert.Len(t, req.Tools, 1)
	assert.Equal(t, "search", req.Messages[1].ToolCalls[0].Name)
	assert.Equal(t, "tc-1", req.Messages[2].ToolCallID)
}

func TestResponse_Usage(t *testing.T) {
	resp := Response{
		ID:    "resp-1",
		Model: "gpt-4o",
		Usage: Usage{
			InputTokens:       100,
			OutputTokens:      50,
			CachedInputTokens: 80,
		},
	}

	assert.Equal(t, 100, resp.Usage.InputTokens)
	assert.Equal(t, 50, resp.Usage.OutputTokens)
	assert.Equal(t, 80, resp.Usage.CachedInputTokens)
}

func TestModel_CostFields(t *testing.T) {
	m := Model{
		ID:                "claude-3-opus",
		Name:              "Claude 3 Opus",
		MaxTokens:         200000,
		CostIn:            0.015,
		CostOut:           0.075,
		CostCacheIn:       0.001875,
		CostCacheOut:      0.075,
		SupportsTools:     true,
		SupportsStreaming: true,
		SupportsVision:    true,
	}

	assert.Equal(t, "claude-3-opus", m.ID)
	assert.True(t, m.SupportsTools)
	assert.True(t, m.SupportsStreaming)
	assert.True(t, m.SupportsVision)
	assert.Equal(t, 0.015, m.CostIn)
}

func TestFailover_MarkFailedDefaultCooldown(t *testing.T) {
	primary := NewMockProvider("primary", nil, func(req Request) (Response, error) {
		return Response{ID: "primary-ok"}, nil
	})
	secondary := NewMockProvider("secondary", nil, func(req Request) (Response, error) {
		return Response{ID: "secondary-ok"}, nil
	})

	fc := NewFailoverChain(primary, secondary)
	fc.MarkFailed("primary", 0)

	resp, err := fc.Complete(context.Background(), Request{Model: "gpt-4o"})
	require.NoError(t, err)
	assert.Equal(t, "secondary-ok", resp.ID)
}

type retryAfterError struct {
	after time.Duration
}

func (e *retryAfterError) Error() string {
	return fmt.Sprintf("retry after %v", e.after)
}

func (e *retryAfterError) RetryAfter() time.Duration {
	return e.after
}
