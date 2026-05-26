// Package chat — clickable-zone registry for code-block copy buttons.
//
// Bubble Tea's mouse events arrive as (X, Y) in cell coordinates over
// the program output. Translating those back to "which code block did
// the user click?" needs a per-frame record of where every clickable
// element rendered. This file owns that record.
//
// Flow: MessageList.View calls Reset() at the start of every render,
// then pushes a CopyZone into the registry for each footer chip as it
// computes absolute screen positions. The TUI's Update handler queries
// HitTest(x, y) on mouse events.
//
// Thread-safety: View runs on the Update goroutine in Bubble Tea, so
// in practice this isn't accessed concurrently. The mutex is here for
// defense and so future code that reads zones from another goroutine
// (e.g. a render-rate-limit timer) doesn't introduce a hard-to-debug
// race.
package chat

import (
	"sync"
)

// CopyZone is one clickable footer chip. (Row, MinX) is the top-left
// cell of the chip; MaxX is exclusive. Body is the raw code-block
// content (no ANSI styling) that gets pushed to the clipboard. Lang is
// for the confirmation toast.
type CopyZone struct {
	Row  int
	MinX int
	MaxX int
	Body string
	Lang string
}

var (
	zoneMu    sync.RWMutex
	zones     []CopyZone
	hoveredID int // -1 = no hover; otherwise index into zones from last frame
)

// ResetZones clears the registry. Called by MessageList.View at the
// start of every frame. Without the reset, stale zones from before a
// scroll would still hit-test positive.
func ResetZones() {
	zoneMu.Lock()
	zones = zones[:0]
	zoneMu.Unlock()
}

// RegisterZone appends a clickable zone. Returns the index so the
// caller can correlate hover state with the rendered chip if needed.
func RegisterZone(z CopyZone) int {
	zoneMu.Lock()
	defer zoneMu.Unlock()
	zones = append(zones, z)
	return len(zones) - 1
}

// HitTest returns the topmost zone containing (x, y), or nil if none
// matched. "Topmost" in row-major order — the registry is filled in
// render order, so we walk from the END so the most-recently-rendered
// (newest) chip wins overlapping coordinates.
func HitTest(x, y int) *CopyZone {
	zoneMu.RLock()
	defer zoneMu.RUnlock()
	for i := len(zones) - 1; i >= 0; i-- {
		z := zones[i]
		if y == z.Row && x >= z.MinX && x < z.MaxX {
			return &z
		}
	}
	return nil
}

// SetHoveredID records which zone the mouse is currently over. The
// renderer reads this during the next frame to highlight the matching
// chip. -1 clears the hover.
func SetHoveredID(id int) {
	zoneMu.Lock()
	hoveredID = id
	zoneMu.Unlock()
}

// HoveredID returns the currently hovered zone id, or -1 if none.
func HoveredID() int {
	zoneMu.RLock()
	defer zoneMu.RUnlock()
	return hoveredID
}

// HoveredIDForPoint returns the registered zone id that contains
// (x, y), or -1. Used by the mouse-motion handler to compute the new
// hover target without re-implementing hit-test logic.
func HoveredIDForPoint(x, y int) int {
	zoneMu.RLock()
	defer zoneMu.RUnlock()
	for i := len(zones) - 1; i >= 0; i-- {
		z := zones[i]
		if y == z.Row && x >= z.MinX && x < z.MaxX {
			return i
		}
	}
	return -1
}
