package personality

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEventLog_AppendThenLatestRoundtrip(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "state.json")
	log := NewEventLog(snap)

	for i, payload := range []string{`{"v":1}`, `{"v":2}`, `{"v":3}`} {
		if err := log.Append([]byte(payload)); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	got, err := log.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected an entry")
	}
	if string(got.State) != `{"v":3}` {
		t.Errorf("expected latest state {v:3}, got %s", string(got.State))
	}
	if got.Version != 3 {
		t.Errorf("version should be 3, got %d", got.Version)
	}
}

func TestEventLog_LatestSkipsCorruptTrailingLine(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "state.json")
	log := NewEventLog(snap)

	if err := log.Append([]byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash mid-write — append a partial line.
	f, _ := os.OpenFile(log.path, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("{partial json no closing")
	_ = f.Close()

	got, err := log.Latest()
	if err != nil {
		t.Logf("scan err (expected for partial line): %v", err)
	}
	if got == nil || string(got.State) != `{"v":1}` {
		t.Errorf("partial line should be skipped; last good is {v:1}; got %+v", got)
	}
}

func TestEventLog_LatestOnMissingFileIsNil(t *testing.T) {
	log := NewEventLog(filepath.Join(t.TempDir(), "nope.json"))
	got, err := log.Latest()
	if err != nil {
		t.Errorf("missing log should not error: %v", err)
	}
	if got != nil {
		t.Errorf("missing log should return nil: %+v", got)
	}
}

func TestLoadWithFallback_PrefersSnapshot(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "state.json")
	if err := os.WriteFile(snap, []byte(`{"src":"snapshot"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	log := NewEventLog(snap)
	_ = log.Append([]byte(`{"src":"log"}`))

	out, err := LoadWithFallback(snap, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"src":"snapshot"}` {
		t.Errorf("expected snapshot, got %s", string(out))
	}
}

func TestLoadWithFallback_RecoversFromMissingSnapshot(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "state.json")
	log := NewEventLog(snap)
	_ = log.Append([]byte(`{"src":"log"}`))

	out, err := LoadWithFallback(snap, log, nil)
	if err != nil {
		t.Fatalf("recovery should not error: %v", err)
	}
	if string(out) != `{"src":"log"}` {
		t.Errorf("expected log fallback, got %s", string(out))
	}
}

func TestLoadWithFallback_RecoversFromCorruptSnapshot(t *testing.T) {
	dir := t.TempDir()
	snap := filepath.Join(dir, "state.json")
	if err := os.WriteFile(snap, []byte("{this is not valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := NewEventLog(snap)
	_ = log.Append([]byte(`{"src":"log","ok":true}`))

	valid := func(b []byte) bool {
		var tmp map[string]any
		return json.Unmarshal(b, &tmp) == nil
	}
	out, err := LoadWithFallback(snap, log, valid)
	if err != nil {
		t.Fatalf("recovery should not error: %v", err)
	}
	if string(out) != `{"src":"log","ok":true}` {
		t.Errorf("expected log fallback for corrupt snapshot, got %s", string(out))
	}
}

func TestLoadWithFallback_NoLogNoSnapshotReturnsErr(t *testing.T) {
	snap := filepath.Join(t.TempDir(), "missing.json")
	_, err := LoadWithFallback(snap, NewEventLog(snap), nil)
	if err == nil {
		t.Error("expected error when neither snapshot nor log exists")
	}
}
