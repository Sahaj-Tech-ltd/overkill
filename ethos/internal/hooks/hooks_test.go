package hooks

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	require.NotNil(t, r)
	require.NotNil(t, r.hooks)
	require.NotNil(t, r.names)
}

func TestRegistry_Register(t *testing.T) {
	r := NewRegistry()

	hook := Hook{
		Name:     "test-hook",
		Point:    BeforeToolCall,
		Fn:       func(ctx context.Context, event Event) (context.Context, error) { return ctx, nil },
		Priority: 10,
	}

	err := r.Register(hook)
	require.NoError(t, err)

	list := r.List(BeforeToolCall)
	assert.Len(t, list, 1)
	assert.Equal(t, "test-hook", list[0].Name)
}

func TestRegistry_RegisterDuplicateName(t *testing.T) {
	r := NewRegistry()

	hook := Hook{
		Name:     "dup",
		Point:    BeforeToolCall,
		Fn:       func(ctx context.Context, event Event) (context.Context, error) { return ctx, nil },
		Priority: 10,
	}

	require.NoError(t, r.Register(hook))
	err := r.Register(hook)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestRegistry_RegisterEmptyName(t *testing.T) {
	r := NewRegistry()

	hook := Hook{
		Name:  "",
		Point: BeforeToolCall,
		Fn:    func(ctx context.Context, event Event) (context.Context, error) { return ctx, nil },
	}

	err := r.Register(hook)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestRegistry_RegisterNilFn(t *testing.T) {
	r := NewRegistry()

	hook := Hook{
		Name:  "nil-fn",
		Point: BeforeToolCall,
	}

	err := r.Register(hook)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "function is required")
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()

	hook := Hook{
		Name:     "remove-me",
		Point:    BeforeToolCall,
		Fn:       func(ctx context.Context, event Event) (context.Context, error) { return ctx, nil },
		Priority: 10,
	}

	require.NoError(t, r.Register(hook))
	assert.True(t, r.Unregister("remove-me"))
	assert.Len(t, r.List(BeforeToolCall), 0)
}

func TestRegistry_UnregisterNonExistent(t *testing.T) {
	r := NewRegistry()
	assert.False(t, r.Unregister("no-such-hook"))
}

func TestRegistry_PriorityOrdering(t *testing.T) {
	r := NewRegistry()

	var order []int
	var mu sync.Mutex

	for _, prio := range []int{30, 10, 20} {
		p := prio
		require.NoError(t, r.Register(Hook{
			Name:  fmt.Sprintf("hook-%d", p),
			Point: BeforeToolCall,
			Fn: func(ctx context.Context, event Event) (context.Context, error) {
				mu.Lock()
				order = append(order, p)
				mu.Unlock()
				return ctx, nil
			},
			Priority: p,
		}))
	}

	_, err := r.Fire(context.Background(), BeforeToolCall, Event{Point: BeforeToolCall})
	require.NoError(t, err)

	assert.Equal(t, []int{10, 20, 30}, order)
}

func TestRegistry_FireNoHooks(t *testing.T) {
	r := NewRegistry()
	ctx := context.Background()

	resultCtx, err := r.Fire(ctx, BeforeToolCall, Event{Point: BeforeToolCall})
	assert.NoError(t, err)
	assert.Equal(t, ctx, resultCtx)
}

func TestRegistry_FirePanicIsolation(t *testing.T) {
	r := NewRegistry()

	var ran bool
	require.NoError(t, r.Register(Hook{
		Name:     "panicker",
		Point:    BeforeToolCall,
		Fn:       func(ctx context.Context, event Event) (context.Context, error) { panic("boom") },
		Priority: 10,
	}))
	require.NoError(t, r.Register(Hook{
		Name:  "after-panic",
		Point: BeforeToolCall,
		Fn: func(ctx context.Context, event Event) (context.Context, error) {
			ran = true
			return ctx, nil
		},
		Priority: 20,
	}))

	_, err := r.Fire(context.Background(), BeforeToolCall, Event{Point: BeforeToolCall})
	assert.NoError(t, err)
	assert.True(t, ran, "hook after panic should still run")
}

func TestRegistry_FireErrorIsolation(t *testing.T) {
	r := NewRegistry()

	var ran bool
	require.NoError(t, r.Register(Hook{
		Name:  "error-hook",
		Point: BeforeToolCall,
		Fn: func(ctx context.Context, event Event) (context.Context, error) {
			return ctx, errors.New("hook error")
		},
		Priority: 10,
	}))
	require.NoError(t, r.Register(Hook{
		Name:  "after-error",
		Point: BeforeToolCall,
		Fn: func(ctx context.Context, event Event) (context.Context, error) {
			ran = true
			return ctx, nil
		},
		Priority: 20,
	}))

	_, err := r.Fire(context.Background(), BeforeToolCall, Event{Point: BeforeToolCall})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error-hook")
	assert.True(t, ran, "hook after error should still run")
}

func TestRegistry_FireReturnsFirstError(t *testing.T) {
	r := NewRegistry()

	require.NoError(t, r.Register(Hook{
		Name:  "first-err",
		Point: BeforeToolCall,
		Fn: func(ctx context.Context, event Event) (context.Context, error) {
			return ctx, errors.New("first")
		},
		Priority: 10,
	}))
	require.NoError(t, r.Register(Hook{
		Name:  "second-err",
		Point: BeforeToolCall,
		Fn: func(ctx context.Context, event Event) (context.Context, error) {
			return ctx, errors.New("second")
		},
		Priority: 20,
	}))

	_, err := r.Fire(context.Background(), BeforeToolCall, Event{Point: BeforeToolCall})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "first-err")
	assert.NotContains(t, err.Error(), "second-err")
}

