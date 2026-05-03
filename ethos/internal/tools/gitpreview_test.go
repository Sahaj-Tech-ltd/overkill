package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		require.NoError(t, cmd.Run(), "git %v", args)
	}
	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")

	err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0o644)
	require.NoError(t, err)
	run("add", ".")
	run("commit", "-m", "initial")

	return dir
}

func commitFile(t *testing.T, dir, path, content, message string) {
	t.Helper()
	fullPath := filepath.Join(dir, path)
	err := os.MkdirAll(filepath.Dir(fullPath), 0o755)
	require.NoError(t, err)
	err = os.WriteFile(fullPath, []byte(content), 0o644)
	require.NoError(t, err)

	cmd := exec.Command("git", "add", path)
	cmd.Dir = dir
	require.NoError(t, cmd.Run())

	cmd = exec.Command("git", "commit", "-m", message)
	cmd.Dir = dir
	require.NoError(t, cmd.Run())
}

func TestGitGeneratePushPreview_NoUpstream(t *testing.T) {
	dir := setupGitRepo(t)

	commitFile(t, dir, "hello.txt", "world", "add hello")

	preview, err := GeneratePushPreview(context.Background(), dir)
	require.NoError(t, err)
	require.NotNil(t, preview)

	assert.NotEmpty(t, preview.Commits, "should have at least one commit")
	assert.False(t, preview.HasSecrets, "no secrets in clean files")
}

func TestGitGeneratePushPreview_WithCommits(t *testing.T) {
	dir := setupGitRepo(t)

	commitFile(t, dir, "a.txt", "aaa", "first commit")
	commitFile(t, dir, "b.txt", "bbb", "second commit")
	commitFile(t, dir, "c.txt", "ccc", "third commit")

	preview, err := GeneratePushPreview(context.Background(), dir)
	require.NoError(t, err)
	require.NotNil(t, preview)

	assert.GreaterOrEqual(t, len(preview.Commits), 3, "should have at least 3 commits")

	messages := make(map[string]bool)
	for _, c := range preview.Commits {
		messages[c.Message] = true
		assert.NotEmpty(t, c.Hash)
		assert.NotEmpty(t, c.Author)
	}
	assert.True(t, messages["third commit"])
	assert.True(t, messages["second commit"])
	assert.True(t, messages["first commit"])
}

func TestGitGeneratePushPreview_FileDiffs(t *testing.T) {
	dir := setupGitRepo(t)

	commitFile(t, dir, "internal/auth/middleware.go", "package auth\n\nfunc Middleware() {}\n", "add middleware")
	commitFile(t, dir, "internal/auth/token.go", "package auth\n\nfunc Token() string { return \"x\" }\n", "add token")

	preview, err := GeneratePushPreview(context.Background(), dir)
	require.NoError(t, err)
	require.NotNil(t, preview)

	assert.NotEmpty(t, preview.Files, "should have file diffs")

	paths := make(map[string]bool)
	for _, f := range preview.Files {
		paths[f.Path] = true
		assert.NotEmpty(t, f.Status)
	}
	assert.True(t, paths["internal/auth/middleware.go"])
	assert.True(t, paths["internal/auth/token.go"])
}

func TestGitScanForSecrets_DetectsBearerToken(t *testing.T) {
	dir := setupGitRepo(t)

	commitFile(t, dir, "client.go", `package main

func main() {
	headers["Authorization"] = "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
}
`, "add client with bearer")

	hits, err := ScanForSecrets(context.Background(), dir)
	require.NoError(t, err)
	assert.NotEmpty(t, hits, "should detect bearer token or JWT")

	found := false
	for _, h := range hits {
		if h.Pattern == "bearer token" || h.Pattern == "JWT token" {
			found = true
			assert.Equal(t, "client.go", h.File)
			assert.Greater(t, h.Line, 0)
			assert.Contains(t, h.Preview, "[REDACTED]")
			assert.NotContains(t, h.Preview, "eyJhbGci")
		}
	}
	assert.True(t, found, "expected bearer token or JWT detection")
}

func TestGitScanForSecrets_DetectsAWSKey(t *testing.T) {
	dir := setupGitRepo(t)

	commitFile(t, dir, "config.go", `package main

var accessKey = "AKIAIOSFODNN7EXAMPLE"
`, "add config with aws key")

	hits, err := ScanForSecrets(context.Background(), dir)
	require.NoError(t, err)
	assert.NotEmpty(t, hits, "should detect AWS key")

	found := false
	for _, h := range hits {
		if h.Pattern == "AWS access key" {
			found = true
			assert.Equal(t, "config.go", h.File)
			assert.Contains(t, h.Preview, "[REDACTED]")
			assert.NotContains(t, h.Preview, "AKIAIOSFODNN7EXAMPLE")
		}
	}
	assert.True(t, found, "expected AWS access key detection")
}

