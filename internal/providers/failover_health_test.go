package providers

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type failoverHealthProvider struct {
	name     string
	complete func(context.Context) (Response, error)
	stream   func(context.Context) (<-chan Chunk, error)
}

func (p *failoverHealthProvider) Name() string    { return p.name }
func (p *failoverHealthProvider) Models() []Model { return nil }
func (p *failoverHealthProvider) Complete(ctx context.Context, _ Request) (Response, error) {
	return p.complete(ctx)
}
func (p *failoverHealthProvider) Stream(ctx context.Context, _ Request) (<-chan Chunk, error) {
	return p.stream(ctx)
}

func TestFailoverActualFailuresCooldownAndRecover(t *testing.T) {
	var calls atomic.Int32
	primary := &failoverHealthProvider{name: "primary", complete: func(context.Context) (Response, error) {
		if calls.Add(1) == 1 {
			return Response{}, &HTTPError{StatusCode: 503}
		}
		return Response{Content: "recovered"}, nil
	}}
	secondary := NewMockProvider("secondary", nil, func(Request) (Response, error) { return Response{Content: "fallback"}, nil })
	chain := NewFailoverChain(primary, secondary)
	for range 2 {
		response, err := chain.Complete(t.Context(), Request{})
		require.NoError(t, err)
		require.Equal(t, "fallback", response.Content)
	}
	require.Equal(t, int32(1), calls.Load(), "next request must skip failed primary")
	chain.mu.Lock()
	chain.cooldowns["primary"] = time.Now().Add(-time.Second)
	chain.mu.Unlock()
	response, err := chain.Complete(t.Context(), Request{})
	require.NoError(t, err)
	require.Equal(t, "recovered", response.Content)
	chain.mu.RLock()
	_, cooling := chain.cooldowns["primary"]
	chain.mu.RUnlock()
	require.False(t, cooling)
}

func TestFailoverCallerCancellationDoesNotPenalizeOrCallFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var fallback atomic.Int32
	primary := &failoverHealthProvider{name: "primary", complete: func(context.Context) (Response, error) { cancel(); return Response{}, ctx.Err() }}
	secondary := NewMockProvider("secondary", nil, func(Request) (Response, error) { fallback.Add(1); return Response{}, nil })
	chain := NewFailoverChain(primary, secondary)
	_, err := chain.Complete(ctx, Request{})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, fallback.Load())
	require.Len(t, chain.availableProviders(), 2)
	for _, failure := range []error{ErrContextLength, ErrModelNotFound} {
		require.True(t, chain.recordFailure(t.Context(), "primary", failure))
		require.Len(t, chain.availableProviders(), 2)
	}
}

func TestFailoverStreamFailurePreservesUsageAndNeverReplays(t *testing.T) {
	var primaryCalls, secondaryCalls atomic.Int32
	primary := &failoverHealthProvider{name: "primary", stream: func(context.Context) (<-chan Chunk, error) {
		primaryCalls.Add(1)
		input := make(chan Chunk, 2)
		input <- Chunk{Content: "partial"}
		input <- Chunk{Usage: &Usage{InputTokens: 12, OutputTokens: 3}, Err: &HTTPError{StatusCode: 503}}
		close(input)
		return input, nil
	}}
	secondary := &failoverHealthProvider{name: "secondary", stream: func(context.Context) (<-chan Chunk, error) {
		secondaryCalls.Add(1)
		input := make(chan Chunk, 1)
		input <- Chunk{Done: true}
		close(input)
		return input, nil
	}}
	chain := NewFailoverChain(primary, secondary)
	stream, err := chain.Stream(t.Context(), Request{})
	require.NoError(t, err)
	var received []Chunk
	for chunk := range stream {
		received = append(received, chunk)
	}
	require.Len(t, received, 2)
	require.Equal(t, "partial", received[0].Content)
	require.Error(t, received[1].Err)
	require.Equal(t, &Usage{InputTokens: 12, OutputTokens: 3}, received[1].Usage)
	require.False(t, received[1].Done)
	require.Zero(t, secondaryCalls.Load(), "published work must never be replayed")
	stream, err = chain.Stream(t.Context(), Request{})
	require.NoError(t, err)
	for range stream {
	}
	require.Equal(t, int32(1), primaryCalls.Load())
	require.Equal(t, int32(1), secondaryCalls.Load())
}

func TestFailoverStreamStartupFailureAndUnterminatedStream(t *testing.T) {
	for _, startup := range []bool{true, false} {
		t.Run(map[bool]string{true: "startup", false: "unterminated"}[startup], func(t *testing.T) {
			primary := &failoverHealthProvider{name: "primary", stream: func(context.Context) (<-chan Chunk, error) {
				if startup {
					return nil, &HTTPError{StatusCode: 429}
				}
				input := make(chan Chunk)
				close(input)
				return input, nil
			}}
			secondary := &failoverHealthProvider{name: "secondary", stream: func(context.Context) (<-chan Chunk, error) {
				input := make(chan Chunk, 1)
				input <- Chunk{Done: true}
				close(input)
				return input, nil
			}}
			chain := NewFailoverChain(primary, secondary)
			stream, err := chain.Stream(t.Context(), Request{})
			require.NoError(t, err)
			var last Chunk
			for chunk := range stream {
				last = chunk
			}
			if startup {
				require.True(t, last.Done)
			} else {
				require.Error(t, last.Err)
				require.False(t, last.Done)
			}
			require.Len(t, chain.availableProviders(), 1)
		})
	}
}

func TestFailoverCancelledStreamClosesWithoutCooldown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	input := make(chan Chunk)
	provider := &failoverHealthProvider{name: "primary", stream: func(context.Context) (<-chan Chunk, error) { return input, nil }}
	chain := NewFailoverChain(provider)
	stream, err := chain.Stream(ctx, Request{})
	require.NoError(t, err)
	cancel()
	select {
	case _, ok := <-stream:
		require.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("stream did not release on cancellation")
	}
	require.Len(t, chain.availableProviders(), 1)
}