func TestRegistry_FireContextPropagation(t *testing.T) {
	r := NewRegistry()

	type ctxKey string
	const key ctxKey = "marker"

	require.NoError(t, r.Register(Hook{
		Name:  "add-ctx",
		Point: BeforeToolCall,
		Fn: func(ctx context.Context, event Event) (context.Context, error) {
			return context.WithValue(ctx, key, "set"), nil
		},
		Priority: 10,
	}))
	require.NoError(t, r.Register(Hook{
		Name:  "check-ctx",
		Point: BeforeToolCall,
		Fn: func(ctx context.Context, event Event) (context.Context, error) {
			assert.Equal(t, "set", ctx.Value(key))
			return ctx, nil
		},
		Priority: 20,
	}))

	resultCtx, err := r.Fire(context.Background(), BeforeToolCall, Event{Point: BeforeToolCall})
	require.NoError(t, err)
	assert.Equal(t, "set", resultCtx.Value(key))
}

func TestRegistry_ConcurrentFire(t *testing.T) {
	r := NewRegistry()

	var calls atomic.Int64
	require.NoError(t, r.Register(Hook{
		Name:  "concurrent",
		Point: BeforeToolCall,
		Fn: func(ctx context.Context, event Event) (context.Context, error) {
			calls.Add(1)
			return ctx, nil
		},
		Priority: 10,
	}))

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.Fire(context.Background(), BeforeToolCall, Event{Point: BeforeToolCall})
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(100), calls.Load())
}

func TestRegistry_List(t *testing.T) {
	r := NewRegistry()

	require.NoError(t, r.Register(Hook{
		Name: "h1", Point: BeforeToolCall,
		Fn:       func(ctx context.Context, event Event) (context.Context, error) { return ctx, nil },
		Priority: 10,
	}))
	require.NoError(t, r.Register(Hook{
		Name: "h2", Point: AfterToolCall,
		Fn:       func(ctx context.Context, event Event) (context.Context, error) { return ctx, nil },
		Priority: 10,
	}))

	before := r.List(BeforeToolCall)
	assert.Len(t, before, 1)
	assert.Equal(t, "h1", before[0].Name)

	after := r.List(AfterToolCall)
	assert.Len(t, after, 1)
	assert.Equal(t, "h2", after[0].Name)
}

