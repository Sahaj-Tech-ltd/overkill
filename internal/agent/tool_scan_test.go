package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
)

type stubScanner struct {
	name    string
	blocked bool
	desc    string
}

func (s stubScanner) Name() string { return s.name }
func (s stubScanner) Scan(input string) (*security.ScanResult, error) {
	if !s.blocked {
		return &security.ScanResult{}, nil
	}
	return &security.ScanResult{
		Blocked:  true,
		Findings: []security.Finding{{Description: s.desc}},
	}, nil
}

func TestPreToolScan_NoScanners(t *testing.T) {
	a := &Agent{}
	blocked, _ := a.preToolScan("shell", `{"command":"rm -rf /"}`)
	if blocked {
		t.Error("no scanners installed should not block")
	}
}

func TestPreToolScan_NonRiskyToolPasses(t *testing.T) {
	a := &Agent{scanners: []security.Scanner{stubScanner{name: "x", blocked: true, desc: "would block"}}}
	blocked, _ := a.preToolScan("git_status", `{}`)
	if blocked {
		t.Error("non-risky tool should be skipped by scan extractor")
	}
}

func TestPreToolScan_ShellBlocked(t *testing.T) {
	a := &Agent{scanners: []security.Scanner{stubScanner{name: "command", blocked: true, desc: "rm -rf is forbidden"}}}
	blocked, reason := a.preToolScan("shell", `{"command":"rm -rf /"}`)
	if !blocked {
		t.Fatal("expected block")
	}
	if !strings.Contains(reason, "rm -rf is forbidden") {
		t.Errorf("reason missing finding description: %q", reason)
	}
	if !strings.Contains(reason, "command") {
		t.Errorf("reason missing scanner name: %q", reason)
	}
}

func TestPreToolScan_ShellClean(t *testing.T) {
	a := &Agent{scanners: []security.Scanner{stubScanner{name: "command", blocked: false}}}
	blocked, _ := a.preToolScan("shell", `{"command":"ls -la"}`)
	if blocked {
		t.Error("clean command should pass")
	}
}

func TestPreToolScan_MalformedArgsScanned(t *testing.T) {
	// Unmarshal failure falls back to scanning the raw args — better to
	// over-scan a weird payload than let a malformed-but-real command slip.
	a := &Agent{scanners: []security.Scanner{stubScanner{name: "command", blocked: true, desc: "matched"}}}
	blocked, _ := a.preToolScan("shell", `not even json`)
	if !blocked {
		t.Error("malformed args should still hit the scanner")
	}
}

func TestPreToolScan_PatchScansPath(t *testing.T) {
	a := &Agent{scanners: []security.Scanner{stubScanner{name: "path", blocked: true, desc: "traversal"}}}
	blocked, _ := a.preToolScan("patch", `{"path":"../etc/passwd"}`)
	if !blocked {
		t.Error("patch with risky path should be scanned")
	}
}

// ── Protected-path gate tests ───────────────────────────────────────

func TestCheckProtectedPaths_BlocksHomeRelativeWrite(t *testing.T) {
	args := `{"file_path": "~/.overkill/memories/relationship-arc.json", "content": "..."}`
	blocked, reason := checkProtectedPaths("Write", args)
	if !blocked {
		t.Fatal("expected ~/.overkill/memories/... write to block")
	}
	if !strings.Contains(reason, "protected-path") {
		t.Errorf("reason should name the gate: %q", reason)
	}
}

func TestCheckProtectedPaths_BlocksAbsoluteWrite(t *testing.T) {
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".overkill", "memories", "fingerprint.json")
	args := `{"file_path": "` + p + `"}`
	blocked, _ := checkProtectedPaths("Edit", args)
	if !blocked {
		t.Errorf("absolute %s should block", p)
	}
}

func TestCheckProtectedPaths_BlocksJournalAndFailhypo(t *testing.T) {
	cases := []string{
		`{"file_path": "~/.overkill/failed_hypotheses/2026-05-14.jsonl"}`,
		`{"file_path": "~/.overkill/journal/raw/2026-05-14.jsonl"}`,
		`{"file_path": "~/.overkill/alerts/store.json"}`,
		`{"file_path": "~/.overkill/snapshots/x.tar.gz"}`,
		`{"file_path": "~/.overkill/receipts/chain.jsonl"}`,
	}
	for _, c := range cases {
		blocked, _ := checkProtectedPaths("Write", c)
		if !blocked {
			t.Errorf("expected block for %s", c)
		}
	}
}

func TestCheckProtectedPaths_AllowsOtherPaths(t *testing.T) {
	cases := []string{
		`{"file_path": "/home/user/code/main.go"}`,
		`{"file_path": "~/projects/foo/bar.txt"}`,
		`{"file_path": "/tmp/scratch.md"}`,
		`{"file_path": "~/.overkill/config.toml"}`,
	}
	for _, c := range cases {
		blocked, _ := checkProtectedPaths("Write", c)
		if blocked {
			t.Errorf("non-protected path should not block: %s", c)
		}
	}
}

func TestCheckProtectedPaths_NonWriteToolPasses(t *testing.T) {
	args := `{"file_path": "~/.overkill/memories/relationship-arc.json"}`
	blocked, _ := checkProtectedPaths("Read", args)
	if blocked {
		t.Error("Read tool should not be gated by protected-path check")
	}
}

func TestCheckProtectedPaths_MalformedJSONDoesNotBlock(t *testing.T) {
	blocked, _ := checkProtectedPaths("Write", "not json {{{")
	if blocked {
		t.Error("malformed JSON should not block at this gate")
	}
}

func TestCheckProtectedPaths_HandlesPathKeyAliases(t *testing.T) {
	blocked, _ := checkProtectedPaths("write_file", `{"path": "~/.overkill/memories/x.json"}`)
	if !blocked {
		t.Error("write_file with path= should still block protected path")
	}
}

func TestPathInProtectedSubdir_RelativeContaining(t *testing.T) {
	sub, hit := pathInProtectedSubdir(".overkill/memories/foo.json")
	if !hit || sub != "memories" {
		t.Errorf("relative .overkill/memories/ should hit: sub=%q hit=%v", sub, hit)
	}
}

func TestCheckProtectedPaths_BlocksStandingOrdersFile(t *testing.T) {
	cases := []string{
		`{"file_path": "~/.overkill/standing-orders.jsonl"}`,
		`{"file_path": "/home/user/.overkill/standing-orders.jsonl"}`,
	}
	for _, c := range cases {
		blocked, _ := checkProtectedPaths("Write", c)
		if !blocked {
			t.Errorf("standing-orders.jsonl should block: %s", c)
		}
	}
}
