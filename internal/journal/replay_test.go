package journal

import (
	"context"
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
	case err := <-errCh:
		t.Fatalf("unexpected err: %v", err)
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
