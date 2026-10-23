package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func usageFixtureProvider(t *testing.T, kind string, handler http.HandlerFunc) Provider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	provider, err := NewProvider(FactoryConfig{Type: kind, Name: kind, APIKey: "fixture-only", BaseURL: server.URL, Models: []Model{{ID: "fixture-model"}}})
	require.NoError(t, err)
	return provider
}
func collectUsageStream(t *testing.T, provider Provider, request Request) []Chunk {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	stream, err := provider.Stream(ctx, request)
	require.NoError(t, err)
	var chunks []Chunk
	for chunk := range stream {
		chunks = append(chunks, chunk)
	}
	return chunks
}
func requireUsageTerminal(t *testing.T, chunks []Chunk, wantError bool) Chunk {
	t.Helper()
	require.NotEmpty(t, chunks)
	terminal := chunks[len(chunks)-1]
	done, failures := 0, 0
	for _, c := range chunks {
		if c.Done {
			done++
		}
		if c.Err != nil {
			failures++
		}
	}
	if wantError {
		require.Equal(t, 0, done, "failed stream must not emit Done")
		require.Equal(t, 1, failures)
		require.Error(t, terminal.Err)
	} else {
		require.Equal(t, 1, done)
		require.Zero(t, failures)
		require.True(t, terminal.Done)
	}
	return terminal
}

func TestOpenAIUsage_HTTPRequestsAndCompatibleAliases(t *testing.T) {
	for _, kind := range []string{"openai", "deepseek", "openrouter"} {
		t.Run(kind, func(t *testing.T) {
			requests := make(chan map[string]any, 2)
			provider := usageFixtureProvider(t, kind, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/chat/completions", r.URL.Path)
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				requests <- body
				if body["stream"] == true {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":null}\n\n")
					fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":5,\"prompt_tokens_details\":{\"cached_tokens\":12}}}\n\ndata: [DONE]\n\n")
				} else {
					fmt.Fprint(w, `{"id":"response","model":"fixture-model","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":12}}}`)
				}
			})
			chunks := collectUsageStream(t, provider, Request{Model: "fixture-model"})
			terminal := requireUsageTerminal(t, chunks, false)
			require.Equal(t, &Usage{InputTokens: 20, OutputTokens: 5, CachedInputTokens: 12}, terminal.Usage)
			request := <-requests
			require.Equal(t, true, request["stream"])
			require.Equal(t, map[string]any{"include_usage": true}, request["stream_options"])
			// The usage-only chunk is delivered before the terminal marker. A consumer
			// merging cumulative snapshots counts 25 tokens, not 25+cache or 25 twice.
			highInput, highOutput, observations := 0, 0, 0
			for _, c := range chunks {
				if c.Usage != nil {
					observations++
					if c.Usage.InputTokens > highInput {
						highInput = c.Usage.InputTokens
					}
					if c.Usage.OutputTokens > highOutput {
						highOutput = c.Usage.OutputTokens
					}
				}
			}
			require.GreaterOrEqual(t, observations, 2)
			require.Equal(t, 25, highInput+highOutput)
			result, err := provider.Complete(t.Context(), Request{Model: "fixture-model"})
			require.NoError(t, err)
			require.True(t, result.UsageReported)
			require.Equal(t, *terminal.Usage, result.Usage)
			request = <-requests
			require.NotContains(t, request, "stream_options", "stream-only option must not be sent on Complete")
		})
	}
}

