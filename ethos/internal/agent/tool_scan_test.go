package agent

import (
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
