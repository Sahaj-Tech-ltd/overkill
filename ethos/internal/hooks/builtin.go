package hooks

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

func NewLoggingHook() Hook {
	return Hook{
		Name:  "builtin.logging",
		Point: BeforeToolCall,
		Fn: func(ctx context.Context, event Event) (context.Context, error) {
			log.Printf("hooks: point=%s tool=%s session=%s", event.Point, event.ToolName, event.SessionID)
			return ctx, nil
		},
		Priority: 1000,
	}
}

type MetricsHook struct {
	Hook
	counts sync.Map
}

func NewMetricsHook() *MetricsHook {
	m := &MetricsHook{}
	m.Hook = Hook{
		Name:  "builtin.metrics",
		Point: BeforeToolCall,
		Fn:    m.record,
		Priority: 999,
	}
	return m
}

func (m *MetricsHook) record(ctx context.Context, event Event) (context.Context, error) {
	val, _ := m.counts.LoadOrStore(event.Point, new(int64))
	counter := val.(*int64)
	*counter++
	return ctx, nil
}

func (m *MetricsHook) Counts() map[HookPoint]int64 {
	result := make(map[HookPoint]int64)
	m.counts.Range(func(key, value any) bool {
		result[key.(HookPoint)] = *value.(*int64)
		return true
	})
	return result
}

func NewRateLimitHook(maxPerMinute int) Hook {
	rl := &rateLimiter{
		maxPerMinute: maxPerMinute,
		timestamps:   make([]time.Time, 0),
	}
	return Hook{
		Name:     "builtin.rate_limit",
		Point:    BeforeToolCall,
		Fn:       rl.check,
		Priority: 0,
	}
}

type rateLimiter struct {
	mu           sync.Mutex
	maxPerMinute int
	timestamps   []time.Time
}

func (rl *rateLimiter) check(ctx context.Context, event Event) (context.Context, error) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-time.Minute)

	i := 0
	for i < len(rl.timestamps) && rl.timestamps[i].Before(windowStart) {
		i++
	}
	rl.timestamps = rl.timestamps[i:]

	if len(rl.timestamps) >= rl.maxPerMinute {
		return ctx, fmt.Errorf("hooks: rate limit exceeded (%d calls/min)", rl.maxPerMinute)
	}

	rl.timestamps = append(rl.timestamps, now)
	return ctx, nil
}