func TestOpenAIUsage_PreservesZeroAndOmittedPresence(t *testing.T) {
	for _, tc := range []struct {
		name, wire string
		reported   bool
		expected   Usage
	}{
		{name: "omitted"}, {name: "null", wire: `,"usage":null`}, {name: "zero", wire: `,"usage":{"prompt_tokens":0,"completion_tokens":0}`, reported: true},
		{name: "empty reported object", wire: `,"usage":{}`, reported: true},
		{name: "cached subset", wire: `,"usage":{"prompt_tokens":15,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":8}}`, reported: true, expected: Usage{InputTokens: 15, OutputTokens: 4, CachedInputTokens: 8}},
		{name: "legacy deepseek cache", wire: `,"usage":{"prompt_tokens":15,"completion_tokens":4,"prompt_cache_hit_tokens":8}`, reported: true, expected: Usage{InputTokens: 15, OutputTokens: 4, CachedInputTokens: 8}},
		{name: "nested zero overrides legacy", wire: `,"usage":{"prompt_tokens":15,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":0},"prompt_cache_hit_tokens":8}`, reported: true, expected: Usage{InputTokens: 15, OutputTokens: 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := usageFixtureProvider(t, "openai", func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				if body["stream"] == true {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]%s}\n\ndata: [DONE]\n\n", tc.wire)
				} else {
					fmt.Fprintf(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]%s}`, tc.wire)
				}
			})
			result, err := provider.Complete(t.Context(), Request{Model: "fixture-model"})
			require.NoError(t, err)
			require.Equal(t, tc.reported, result.UsageReported)
			require.Equal(t, tc.expected, result.Usage)
			terminal := requireUsageTerminal(t, collectUsageStream(t, provider, Request{Model: "fixture-model"}), false)
			if tc.reported {
				require.NotNil(t, terminal.Usage)
				require.Equal(t, tc.expected, *terminal.Usage)
			} else {
				require.Nil(t, terminal.Usage, "unreported usage must remain unknown, not reported zero")
			}
		})
	}
}

func TestOpenAIUsage_DecodedUsageSurvivesResponseValidationErrors(t *testing.T) {
	for _, choices := range []string{`[]`, `[{"message":{"content":123},"finish_reason":"stop"}]`, `[{"message":{"content":null},"finish_reason":"length"}]`} {
		t.Run(choices, func(t *testing.T) {
			provider := usageFixtureProvider(t, "openai", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"id":"reported-error","choices":%s,"usage":{"prompt_tokens":9,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":3}}}`, choices)
			})
			result, err := provider.Complete(t.Context(), Request{Model: "fixture-model"})
			require.Error(t, err)
			require.True(t, result.UsageReported)
			require.Equal(t, Usage{InputTokens: 9, OutputTokens: 2, CachedInputTokens: 3}, result.Usage)
		})
	}
}

