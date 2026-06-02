package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAtMentions_None(t *testing.T) {
	got := loadAtMentions("hello there")
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestLoadAtMentions_LoadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	got := loadAtMentions("look at @hello.txt please")
	if !strings.Contains(got, "contents") {
		t.Fatalf("expected file contents in mention block, got %q", got)
	}
	if !strings.Contains(got, "@hello.txt") {
		t.Fatalf("expected file path tag, got %q", got)
	}
}

func TestLoadAtMentions_MissingFile(t *testing.T) {
	got := loadAtMentions("see @does-not-exist.xyz")
	if !strings.Contains(got, "not a readable file") {
		t.Fatalf("expected unreadable marker, got %q", got)
	}
}

func TestLoadAtMentions_DedupesAndCaps(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxMentionFiles+5; i++ {
		path := filepath.Join(dir, "f"+itoaQuick(i)+".txt")
		_ = os.WriteFile(path, []byte("x"), 0o600)
	}
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < maxMentionFiles+5; i++ {
		b.WriteString(" @f")
		b.WriteString(itoaQuick(i))
		b.WriteString(".txt")
	}
	// Repeat one to exercise dedup.
	b.WriteString(" @f0.txt")
	got := loadAtMentions(b.String())
	count := strings.Count(got, "--- @")
	if count > maxMentionFiles {
		t.Fatalf("expected at most %d files, got %d", maxMentionFiles, count)
	}
}

func itoaQuick(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
