package journal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeReplaySession(t *testing.T) (*FlightRecorder, string) {
	t.Helper()
	dir := t.TempDir()
	rec := NewFlightRecorder(filepath.Join(dir, "journal"), "sess-replay")
	if err := rec.RecordInput("fix the bug"); err != nil {
		t.Fatal(err)
	}
	if err := rec.RecordToolCall("Read", []byte(`{"path":"foo.go"}`)); err != nil {
		t.Fatal(err)
	}
	if err := rec.RecordToolResult("Read", []byte(`{"content":"..."}`)); err != nil {
		t.Fatal(err)
	}
	if err := rec.RecordReply("found the issue"); err != nil {
		t.Fatal(err)
	}
	return rec, "sess-replay"
}

func TestReplay_YieldsAllEntriesInOrder(t *testing.T) {
	rec, sid := writeReplaySession(t)
	out, errCh := rec.Replay(context.Background(), sid, ReplayOptions{})
	var got []ReplayEvent
	for ev := range out {
		got = append(got, ev)
	}
	select {
	case err, ok := <-errCh:
		if ok {
			t.Fatalf("unexpected err: %v", err)
		}
	default:
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 events, got %d", len(got))
	}
	if got[0].Entry.Type != EntryUserInput {
		t.Errorf("first should be user input, got %s", got[0].Entry.Type)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Index != i {
			t.Errorf("Index should be 0-based sequential, got %d at i=%d", got[i].Index, i)
		}
		if got[i].Offset < got[i-1].Offset {
			t.Errorf("offsets should be monotonic")
		}
	}
}

func TestReplayClosesBothChannelsOnEveryExit(t *testing.T) {
	for _, scenario := range []string{"empty", "success", "cancelled", "read-error"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			recorder := NewFlightRecorder(dir, "session")
			if scenario != "read-error" {
				if err := os.Mkdir(filepath.Join(dir, "raw"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "success" {
				if err := recorder.RecordInput("recorded input"); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "cancelled" {
				first := Entry{SessionID: "session", Timestamp: time.Now(), Content: "first"}
				second := first
				second.Timestamp = first.Timestamp.Add(time.Hour)
				var lines []byte
				for _, entry := range []Entry{first, second} {
					data, err := json.Marshal(entry)
					if err != nil {
						t.Fatal(err)
					}
					lines = append(append(lines, data...), '\n')
				}
				if err := os.WriteFile(filepath.Join(dir, "raw", "fixture.jsonl"), lines, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			replayCtx, stop := context.WithCancel(ctx)
			defer stop()
			options := ReplayOptions{}
			if scenario == "cancelled" {
				options.Speed = 1
			}
			events, errors := recorder.Replay(replayCtx, "session", options)
			count := 0
			for events != nil || errors != nil {
				select {
				case _, ok := <-events:
					if !ok {
						events = nil
					} else if scenario == "cancelled" {
						stop()
					}
				case err, ok := <-errors:
					if !ok {
						errors = nil
					} else {
						count++
						if err == nil {
							t.Fatal("nil error emitted")
						}
					}
				case <-ctx.Done():
					t.Fatal("replay did not close both channels")
				}
			}
			if scenario == "read-error" && count != 1 || scenario != "read-error" && count != 0 {
				t.Fatalf("unexpected error count: %d", count)
			}
		})
	}
}

func TestReplay_FilterByType(t *testing.T) {
	rec, sid := writeReplaySession(t)
	out, _ := rec.Replay(context.Background(), sid, ReplayOptions{
		Types: []EntryType{EntryToolCall, EntryToolResult},
	})
	var count int
	for ev := range out {
		if ev.Entry.Type != EntryToolCall && ev.Entry.Type != EntryToolResult {
			t.Errorf("type filter let through %s", ev.Entry.Type)
		}
		count++
	}
	if count != 2 {
		t.Errorf("expected 2 tool events, got %d", count)
	}
}

func TestReplay_FilterByTimeRange(t *testing.T) {
	rec, sid := writeReplaySession(t)
	// Use a wide future window — should drop everything.
	future := time.Now().Add(time.Hour)
	out, _ := rec.Replay(context.Background(), sid, ReplayOptions{From: future})
	count := 0
	for range out {
		count++
	}
	if count != 0 {
		t.Errorf("future From should drop all entries, got %d", count)
	}
}

func TestReplay_ContextCancelStopsStream(t *testing.T) {
	rec, sid := writeReplaySession(t)
	ctx, cancel := context.WithCancel(context.Background())
	out, _ := rec.Replay(ctx, sid, ReplayOptions{})
	// Consume first event then cancel.
	<-out
	cancel()
	// Drain — should close without further sends. Use a deadline
	// so a goroutine leak makes the test hang loudly.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-out:
			if !ok {
				return // closed cleanly
			}
		case <-deadline:
			t.Fatal("replay did not stop within 2s of cancel")
		}
	}
}

func TestReplay_SnapshotMatchesStream(t *testing.T) {
	rec, sid := writeReplaySession(t)
	r := NewReplayer(rec, sid, ReplayOptions{})
	snap, err := r.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	out, _ := rec.Replay(context.Background(), sid, ReplayOptions{})
	var streamed []ReplayEvent
	for ev := range out {
		streamed = append(streamed, ev)
	}
	if len(snap) != len(streamed) {
		t.Fatalf("snapshot vs stream count: %d vs %d", len(snap), len(streamed))
	}
	for i := range snap {
		if snap[i].Entry.ID != streamed[i].Entry.ID {
			t.Errorf("ordering mismatch at i=%d", i)
		}
	}
}

func TestFormatReplayEvent_RendersGlyphs(t *testing.T) {
	cases := map[EntryType]string{
		EntryUserInput:  "→",
		EntryAgentReply: "←",
		EntryToolCall:   "⚙",
		EntryToolResult: "✓",
		EntryError:      "✗",
		EntrySystem:     "#",
	}
	for typ, glyph := range cases {
		ev := ReplayEvent{
			Entry: Entry{Type: typ, Content: "x"},
		}
		got := FormatReplayEvent(ev)
		if !strings.Contains(got, glyph) {
			t.Errorf("expected glyph %s for %s in %q", glyph, typ, got)
		}
	}
}

func TestFormatReplayEvent_TruncatesLongContent(t *testing.T) {
	long := strings.Repeat("x", 500)
	ev := ReplayEvent{Entry: Entry{Type: EntryAgentReply, Content: long}}
	got := FormatReplayEvent(ev)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("long content should be truncated: %s", got[len(got)-20:])
	}
}