func TestOpenAIUsage_ExplicitCompatibilityOptOutAndNoReplay(t *testing.T) {
	var calls atomic.Int32
	requests := make(chan map[string]any, 4)
	provider := usageFixtureProvider(t, "openai", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		requests <- body
		if _, found := body["stream_options"]; found {
			http.Error(w, `{"error":{"message":"unsupported stream_options"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"legacy\"},\"finish_reason\":\"stop\"}]}\n\n")
	})
	_, err := provider.Stream(t.Context(), Request{Model: "fixture-model"})
	require.Error(t, err)
	require.Equal(t, int32(1), calls.Load(), "rejection must not replay billable model work")
	require.Contains(t, <-requests, "stream_options")
	chunks := collectUsageStream(t, provider, Request{Model: "fixture-model", Metadata: map[string]any{OpenAIStreamUsageMetadataKey: false}})
	terminal := requireUsageTerminal(t, chunks, false)
	require.Nil(t, terminal.Usage)
	require.NotContains(t, <-requests, "stream_options")
	require.Equal(t, int32(2), calls.Load())
	for _, value := range []any{"false", 0, 1, nil, map[string]any{}} {
		_, err := provider.Stream(t.Context(), Request{Model: "fixture-model", Metadata: map[string]any{OpenAIStreamUsageMetadataKey: value}})
		require.ErrorContains(t, err, "must be a boolean")
	}
	require.Equal(t, int32(2), calls.Load(), "invalid metadata must fail before a request")
}

func TestOpenAIUsage_StreamFailureRetainsKnownUsageAndNeverDone(t *testing.T) {
	const content = "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n"
	const usage = "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":3,\"prompt_tokens_details\":{\"cached_tokens\":6}}}\n\n"
	for _, tc := range []struct {
		name, tail string
		transport  bool
		wantUsage  bool
	}{
		{name: "truncated without usage"}, {name: "truncated after usage", tail: usage, wantUsage: true},
		{name: "malformed after usage", tail: usage + "data: {broken-json}\n\n", wantUsage: true},
		{name: "top-level API error", tail: usage + "data: {\"error\":{\"message\":\"upstream failed\"}}\n\n", wantUsage: true},
		{name: "OpenRouter choice error", tail: usage + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"error\",\"error\":{\"message\":\"provider failed\"}}]}\n\n", wantUsage: true},
		{name: "error finish reason", tail: usage + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"error\"}]}\n\n", wantUsage: true},
		{name: "transport reset after finish and usage", tail: usage + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n", transport: true, wantUsage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := usageFixtureProvider(t, "openai", func(w http.ResponseWriter, r *http.Request) {
				wire := content + tc.tail
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.transport {
					w.Header().Set("Content-Length", strconv.Itoa(len(wire)+512))
				}
				_, _ = fmt.Fprint(w, wire)
			})
			chunks := collectUsageStream(t, provider, Request{Model: "fixture-model"})
			terminal := requireUsageTerminal(t, chunks, true)
			if tc.wantUsage {
				require.Equal(t, &Usage{InputTokens: 11, OutputTokens: 3, CachedInputTokens: 6}, terminal.Usage)
				foundEarly := false
				for _, chunk := range chunks[:len(chunks)-1] {
					if chunk.Usage != nil {
						foundEarly = true
					}
				}
				require.True(t, foundEarly, "usage must reach accounting before a later failure")
			} else {
				require.Nil(t, terminal.Usage)
			}
			for _, chunk := range chunks {
				require.NotContains(t, chunk.Content, "data:")
				require.NotContains(t, chunk.Content, "broken-json")
			}
		})
	}
}

func TestOpenAIUsage_UnterminatedTailUsesSSEParser(t *testing.T) {
	for _, tc := range []struct {
		name, wire  string
		wantError   bool
		wantContent string
	}{
		{name: "DONE without blank delimiter", wire: "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]", wantContent: "ok"},
		{name: "JSON without blank delimiter", wire: "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}", wantContent: "ok"},
		{name: "malformed tail", wire: "data: {\"choices\":[", wantError: true},
		{name: "multi-line data", wire: "data: {\"choices\":\ndata: [{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", wantContent: "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := usageFixtureProvider(t, "openai", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, tc.wire)
			})
			chunks := collectUsageStream(t, provider, Request{Model: "fixture-model"})
			requireUsageTerminal(t, chunks, tc.wantError)
			var result strings.Builder
			for _, chunk := range chunks {
				result.WriteString(chunk.Content)
			}
			require.Equal(t, tc.wantContent, result.String())
		})
	}
}

func TestOpenAIUsage_DeepSeekFinalChoiceCarriesTotals(t *testing.T) {
	provider := usageFixtureProvider(t, "deepseek", func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, map[string]any{"include_usage": true}, request["stream_options"])
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}],\"usage\":null}\n\n")
		// Unlike OpenAI's usage-only event, DeepSeek includes usage on its final
		// nonempty choice with finish_reason. Historical versions use cache-hit totals.
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":5,\"prompt_cache_hit_tokens\":12}}\n\ndata: [DONE]\n\n")
	})
	terminal := requireUsageTerminal(t, collectUsageStream(t, provider, Request{Model: "fixture-model"}), false)
	require.Equal(t, &Usage{InputTokens: 20, OutputTokens: 5, CachedInputTokens: 12}, terminal.Usage)
}
