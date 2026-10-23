package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/stretchr/testify/require"
)

func TestAfterTurnRunAndStreamFireExactlyOnceBeforeCompletion(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "run", true: "stream"}[stream], func(t *testing.T) {
			registry := hooks.NewRegistry()
			var calls atomic.Int32
			require.NoError(t, registry.Register(hooks.Hook{Name: "completion", Point: hooks.AfterTurn, Fn: func(ctx context.Context, event hooks.Event) (context.Context, error) { calls.Add(1); return ctx, nil }}))
			a := newTestAgent(&mockProvider{}, nil, registry, nil)
			defer a.Shutdown()
			for count := int32(1); count <= 2; count++ {
				if stream {
					events, err := a.Stream(t.Context(), "Reply briefly")
					require.NoError(t, err)
					for event := range events {
						require.NoError(t, event.Error)
					}
				} else {
					_, err := a.Run(t.Context(), "Reply briefly")
					require.NoError(t, err)
				}
				require.Equal(t, count, calls.Load(), "successful completion must include its single hook invocation")
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if stream {
				events, err := a.Stream(ctx, "Cancelled turn")
				if err == nil {
					for range events {
					}
				}
			} else {
				_, _ = a.Run(ctx, "Cancelled turn")
			}
			require.EqualValues(t, 2, calls.Load(), "cancelled turns cannot emit successful completion hooks")
		})
	}
}
func TestAfterTurnCancellationOwnsHookLifetime(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "run", true: "stream"}[stream], func(t *testing.T) {
			for _, shutdown := range []bool{false, true} {
				t.Run(map[bool]string{false: "turn-cancel", true: "shutdown"}[shutdown], func(t *testing.T) {
					registry := hooks.NewRegistry()
					entered, exited := make(chan struct{}), make(chan struct{})
					require.NoError(t, registry.Register(hooks.Hook{Name: "controlled-wait", Point: hooks.AfterTurn, Fn: func(ctx context.Context, event hooks.Event) (context.Context, error) {
						close(entered)
						<-ctx.Done()
						close(exited)
						return ctx, ctx.Err()
					}}))
					a := newTestAgent(&mockProvider{}, nil, registry, nil)
					defer a.Shutdown()
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					done := make(chan struct{})
					go func() {
						defer close(done)
						if stream {
							events, err := a.Stream(ctx, "Reply briefly")
							if err == nil {
								for range events {
								}
							}
						} else {
							_, _ = a.Run(ctx, "Reply briefly")
						}
					}()
					select {
					case <-entered:
					case <-time.After(2 * time.Second):
						t.Fatal("after-turn hook not entered")
					}
					if shutdown {
						a.Shutdown()
					} else {
						cancel()
					}
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Fatal("completion hook outlived cancelled owner")
					}
					select {
					case <-exited:
					default:
						t.Fatal("turn completed before hook cleanup joined")
					}
				})
			}
		})
	}
}
