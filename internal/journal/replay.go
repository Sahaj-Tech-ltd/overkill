// Package journal — session replay (§8.3 Phase 5 #5).
//
// Reads a journaled session's flight recorder entries and yields
// them in chronological order with optional filtering. Useful for:
//
//   - Debugging "why did the agent decide X?" — step through the
//     exact sequence of inputs, tool calls, results, errors.
//   - Post-mortem rendering — the SSE dashboard subscribes to the
//     replay stream to redraw a past session.
//   - Test fixtures — feed a deterministic replay into a stub
//     agent to verify deterministic-by-design behaviour.
//
// What this is NOT: re-executing tools. Replay never invokes side
// effects; it just yields recorded entries. The tools' real results
// are in the journal — we surface them, we don't reproduce them.
package journal

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ReplayOptions tunes what to yield. Zero-value yields every entry
// in the session.
type ReplayOptions struct {
	// Types filters to specific entry types. Empty means "all".
	Types []EntryType
	// From skips entries before this timestamp. Zero means "no
	// lower bound".
	From time.Time
	// Until stops at this timestamp. Zero means "no upper bound".
	Until time.Time
	// Speed >0 enables real-time playback: sleep between entries
	// so they emit at recorded pace, divided by speed (1.0 = real
	// time, 2.0 = 2× faster). 0 yields as fast as the consumer can
	// read.
	Speed float64
}

// ReplayEvent is one yielded entry plus the elapsed offset from
// session start. Consumers can use Offset for timeline rendering
// without re-deriving it.
type ReplayEvent struct {
	Entry  Entry
	Offset time.Duration // since first entry in the replay
	Index  int           // 0-based position within the filtered stream
}

// Replay walks a session in order and emits ReplayEvents on the
// returned channel. The channel closes when:
//
//   - All entries have been yielded.
//   - ctx is cancelled.
//   - An unrecoverable read error occurs (sent on errCh too).
//
// errCh is buffered so a caller that ignores it doesn't leak the
// goroutine. Both channels are 1-shot from the producer side.
func (r *FlightRecorder) Replay(ctx context.Context, sessionID string, opts ReplayOptions) (<-chan ReplayEvent, <-chan error) {
	out := make(chan ReplayEvent, 16)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		entries, err := r.ReadSession(sessionID)
		if err != nil {
			errCh <- fmt.Errorf("replay: read session %s: %w", sessionID, err)
			return
		}
		entries = filterAndSort(entries, opts)
		if len(entries) == 0 {
			return
		}
		start := entries[0].Timestamp
		for i, e := range entries {
			offset := e.Timestamp.Sub(start)
			if opts.Speed > 0 && i > 0 {
				// Sleep for the gap between this and previous entry,
				// scaled by Speed.
				prev := entries[i-1].Timestamp
				gap := e.Timestamp.Sub(prev)
				if scaled := time.Duration(float64(gap) / opts.Speed); scaled > 0 {
					select {
					case <-ctx.Done():
						return
					case <-time.After(scaled):
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case out <- ReplayEvent{Entry: e, Offset: offset, Index: i}:
			}
		}
	}()
	return out, errCh
}

// filterAndSort applies the ReplayOptions filters and returns
// chronologically-sorted entries. Sort is stable so entries with
// identical timestamps preserve their on-disk order.
func filterAndSort(entries []Entry, opts ReplayOptions) []Entry {
	out := make([]Entry, 0, len(entries))
	allow := map[EntryType]bool{}
	for _, t := range opts.Types {
		allow[t] = true
	}
	for _, e := range entries {
		if len(allow) > 0 && !allow[e.Type] {
			continue
		}
		if !opts.From.IsZero() && e.Timestamp.Before(opts.From) {
			continue
		}
		if !opts.Until.IsZero() && e.Timestamp.After(opts.Until) {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out
}

// FormatReplayEvent renders one event as a single-line summary
// suitable for CLI streaming. Types get a single-character glyph;
// content is truncated to keep terminal output readable.
func FormatReplayEvent(ev ReplayEvent) string {
	glyph := "·"
	switch ev.Entry.Type {
	case EntryUserInput:
		glyph = "→"
	case EntryAgentReply:
		glyph = "←"
	case EntryToolCall:
		glyph = "⚙"
	case EntryToolResult:
		glyph = "✓"
	case EntryError:
		glyph = "✗"
	case EntrySystem:
		glyph = "#"
	}
	content := strings.ReplaceAll(ev.Entry.Content, "\n", " ⏎ ")
	if len(content) > 200 {
		content = content[:200] + "…"
	}
	return fmt.Sprintf("[%6s] %s %s  %s",
		ev.Offset.Round(time.Millisecond).String(),
		glyph,
		string(ev.Entry.Type),
		content,
	)
}

// Replayer is a stateful wrapper around Replay for clients that
// want push-style consumption with backpressure. The simpler
// Replay channel API is usually enough — Replayer is a
// convenience for SSE / TUI consumers that need to control
// stepping themselves.
type Replayer struct {
	recorder  *FlightRecorder
	sessionID string
	opts      ReplayOptions
}

func NewReplayer(rec *FlightRecorder, sessionID string, opts ReplayOptions) *Replayer {
	return &Replayer{recorder: rec, sessionID: sessionID, opts: opts}
}

// Snapshot returns the full filtered+sorted entry list synchronously.
// Useful for "give me the whole session as one blob" callers (e.g.
// the CLI's --no-stream mode).
func (r *Replayer) Snapshot() ([]ReplayEvent, error) {
	if r.recorder == nil {
		return nil, errors.New("replayer: nil recorder")
	}
	entries, err := r.recorder.ReadSession(r.sessionID)
	if err != nil {
		return nil, err
	}
	entries = filterAndSort(entries, r.opts)
	if len(entries) == 0 {
		return nil, nil
	}
	start := entries[0].Timestamp
	out := make([]ReplayEvent, 0, len(entries))
	for i, e := range entries {
		out = append(out, ReplayEvent{
			Entry:  e,
			Offset: e.Timestamp.Sub(start),
			Index:  i,
		})
	}
	return out, nil
}
