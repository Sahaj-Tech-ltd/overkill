package chat

import (
	"sync"
	"testing"
)

func TestResetZones_ClearsAll(t *testing.T) {
	ResetZones()
	RegisterZone(CopyZone{Row: 1, MinX: 0, MaxX: 5, Body: "a"})
	RegisterZone(CopyZone{Row: 2, MinX: 0, MaxX: 5, Body: "b"})
	ResetZones()
	if HitTest(0, 1) != nil {
		t.Error("Reset must drop all zones")
	}
}

func TestHitTest_InsideZoneMatches(t *testing.T) {
	ResetZones()
	RegisterZone(CopyZone{Row: 4, MinX: 2, MaxX: 8, Body: "body", Lang: "go"})
	got := HitTest(5, 4)
	if got == nil {
		t.Fatal("expected hit, got nil")
	}
	if got.Body != "body" || got.Lang != "go" {
		t.Errorf("payload mismatch: %+v", got)
	}
}

func TestHitTest_BoundaryRules(t *testing.T) {
	ResetZones()
	RegisterZone(CopyZone{Row: 4, MinX: 2, MaxX: 8, Body: "x"})
	tests := []struct {
		x, y int
		hit  bool
	}{
		{1, 4, false}, // left of MinX
		{2, 4, true},  // MinX inclusive
		{7, 4, true},  // MaxX-1 included
		{8, 4, false}, // MaxX exclusive
		{5, 3, false}, // wrong row
		{5, 5, false}, // wrong row
	}
	for _, tt := range tests {
		got := HitTest(tt.x, tt.y) != nil
		if got != tt.hit {
			t.Errorf("HitTest(%d,%d) = %v, want %v", tt.x, tt.y, got, tt.hit)
		}
	}
}

func TestHitTest_TopmostWins(t *testing.T) {
	ResetZones()
	// Two zones overlapping at the same coords — newest (later-
	// registered) should win.
	RegisterZone(CopyZone{Row: 4, MinX: 2, MaxX: 8, Body: "old"})
	RegisterZone(CopyZone{Row: 4, MinX: 2, MaxX: 8, Body: "new"})
	got := HitTest(5, 4)
	if got == nil || got.Body != "new" {
		t.Errorf("topmost overlap should win: got %+v", got)
	}
}

func TestHoveredID_RoundTrip(t *testing.T) {
	ResetZones()
	SetHoveredID(7)
	if HoveredID() != 7 {
		t.Errorf("HoveredID lost: %d", HoveredID())
	}
	SetHoveredID(-1)
	if HoveredID() != -1 {
		t.Errorf("clear: %d", HoveredID())
	}
}

func TestHoveredIDForPoint_ReturnsRegistryIndex(t *testing.T) {
	ResetZones()
	RegisterZone(CopyZone{Row: 1, MinX: 0, MaxX: 5})
	RegisterZone(CopyZone{Row: 2, MinX: 0, MaxX: 5})
	if id := HoveredIDForPoint(2, 2); id != 1 {
		t.Errorf("expected zone index 1, got %d", id)
	}
	if id := HoveredIDForPoint(0, 0); id != -1 {
		t.Errorf("miss should return -1, got %d", id)
	}
}

func TestRegisterZone_ConcurrentSafe(t *testing.T) {
	// Mutex guards the registry; this exercises the lock under -race.
	ResetZones()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			RegisterZone(CopyZone{Row: i, MinX: 0, MaxX: 10})
			_ = HitTest(5, i)
		}(i)
	}
	wg.Wait()
}