func TestRegistry_ListAll(t *testing.T) {
	r := NewRegistry()

	require.NoError(t, r.Register(Hook{
		Name: "h1", Point: BeforeToolCall,
		Fn:       func(ctx context.Context, event Event) (context.Context, error) { return ctx, nil },
		Priority: 10,
	}))
	require.NoError(t, r.Register(Hook{
		Name: "h2", Point: AfterToolCall,
		Fn:       func(ctx context.Context, event Event) (context.Context, error) { return ctx, nil },
		Priority: 10,
	}))

	all := r.ListAll()
	assert.Len(t, all, 2)
	assert.Contains(t, all, BeforeToolCall)
	assert.Contains(t, all, AfterToolCall)
}

func TestRegistry_ListReturnsCopy(t *testing.T) {
	r := NewRegistry()

	require.NoError(t, r.Register(Hook{
		Name: "h1", Point: BeforeToolCall,
		Fn:       func(ctx context.Context, event Event) (context.Context, error) { return ctx, nil },
		Priority: 10,
	}))

	list := r.List(BeforeToolCall)
	list[0] = Hook{Name: "mutated"}

	original := r.List(BeforeToolCall)
	assert.Equal(t, "h1", original[0].Name)
}

func TestNewLoggingHook(t *testing.T) {
	r := NewRegistry()
	hook := NewLoggingHook()
	require.NoError(t, r.Register(hook))

	_, err := r.Fire(context.Background(), BeforeToolCall, Event{
		Point:     BeforeToolCall,
		ToolName:  "shell",
		SessionID: "sess-123",
	})
	assert.NoError(t, err)
}

func TestNewMetricsHook_Counts(t *testing.T) {
	m := NewMetricsHook()
	r := NewRegistry()
	require.NoError(t, r.Register(m.Hook))

	for i := 0; i < 5; i++ {
		_, err := r.Fire(context.Background(), BeforeToolCall, Event{Point: BeforeToolCall})
		require.NoError(t, err)
	}

	counts := m.Counts()
	assert.Equal(t, int64(5), counts[BeforeToolCall])
}

func TestNewMetricsHook_MultiplePoints(t *testing.T) {
	m := NewMetricsHook()
	r := NewRegistry()

	require.NoError(t, r.Register(m.Hook))

	require.NoError(t, r.Register(Hook{
		Name:     "metrics-after",
		Point:    AfterToolCall,
		Fn:       m.record,
		Priority: 999,
	}))

	_, _ = r.Fire(context.Background(), BeforeToolCall, Event{Point: BeforeToolCall})
	_, _ = r.Fire(context.Background(), BeforeToolCall, Event{Point: BeforeToolCall})
	_, _ = r.Fire(context.Background(), AfterToolCall, Event{Point: AfterToolCall})

	counts := m.Counts()
	assert.Equal(t, int64(2), counts[BeforeToolCall])
	assert.Equal(t, int64(1), counts[AfterToolCall])
}

func TestNewRateLimitHook_AllowsUnderLimit(t *testing.T) {
	r := NewRegistry()
	hook := NewRateLimitHook(5)
	require.NoError(t, r.Register(hook))

	for i := 0; i < 5; i++ {
		_, err := r.Fire(context.Background(), BeforeToolCall, Event{Point: BeforeToolCall})
		assert.NoError(t, err)
	}
}

func TestNewRateLimitHook_BlocksOverLimit(t *testing.T) {
	r := NewRegistry()
	hook := NewRateLimitHook(3)
	require.NoError(t, r.Register(hook))

	for i := 0; i < 3; i++ {
		_, err := r.Fire(context.Background(), BeforeToolCall, Event{Point: BeforeToolCall})
		require.NoError(t, err)
	}

	_, err := r.Fire(context.Background(), BeforeToolCall, Event{Point: BeforeToolCall})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rate limit exceeded")
}

func TestNewRateLimitHook_WindowExpires(t *testing.T) {
	rl := &rateLimiter{maxPerMinute: 1, timestamps: make([]time.Time, 0)}

	ctx := context.Background()
	_, err := rl.check(ctx, Event{Point: BeforeToolCall})
	require.NoError(t, err)

	_, err = rl.check(ctx, Event{Point: BeforeToolCall})
	assert.Error(t, err, "should be rate limited")

	rl.timestamps[0] = time.Now().Add(-2 * time.Minute)

	_, err = rl.check(ctx, Event{Point: BeforeToolCall})
	assert.NoError(t, err, "old entry should have expired")
}