func TestGitScanForSecrets_DetectsPrivateKey(t *testing.T) {
	dir := setupGitRepo(t)

	commitFile(t, dir, "server.pem", `-----BEGIN RSA PRIVATE KEY-----
MIIBOgIBAAJBALRiMLAHudeSA/x3hB2f+2NRkJMg
-----END RSA PRIVATE KEY-----
`, "add private key")

	hits, err := ScanForSecrets(context.Background(), dir)
	require.NoError(t, err)
	assert.NotEmpty(t, hits, "should detect private key")

	found := false
	for _, h := range hits {
		if h.Pattern == "private key" {
			found = true
			assert.Equal(t, "server.pem", h.File)
		}
	}
	assert.True(t, found, "expected private key detection")
}

func TestGitScanForSecrets_CleanFiles(t *testing.T) {
	dir := setupGitRepo(t)

	commitFile(t, dir, "clean.go", `package main

func Add(a, b int) int {
	return a + b
}
`, "add clean code")

	hits, err := ScanForSecrets(context.Background(), dir)
	require.NoError(t, err)
	assert.Empty(t, hits, "no secrets should be detected in clean files")
}

func TestGitFormatPreview_WithSecrets(t *testing.T) {
	preview := &PushPreview{
		Commits: []CommitInfo{
			{Hash: "abc1234567890", Message: "feat: add auth", Author: "Test", Date: "2026-04-30"},
		},
		Files: []FileDiff{
			{Path: "internal/auth/token.go", Status: "A", Additions: 45, Deletions: 0},
		},
		HasSecrets: true,
		SecretHits: []SecretHit{
			{File: "internal/auth/token.go", Line: 15, Pattern: "bearer token", Preview: "Bearer [REDACTED]..."},
		},
	}

	out := FormatPreview(preview)
	assert.Contains(t, out, "PUSH PREVIEW")
	assert.Contains(t, out, "SECRETS DETECTED")
	assert.Contains(t, out, "BLOCK: Remove secrets before pushing")
	assert.Contains(t, out, "bearer token")
	assert.Contains(t, out, "[REDACTED]")
	assert.Contains(t, out, "internal/auth/token.go:15")
	assert.Contains(t, out, "abc1234")
}

func TestGitFormatPreview_NoSecrets(t *testing.T) {
	preview := &PushPreview{
		Commits: []CommitInfo{
			{Hash: "def5678901234", Message: "chore: cleanup", Author: "Test", Date: "2026-04-30"},
		},
		Files:      []FileDiff{},
		HasSecrets: false,
		SecretHits: nil,
	}

	out := FormatPreview(preview)
	assert.Contains(t, out, "PUSH PREVIEW")
	assert.Contains(t, out, "Safe to push")
	assert.NotContains(t, out, "SECRETS DETECTED")
	assert.NotContains(t, out, "BLOCK")
}

func TestGitFormatPreview_MultipleCommits(t *testing.T) {
	preview := &PushPreview{
		Commits: []CommitInfo{
			{Hash: "aaa1111111111", Message: "feat: add auth middleware", Author: "Alice", Date: "2026-04-28"},
			{Hash: "bbb2222222222", Message: "fix: token refresh bug", Author: "Bob", Date: "2026-04-29"},
			{Hash: "ccc3333333333", Message: "chore: update deps", Author: "Charlie", Date: "2026-04-30"},
		},
		Files: []FileDiff{
			{Path: "internal/auth/middleware.go", Status: "M", Additions: 12, Deletions: 3},
			{Path: "internal/auth/token.go", Status: "A", Additions: 45, Deletions: 0},
			{Path: "internal/auth/old.go", Status: "D", Additions: 0, Deletions: 28},
		},
		HasSecrets: false,
	}

	out := FormatPreview(preview)
	assert.Contains(t, out, "Commits (3)")
	assert.Contains(t, out, "aaa1111 feat: add auth middleware")
	assert.Contains(t, out, "bbb2222 fix: token refresh bug")
	assert.Contains(t, out, "ccc3333 chore: update deps")
	assert.Contains(t, out, "internal/auth/middleware.go")
	assert.Contains(t, out, "internal/auth/token.go")
	assert.Contains(t, out, "internal/auth/old.go")
	assert.Contains(t, out, "Safe to push")
}
