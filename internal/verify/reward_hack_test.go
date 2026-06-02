package verify

import (
	"strings"
	"testing"
)

func TestAuditPaths_FlagsGoTestWithoutCode(t *testing.T) {
	findings := AuditPaths([]string{"internal/auth/auth_test.go"})
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %+v", findings)
	}
	if findings[0].ExpectedCode != "internal/auth/auth.go" {
		t.Errorf("expected path: %q", findings[0].ExpectedCode)
	}
}

func TestAuditPaths_PassesGoTestWithCode(t *testing.T) {
	findings := AuditPaths([]string{
		"internal/auth/auth_test.go",
		"internal/auth/auth.go",
	})
	if len(findings) != 0 {
		t.Errorf("test+code pair should pass, got %+v", findings)
	}
}

func TestAuditPaths_PassesTestWithSibling(t *testing.T) {
	// helpers_test.go modified but the developer changed utils.go in
	// the same directory — anyCodeInSameDir gives the agent rope.
	findings := AuditPaths([]string{
		"internal/auth/helpers_test.go",
		"internal/auth/utils.go",
	})
	if len(findings) != 0 {
		t.Errorf("sibling code change should pass, got %+v", findings)
	}
}

func TestAuditPaths_FlagsTSSpecWithoutCode(t *testing.T) {
	findings := AuditPaths([]string{"src/api/login.spec.ts"})
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %+v", findings)
	}
	if findings[0].ExpectedCode != "src/api/login.ts" {
		t.Errorf("ts spec → ts: %q", findings[0].ExpectedCode)
	}
}

func TestAuditPaths_FlagsTSTestWithoutCode(t *testing.T) {
	findings := AuditPaths([]string{"src/components/button.test.tsx"})
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %+v", findings)
	}
	if findings[0].ExpectedCode != "src/components/button.tsx" {
		t.Errorf("tsx test → tsx: %q", findings[0].ExpectedCode)
	}
}

func TestAuditPaths_FlagsPythonTestPrefix(t *testing.T) {
	findings := AuditPaths([]string{"app/test_billing.py"})
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %+v", findings)
	}
	if findings[0].ExpectedCode != "app/billing.py" {
		t.Errorf("test_X.py → X.py: %q", findings[0].ExpectedCode)
	}
}

func TestAuditPaths_FlagsPythonTestSuffix(t *testing.T) {
	findings := AuditPaths([]string{"app/billing_test.py"})
	if len(findings) != 1 {
		t.Fatal("want 1 finding")
	}
	if findings[0].ExpectedCode != "app/billing.py" {
		t.Errorf("X_test.py → X.py: %q", findings[0].ExpectedCode)
	}
}

func TestAuditPaths_FlagsRustTest(t *testing.T) {
	findings := AuditPaths([]string{"src/parser_test.rs"})
	if len(findings) != 1 {
		t.Fatal("want 1 finding")
	}
	if findings[0].ExpectedCode != "src/parser.rs" {
		t.Errorf("expected: %q", findings[0].ExpectedCode)
	}
}

func TestAuditPaths_MultipleTestFilesAllFlagged(t *testing.T) {
	findings := AuditPaths([]string{
		"a/x_test.go",
		"a/y_test.go",
		"a/z_test.go",
	})
	if len(findings) != 3 {
		t.Errorf("each test should flag independently, got %d", len(findings))
	}
}

func TestAuditPaths_NonTestFilesAlone(t *testing.T) {
	findings := AuditPaths([]string{"internal/auth/auth.go", "internal/auth/utils.go"})
	if len(findings) != 0 {
		t.Errorf("no test files = no findings, got %+v", findings)
	}
}

func TestAuditPaths_EmptyInput(t *testing.T) {
	if got := AuditPaths(nil); got != nil {
		t.Errorf("nil paths → nil: %v", got)
	}
	if got := AuditPaths([]string{}); got != nil {
		t.Errorf("empty paths → nil: %v", got)
	}
}

func TestLooksLikeTestFile_DetectsGenericTestDirs(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"a/b/c/file_test.go", true},
		{"a/tests/helper.py", true},
		{"a/test/helper.go", true},
		{"a/__tests__/comp.js", true},
		{"a/spec/widget.coffee", true},
		{"a/b/c/regular.go", false},
		{"a/b/c/test_dir_name/regular.go", false}, // not a literal "test" segment
	}
	for _, c := range cases {
		if got := looksLikeTestFile(c.path); got != c.want {
			t.Errorf("looksLikeTestFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestExpectedCodePath_UnknownConventionReturnsEmpty(t *testing.T) {
	// Files under tests/ that don't follow a recognized pairing —
	// we'd rather skip the flag than false-flag.
	got := expectedCodePath("tests/integration/full_flow.go")
	if got != "" {
		t.Errorf("unknown convention should return empty, got %q", got)
	}
}

func TestFormatRewardHackMessage_EmptyForNoFindings(t *testing.T) {
	if got := FormatRewardHackMessage(nil); got != "" {
		t.Errorf("nil findings → empty: %q", got)
	}
}

func TestFormatRewardHackMessage_MentionsBothPaths(t *testing.T) {
	findings := []RewardHackFinding{{
		TestPath:     "auth/login_test.go",
		ExpectedCode: "auth/login.go",
		Reason:       "test changed without code",
	}}
	msg := FormatRewardHackMessage(findings)
	if !strings.Contains(msg, "auth/login_test.go") {
		t.Errorf("missing test path: %q", msg)
	}
	if !strings.Contains(msg, "auth/login.go") {
		t.Errorf("missing expected path: %q", msg)
	}
	if !strings.Contains(msg, "lowering the bar") {
		t.Errorf("missing rationale: %q", msg)
	}
}

func TestFormatRewardHackMessage_PluralCount(t *testing.T) {
	findings := []RewardHackFinding{
		{TestPath: "a_test.go", ExpectedCode: "a.go"},
		{TestPath: "b_test.go", ExpectedCode: "b.go"},
	}
	msg := FormatRewardHackMessage(findings)
	if !strings.Contains(msg, "2 test files") {
		t.Errorf("plural count missing: %q", msg)
	}
}

func TestAuditPaths_PathsAreCleaned(t *testing.T) {
	// Paths with redundant separators should still pair up.
	findings := AuditPaths([]string{
		"./internal/auth/auth_test.go",
		"internal/auth/auth.go",
	})
	if len(findings) != 0 {
		t.Errorf("cleaned paths should pair, got %+v", findings)
	}
}
