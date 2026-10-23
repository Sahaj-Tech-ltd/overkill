package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/stretchr/testify/require"
)

func TestRunAndStreamOwnOneHookLifecycle(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "run", true: "stream"}[stream], func(t *testing.T) {
			registry := hooks.NewRegistry()
			var mu sync.Mutex
			var events []hooks.Event
			for _, point := range []hooks.HookPoint{hooks.OnSessionStart, hooks.OnSessionEnd} {
				require.NoError(t, registry.Register(hooks.Hook{Name: string(point), Point: point, Fn: func(ctx context.Context, event hooks.Event) (context.Context, error) {
					require.NoError(t, ctx.Err(), "cleanup must get an independent live context")
					mu.Lock()
					events = append(events, event)
					mu.Unlock()
					return ctx, nil
				}}))
			}
			a := newTestAgent(&mockProvider{}, nil, registry, nil)
			ctx, cancel := context.WithCancel(t.Context())
			for range 2 {
				if stream {
					output, err := a.Stream(ctx, "Reply briefly")
					require.NoError(t, err)
					for event := range output {
						require.NoError(t, event.Error)
					}
				} else {
					_, err := a.Run(ctx, "Reply briefly")
					require.NoError(t, err)
				}
			}
			cancel()
			a.Shutdown()
			a.Shutdown()
			mu.Lock()
			defer mu.Unlock()
			require.Len(t, events, 2)
			require.Equal(t, hooks.OnSessionStart, events[0].Point)
			require.Equal(t, hooks.OnSessionEnd, events[1].Point)
			for _, event := range events {
				require.Equal(t, "test-session", event.SessionID)
			}
		})
	}
}
