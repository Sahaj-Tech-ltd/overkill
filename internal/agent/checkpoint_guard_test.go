package agent

import (
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"strings"
	"sync"
	"testing"
)

type fakeSnapshotter struct {
	mu    sync.Mutex
	calls []struct {
		Session string
		Reason  string
		Paths   []string
	}
	err error
}

func (f *fakeSnapshotter) Snapshot(sessionID, reason string, paths []string) error {
	f.mu.Lock()
	f.calls = append(f.calls, struct {
		Session string
		Reason  string
		Paths   []string
	}{sessionID, reason, append([]string(nil), paths...)})
	f.mu.Unlock()
	return f.err
}

func TestPreToolCheckpoint_NoSnapshotter(t *testing.T) {
	a := &Agent{}
	reason, err := a.preToolCheckpoint("patch", `{"path":"a.go"}`)
	if err != nil || reason != "" {
		t.Errorf("expected no-op when snapshotter nil, got reason=%q err=%v", reason, err)
	}
}

func TestPreToolCheckpoint_NonDestructiveSkipped(t *testing.T) {
	s := &fakeSnapshotter{}
	a := &Agent{}
	a.SetCheckpointSnapshotter(s)
	if _, err := a.preToolCheckpoint("fs_read", `{"path":"a.go"}`); err != nil {
		t.Errorf("read should be skipped: %v", err)
	}
	if len(s.calls) != 0 {
		t.Errorf("expected no snapshot for fs_read, got %v", s.calls)
	}
}

func TestPreToolCheckpoint_PatchSnaps(t *testing.T) {
	s := &fakeSnapshotter{}
	a := &Agent{_sessionID: "sess1"}
	a.SetCheckpointSnapshotter(s)
	reason, err := a.preToolCheckpoint("patch", `{"path":"src/foo.go"}`)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(reason, "patch") {
		t.Errorf("reason should name tool: %q", reason)
	}
	if len(s.calls) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(s.calls))
	}
	if s.calls[0].Session != "sess1" {
		t.Errorf("session mismatch: %s", s.calls[0].Session)
	}
	if len(s.calls[0].Paths) != 1 || s.calls[0].Paths[0] != "src/foo.go" {
		t.Errorf("paths wrong: %v", s.calls[0].Paths)
	}
}

func TestPreToolCheckpoint_ShellDestructive(t *testing.T) {
	s := &fakeSnapshotter{}
	a := &Agent{_sessionID: "x"}
	a.SetCheckpointSnapshotter(s)

	cases := []struct {
		cmd      string
		wantSnap bool
	}{
		{`{"command":"rm -rf build/"}`, true},
		{`{"command":"git reset --hard HEAD~1"}`, true},
		{`{"command":"echo hi > out.txt"}`, true},
		{`{"command":"ls -la"}`, true},
		{`{"command":"go test ./..."}`, true},
		{`{"command":"cat README.md"}`, true},
	}
	for _, tc := range cases {
		s.mu.Lock()
		before := len(s.calls)
		s.mu.Unlock()
		_, err := a.preToolCheckpoint("shell", tc.cmd)
		if err != nil {
			t.Errorf("%s: %v", tc.cmd, err)
		}
		s.mu.Lock()
		fired := len(s.calls) > before
		s.mu.Unlock()
		if fired != tc.wantSnap {
			t.Errorf("%s: wanted snapshot=%v, fired=%v", tc.cmd, tc.wantSnap, fired)
		}
	}
}

func TestPreToolCheckpoint_FailurePropagated(t *testing.T) {
	s := &fakeSnapshotter{err: errFakeSnapshot}
	a := &Agent{_sessionID: "x"}
	a.SetCheckpointSnapshotter(s)
	_, err := a.preToolCheckpoint("patch", `{"path":"x"}`)
	if err == nil {
		t.Error("expected error to bubble for emit")
	}
}

func TestPreToolCheckpoint_MalformedArgsSkipped(t *testing.T) {
	s := &fakeSnapshotter{}
	a := &Agent{}
	a.SetCheckpointSnapshotter(s)
	_, err := a.preToolCheckpoint("patch", `not-json`)
	if err != nil {
		t.Errorf("malformed args should be a no-op, got %v", err)
	}
	if len(s.calls) != 0 {
		t.Errorf("expected no snapshot, got %v", s.calls)
	}
}

var errFakeSnapshot = &fakeErr{"intentional"}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }

func TestPreToolCheckpointActualFSAndOpaqueShell(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		want       bool
	}{
		{"fs", `{"action":"write","path":"a"}`, true},
		{"fs", `{"action":"edit","path":"a"}`, true},
		{"fs", `{"action":"read","path":"a"}`, false},
		{"fs_patch", `{"files":[{"path":"a"},{"file_path":"b"}]}`, true},
		{"shell", `{"command":"r''m x"}`, true},
		{"shell", `{"command":"python3 -c 'open(\"x\",\"w\").write(\"x\")'"}`, true},
		{"shell", `{"command":"echo $(touch x)"}`, true},
		{"shell", `{"command":"rg text . > results"}`, true},
		{"shell", `{"command":"find . -exec rm {} ;"}`, true},
	} {
		t.Run(tc.name+tc.args, func(t *testing.T) {
			s := &fakeSnapshotter{}
			a := &Agent{_sessionID: "s"}
			a.SetCheckpointSnapshotter(s)
			_, err := a.preToolCheckpoint(tc.name, tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if (len(s.calls) > 0) != tc.want {
				t.Fatalf("checkpoint calls=%d want=%v", len(s.calls), tc.want)
			}
		})
	}
}

func TestPreToolCheckpointReaderCreatesNoRecoveryFiles(t *testing.T) {
	a := &Agent{_sessionID: "s"}
	snap := &fakeSnapshotter{}
	a.SetCheckpointSnapshotter(snap)
	a.privilege.Store(security.NewPrivilegeGate(security.ModeReader))
	for _, tool := range []string{"fs", "patch", "shell"} {
		_, err := a.preToolCheckpoint(tool, `{"action":"write","path":"a","command":"touch a"}`)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(snap.calls) != 0 {
		t.Fatal("reader operation created checkpoints")
	}
}
