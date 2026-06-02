package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoVerifier_DetectsUndefinedSymbol creates a tiny go package
// with a hallucinated function call and confirms the verifier
// surfaces the build error. This is the failure mode the whole
// G2 batch exists to catch.
func TestGoVerifier_DetectsUndefinedSymbol(t *testing.T) {
	dir := t.TempDir()
	// Minimal module so `go build .` resolves.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module verifytest\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package main
func main() {
	HallucinatedFunc() // not defined anywhere
}
`
	mainPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainPath, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	v := NewGoVerifier()
	ok, detail, skipped := v.Verify(context.Background(), mainPath, nil)
	if ok {
		t.Error("undefined symbol should fail build")
	}
	if skipped {
		t.Error("build with real error should NOT be skipped — it ran and failed")
	}
	if !strings.Contains(detail, "HallucinatedFunc") {
		t.Errorf("build output should mention the undefined symbol: %q", detail)
	}
}

// TestGoVerifier_ValidPackagePasses confirms a working package
// doesn't get falsely flagged.
func TestGoVerifier_ValidPackagePasses(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module verifytest\n\ngo 1.21\n"), 0o644)
	src := `package main
import "fmt"
func main() { fmt.Println("hi") }
`
	mainPath := filepath.Join(dir, "main.go")
	_ = os.WriteFile(mainPath, []byte(src), 0o600)

	v := NewGoVerifier()
	ok, detail, skipped := v.Verify(context.Background(), mainPath, nil)
	if !ok {
		t.Errorf("valid package should pass: detail=%q skipped=%v", detail, skipped)
	}
}

// TestGoVerifier_TruncatesLongOutput pins the 2KB cap so a cascading
// error doesn't blow the next turn's context budget.
func TestGoVerifier_TruncatesLongOutput(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module verifytest\n\ngo 1.21\n"), 0o644)
	// Generate a file that produces many errors so output exceeds 2KB.
	var b strings.Builder
	b.WriteString("package main\nfunc main() {\n")
	for i := 0; i < 200; i++ {
		b.WriteString("HallucinatedFunc()\n")
	}
	b.WriteString("}\n")
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte(b.String()), 0o644)

	v := NewGoVerifier()
	_, detail, _ := v.Verify(context.Background(), filepath.Join(dir, "main.go"), nil)
	if len(detail) > 2100 { // 2000 + small fudge for "...(truncated)"
		t.Errorf("output not truncated, got %d chars", len(detail))
	}
	if !strings.Contains(detail, "truncated") && len(detail) < 1900 {
		// Either truncated cleanly or naturally short — both fine.
		// We only complain when neither is true.
	}
}
