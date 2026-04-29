package automation

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

type Alarm struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	FireAt    time.Time `json:"fire_at"`
	Action    string    `json:"action"`
	SessionID string    `json:"session_id"`
	Fired     bool      `json:"fired"`
	Cancelled bool      `json:"cancelled"`
}

type AlarmClock struct {
	mu     sync.RWMutex
	alarms map[string]*Alarm
	fire   func(alarm *Alarm) error
	stop   chan struct{}
}

func NewAlarmClock(fire func(alarm *Alarm) error) *AlarmClock {
	return &AlarmClock{
		alarms: make(map[string]*Alarm),
		fire:   fire,
		stop:   make(chan struct{}),
	}
}

func (a *AlarmClock) Start() {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-a.stop:
				return
			case <-ticker.C:
				a.checkAlarms()
			}
		}
	}()
}

func (a *AlarmClock) checkAlarms() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	for _, alarm := range a.alarms {
		if alarm.Fired || alarm.Cancelled {
			continue
		}
		if !now.Before(alarm.FireAt) {
			alarm.Fired = true
			_ = a.fire(alarm)
		}
	}
}

func (a *AlarmClock) Stop() {
	close(a.stop)
}

func (a *AlarmClock) Set(alarm *Alarm) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, exists := a.alarms[alarm.ID]; exists {
		return fmt.Errorf("automation: set alarm %s: %w", alarm.ID, ErrAlreadyExists)
	}

	cp := *alarm
	a.alarms[alarm.ID] = &cp
	return nil
}

func (a *AlarmClock) Cancel(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	alarm, exists := a.alarms[id]
	if !exists {
		return false
	}
	alarm.Cancelled = true
	return true
}

func (a *AlarmClock) List() []*Alarm {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make([]*Alarm, 0, len(a.alarms))
	for _, alarm := range a.alarms {
		cp := *alarm
		result = append(result, &cp)
	}
	return result
}

func (a *AlarmClock) Pending() []*Alarm {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var pending []*Alarm
	for _, alarm := range a.alarms {
		if !alarm.Fired && !alarm.Cancelled {
			cp := *alarm
			pending = append(pending, &cp)
		}
	}

	sort.Slice(pending, func(i, j int) bool {
		return pending[i].FireAt.Before(pending[j].FireAt)
	})

	return pending
}
