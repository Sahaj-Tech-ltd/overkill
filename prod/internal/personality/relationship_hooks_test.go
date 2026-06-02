package personality

import (
	"sync"
	"testing"
	"time"
)

func TestOnBeat_FiresEveryRecord(t *testing.T) {
	r := NewRelationshipTracker()
	var mu sync.Mutex
	got := []BeatType{}
	done := make(chan struct{}, 3)
	r.OnBeat(func(b Beat) {
		mu.Lock()
		got = append(got, b.Type)
		mu.Unlock()
		done <- struct{}{}
	})
	r.RecordBeat(BeatFirstSuccess, "ok", "s")
	r.RecordBeat(BeatFirstSuccess, "ok", "s")
	r.RecordBeat(BeatFirstFailure, "boom", "s")

	for i := 0; i < 3; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("hook timeout, got=%v", got)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("hook count=%d want 3", len(got))
	}
}

func TestOnFirstBeat_FiresOnlyOnce(t *testing.T) {
	r := NewRelationshipTracker()
	var mu sync.Mutex
	count := 0
	done := make(chan struct{}, 2)
	r.OnFirstBeat(func(b Beat) {
		mu.Lock()
		count++
		mu.Unlock()
		done <- struct{}{}
	})
	r.RecordBeat(BeatFirstPR, "merged", "s")
	r.RecordBeat(BeatFirstPR, "merged", "s") // already a milestone — no fire

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("first hook never fired")
	}
	// Allow a moment for any rogue extra firing.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if count != 1 {
		t.Fatalf("first hook fired %d times want 1", count)
	}
}

func TestOnBeat_NilSafe(t *testing.T) {
	r := NewRelationshipTracker()
	r.OnBeat(nil)      // should not panic
	r.OnFirstBeat(nil) // ditto
	r.RecordBeat(BeatFirstHighFive, "", "")
}

func TestOnBeat_PanicRecovered(t *testing.T) {
	r := NewRelationshipTracker()
	r.OnBeat(func(b Beat) { panic("nope") })
	r.RecordBeat(BeatLateNight, "3am", "s")
	time.Sleep(50 * time.Millisecond) // hook runs in a goroutine; verify no crash
}
