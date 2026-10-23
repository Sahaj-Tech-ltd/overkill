package audit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func requireAuditTools(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			if os.Getenv("OVERKILL_REQUIRE_AUDIT_RUNTIME") == "1" {
				t.Fatalf("required audit runtime %s: %v", name, err)
			}
			t.Skipf("audit runtime %s unavailable: %v", name, err)
		}
	}
}
func auditFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
}

func TestAuditMixedGoTypeScriptPythonActuallyExecutes(t *testing.T) {
	requireAuditTools(t, "go", "node", "npm", "python3")
	dir := t.TempDir()
	files := map[string]string{
		"go/go.mod":         "module auditfixture\n\ngo 1.23\n",
		"go/calc.go":        "package calc\nfunc Add(a,b int)int{return a+b}\n",
		"go/calc_test.go":   "package calc\nimport \"testing\"\nfunc TestAdd(t *testing.T){if Add(2,3)!=5{t.Fatal(\"bad sum\")}}\n",
		"ts/package.json":   `{"scripts":{"build":"node build.cjs","test":"node --test calc.test.cjs"}}`,
		"ts/build.cjs":      "const fs=require('node:fs');const {stripTypeScriptTypes}=require('node:module');fs.writeFileSync('calc.cjs',stripTypeScriptTypes(fs.readFileSync('calc.ts','utf8')));\n",
		"ts/tsconfig.json":  `{"compilerOptions":{}}`,
		"ts/calc.ts":        "function add(a: number,b: number): number {return a+b;}\nmodule.exports={add};\n",
		"ts/calc.test.cjs":  "const assert=require('node:assert/strict');const {test}=require('node:test');const {add}=require('./calc.cjs');test('typed sum',()=>assert.equal(add(2,3),5));\n",
		"py/pyproject.toml": "[project]\nname='audit-fixture'\nversion='0.1.0'\n",
		"py/calc.py":        "def add(a, b):\n    return a + b\n",
		"py/test_calc.py":   "import unittest\nfrom calc import add\nclass SumTest(unittest.TestCase):\n    def test_sum(self):\n        self.assertEqual(add(2, 3), 5)\n",
	}
	for path, data := range files {
		auditFile(t, dir, path, data)
	}
	auditor := &Auditor{WorkDir: dir}
	snapshot, err := auditor.CaptureBaseline(t.Context())
	require.NoError(t, err)
	for _, path := range []string{"go/calc.go", "ts/calc.ts", "py/calc.py"} {
		require.Contains(t, snapshot.Files, path)
		auditFile(t, dir, path, files[path]+"\n")
	}
	claims := []Claim{{Description: "updated mixed language sources", Files: []string{"go/calc.go", "ts/calc.ts", "py/calc.py"}, BuildPass: true, TestsPass: true}}
	report, err := auditor.AuditCaptured(t.Context(), claims)
	require.NoError(t, err)
	require.True(t, report.Passed, "%+v", report.Findings)
	require.True(t, report.BuildOK)
	require.True(t, report.TestsOK)
	require.Len(t, report.Checks, 6)
	languages := map[string]int{}
	for _, check := range report.Checks {
		require.Equal(t, "passed", check.Status, "%+v", check)
		languages[check.Language]++
	}
	require.Equal(t, map[string]int{"go": 2, "typescript": 2, "python": 2}, languages)
	auditFile(t, dir, "py/test_calc.py", "import unittest\nclass Failing(unittest.TestCase):\n    def test_failure(self):\n        self.assertEqual(1, 2)\n")
	failed, err := auditor.AuditCaptured(t.Context(), claims)
	require.NoError(t, err)
	require.False(t, failed.Passed)
	require.False(t, failed.TestsOK)
	found := false
	for _, check := range failed.Checks {
		if check.Language == "python" && check.Kind == "test" {
			require.Equal(t, "failed", check.Status)
			require.Contains(t, check.Output, "AssertionError")
			found = true
		}
	}
	require.True(t, found)
}

func TestAuditUnavailableChecksDoNotPass(t *testing.T) {
	dir := t.TempDir()
	auditFile(t, dir, "package.json", `{"name":"no-verification-scripts"}`)
	auditor := &Auditor{WorkDir: dir}
	_, err := auditor.CaptureBaseline(t.Context())
	require.NoError(t, err)
	report, err := auditor.AuditCaptured(t.Context(), []Claim{{Description: "claims build and tests pass", BuildPass: true, TestsPass: true}})
	require.NoError(t, err)
	require.False(t, report.Passed)
	require.False(t, report.BuildOK)
	require.False(t, report.TestsOK)
	require.Len(t, report.Checks, 2)
	for _, check := range report.Checks {
		require.Equal(t, "unavailable", check.Status)
	}
}
func TestAuditClaimsCannotReadOutsideWorkspace(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	auditFile(t, outside, "secret.ts", "secret")
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "alias")))
	for _, path := range []string{"../secret.ts", filepath.Join(outside, "secret.ts"), "alias/secret.ts"} {
		report := &Report{Passed: true}
		(&Auditor{WorkDir: root}).checkFileClaim(context.Background(), Claim{Description: "changed file"}, path, nil, report)
		require.False(t, report.Passed, path)
	}
}
func TestAuditGitIgnoredModulesAreNotExecuted(t *testing.T) {
	requireAuditTools(t, "git", "node", "npm")
	dir := t.TempDir()
	_, err := runCmd(t.Context(), dir, "git", "init")
	require.NoError(t, err)
	auditFile(t, dir, ".gitignore", "research/\n")
	auditFile(t, dir, "package.json", `{"scripts":{"build":"node -e \"process.exit(0)\"","test":"node -e \"process.exit(0)\""}}`)
	auditFile(t, dir, "research/upstream/package.json", `{"scripts":{"build":"node -e \"require('fs').writeFileSync('EXECUTED','bad')\"","test":"node -e \"process.exit(1)\""}}`)
	auditor := &Auditor{WorkDir: dir}
	pre, err := TakeSnapshot(t.Context(), dir)
	require.NoError(t, err)
	report := auditor.Audit(t.Context(), pre, nil)
	require.True(t, report.Passed, "%+v", report.Findings)
	require.Len(t, report.Checks, 2)
	_, err = os.Stat(filepath.Join(dir, "research/upstream/EXECUTED"))
	require.True(t, os.IsNotExist(err))
}

type semanticFixture func(context.Context, string) (string, error)

func (s semanticFixture) Run(ctx context.Context, prompt string) (string, error) {
	return s(ctx, prompt)
}
func TestAuditSemanticRequiresExecutedEvidenceAndCoversClaims(t *testing.T) {
	auditor := &Auditor{WorkDir: t.TempDir(), SubAgent: semanticFixture(func(_ context.Context, prompt string) (string, error) {
		require.Contains(t, prompt, "actual-session-history-marker")
		return `{"findings":[]}`, nil
	})}
	report := &Report{Passed: true}
	auditor.runSemanticAudit(WithTranscript(t.Context(), "actual-session-history-marker"), []Claim{{Description: "implemented feature"}}, report)
	require.False(t, report.Passed)
	require.Contains(t, report.Findings[0].Actual, "did not verify")
	auditor.SubAgent = semanticFixture(func(context.Context, string) (string, error) { return "", os.ErrPermission })
	report = &Report{Passed: true}
	auditor.runSemanticAudit(t.Context(), []Claim{{Description: "feature"}}, report)
	require.False(t, report.Passed)
}
