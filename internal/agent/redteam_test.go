package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// ==========================================================================
// RED TEAM: Agent module — crash/race/deadlock attacks
// Run: go test -race -count=1 -v -run 'TestRedTeam' ./internal/agent/
// ==========================================================================

// RT-AGENT-1: Concurrent Run() calls — race on shared agent state.
func TestRedTeam_Agent_ConcurrentRun(t *testing.T) {
	prov := &stubProvider{
		respond: func(req providers.Request) (providers.Response, error) {
			time.Sleep(10 * time.Millisecond)
			return providers.Response{Model: req.Model, Content: "ok"}, nil
		},
	}
	a := New(Config{Provider: prov, Model: "x", MaxTokens: 100, MaxSteps: 2})

	var wg sync.WaitGroup
	errs := make(chan error, 10)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := a.Run(context.Background(), fmt.Sprintf("msg-%d", n))
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	failCount := 0
	for e := range errs {
		t.Logf("concurrent run error: %v", e)
		failCount++
	}
	if failCount > 0 {
		t.Logf("concurrent runs produced %d errors (may indicate race condition)", failCount)
	}
}

// RT-AGENT-2: Run with nil provider — should not panic.
func TestRedTeam_Agent_NilProvider(t *testing.T) {
	a := New(Config{Model: "x", MaxTokens: 100, MaxSteps: 1})

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC with nil provider: %v", r)
		}
	}()

	_, err := a.Run(context.Background(), "hello")
	if err != nil {
		t.Logf("nil provider returned error (expected): %v", err)
	}
}

// RT-AGENT-3: Cancel context mid-stream — verify clean shutdown, no goroutine leak.
func TestRedTeam_Agent_CancelMidStream(t *testing.T) {
	prov := &stubProvider{
		respond: func(req providers.Request) (providers.Response, error) {
			time.Sleep(100 * time.Millisecond)
			return providers.Response{Model: req.Model, Content: "slow-response"}, nil
		},
	}
	a := New(Config{Provider: prov, Model: "x", MaxTokens: 100, MaxSteps: 5})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := a.Run(ctx, "hello")
	if err == nil {
		t.Log("run completed before timeout — provider too fast?")
	} else {
		t.Logf("cancelled run returned: %v", err)
	}

	// Verify agent is still usable after cancellation.
	prov2 := &stubProvider{
		respond: func(req providers.Request) (providers.Response, error) {
			return providers.Response{Model: req.Model, Content: "recovered"}, nil
		},
	}
	a.provider = prov2
	_, err = a.Run(context.Background(), "hello-again")
	if err != nil {
		t.Errorf("agent broken after cancel: %v", err)
	}
}

// RT-AGENT-4: Empty model name — verify no panic.
func TestRedTeam_Agent_EmptyModel(t *testing.T) {
	prov := &stubProvider{
		respond: func(req providers.Request) (providers.Response, error) {
			if req.Model == "" {
				return providers.Response{}, fmt.Errorf("empty model")
			}
			return providers.Response{Model: req.Model, Content: "ok"}, nil
		},
	}
	a := New(Config{Provider: prov, Model: "", MaxTokens: 100, MaxSteps: 1})

	_, err := a.Run(context.Background(), "hello")
	if err != nil {
		t.Logf("empty model returned error (expected): %v", err)
	}
}

// RT-AGENT-5: Rapid Run/Shutdown cycles — verify no leaks.
func TestRedTeam_Agent_RapidRunShutdown(t *testing.T) {
	for i := 0; i < 20; i++ {
		prov := &stubProvider{
			respond: func(req providers.Request) (providers.Response, error) {
				return providers.Response{Model: req.Model, Content: "ok"}, nil
			},
		}
		a := New(Config{Provider: prov, Model: "x", MaxTokens: 100, MaxSteps: 1})
		_, err := a.Run(context.Background(), "hello")
		if err != nil {
			t.Logf("cycle %d: run error: %v", i, err)
		}
		a.Shutdown()
	}
}

// RT-AGENT-6: Extremely long input — verify no OOM or panic.
func TestRedTeam_Agent_LongInput(t *testing.T) {
	prov := &stubProvider{
		respond: func(req providers.Request) (providers.Response, error) {
			return providers.Response{Model: req.Model, Content: "ok"}, nil
		},
	}
	a := New(Config{Provider: prov, Model: "x", MaxTokens: 100, MaxSteps: 1})

	longMsg := make([]byte, 100_000)
	for i := range longMsg {
		longMsg[i] = 'x'
	}

	_, err := a.Run(context.Background(), string(longMsg))
	if err != nil {
		t.Logf("long input returned error: %v", err)
	}
}

// RT-AGENT-7: Unicode/emoji bomb in input.
func TestRedTeam_Agent_UnicodeBomb(t *testing.T) {
	prov := &stubProvider{
		respond: func(req providers.Request) (providers.Response, error) {
			return providers.Response{Model: req.Model, Content: "👍"}, nil
		},
	}
	a := New(Config{Provider: prov, Model: "x", MaxTokens: 100, MaxSteps: 1})

	// Mix of RTL override, emoji, zero-width, and bidirectional text
	bomb := "hello\xe2\x80\x8f" + string([]rune{0x202E, 0x200F}) + "\U0001F4A3\U0001F525\U0001F480" +
		"שלום" + "日本語" + "👨‍👩‍👧‍👦" + "\x00test"

	_, err := a.Run(context.Background(), bomb)
	if err != nil {
		t.Logf("unicode bomb returned error: %v", err)
	}
}

// RT-AGENT-8: Run with zero MaxSteps.
func TestRedTeam_Agent_ZeroMaxSteps(t *testing.T) {
	prov := &stubProvider{
		respond: func(req providers.Request) (providers.Response, error) {
			return providers.Response{Model: req.Model, Content: "ok"}, nil
		},
	}
	a := New(Config{Provider: prov, Model: "x", MaxTokens: 100, MaxSteps: 0})

	_, err := a.Run(context.Background(), "hello")
	// MaxSteps=0 should default to 20
	if err != nil {
		t.Logf("zero maxsteps returned: %v", err)
	}
}

// RT-AGENT-9: Stream with nil tool registry.
func TestRedTeam_Agent_NilTools(t *testing.T) {
	a := New(Config{Model: "x", MaxTokens: 100, MaxSteps: 1})

	prov := &stubProvider{
		respond: func(req providers.Request) (providers.Response, error) {
			// Return a tool call when no tools are registered
			return providers.Response{
				Model:   req.Model,
				Content: "",
				ToolCalls: []providers.ToolCall{
					{ID: "call_1", Name: "nonexistent", Arguments: `{}`},
				},
			}, nil
		},
	}
	a.provider = prov

	_, err := a.Run(context.Background(), "use a tool")
	if err != nil {
		t.Logf("nil tools returned: %v", err)
	}
}

// RT-AGENT-10: Double Shutdown — verify idempotent.
func TestRedTeam_Agent_DoubleShutdown(t *testing.T) {
	prov := &stubProvider{
		respond: func(req providers.Request) (providers.Response, error) {
			return providers.Response{Model: req.Model, Content: "ok"}, nil
		},
	}
	a := New(Config{Provider: prov, Model: "x", MaxTokens: 100, MaxSteps: 1})
	_, _ = a.Run(context.Background(), "hello")

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC on double shutdown: %v", r)
		}
	}()

	a.Shutdown()
	a.Shutdown() // second should be safe no-op
}
