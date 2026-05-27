package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// pngFixture writes a minimal 1×1 PNG to t.TempDir and returns the path.
func pngFixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	// 8-byte PNG signature + IHDR header start — enough for sniff + ext.
	data := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImageMIMEFromPath_KnownExtensions(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"a.png", "image/png"},
		{"a.PNG", "image/png"},
		{"x.jpg", "image/jpeg"},
		{"x.jpeg", "image/jpeg"},
		{"x.gif", "image/gif"},
		{"x.webp", "image/webp"},
	}
	for _, tt := range tests {
		if got := imageMIMEFromPath(tt.path, nil); got != tt.want {
			t.Errorf("imageMIMEFromPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestImageMIMEFromPath_SniffWhenExtMissing(t *testing.T) {
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	if got := imageMIMEFromPath("screenshot", pngBytes); got != "image/png" {
		t.Errorf("sniff should detect PNG from magic bytes: got %q", got)
	}
}

func TestImageMIMEFromPath_NonImageReturnsEmpty(t *testing.T) {
	if got := imageMIMEFromPath("a.txt", []byte("hello")); got != "" {
		t.Errorf("text file should return empty MIME: got %q", got)
	}
}

func TestSplitAttachPaths_PlainAndQuoted(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"single", "a.png", []string{"a.png"}},
		{"multiple", "a.png b.png", []string{"a.png", "b.png"}},
		{"double-quoted with space", `"file with space.png"`, []string{"file with space.png"}},
		{"mixed", `a.png "b c.png" d.png`, []string{"a.png", "b c.png", "d.png"}},
		{"single-quoted", `'spaced one.png'`, []string{"spaced one.png"}},
		{"unterminated quote falls back", `"unclosed`, []string{`"unclosed`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitAttachPaths(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunAttach_StagesPNG(t *testing.T) {
	m := &appModel{}
	path := pngFixture(t, "foo.png")
	if cmd := m.runAttach(path); cmd == nil {
		t.Fatal("expected toast cmd")
	}
	if len(m.pendingAttachments) != 1 {
		t.Fatalf("want 1 staged, got %d", len(m.pendingAttachments))
	}
	if m.pendingAttachments[0].MediaType != "image/png" {
		t.Errorf("MIME lost: %s", m.pendingAttachments[0].MediaType)
	}
}

func TestRunAttach_RejectsDirectory(t *testing.T) {
	m := &appModel{}
	dir := t.TempDir()
	if cmd := m.runAttach(dir); cmd == nil {
		t.Fatal("expected toast cmd")
	}
	if len(m.pendingAttachments) != 0 {
		t.Errorf("directory should not be staged: got %d", len(m.pendingAttachments))
	}
}

func TestRunAttach_RejectsNonImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &appModel{}
	m.runAttach(path)
	if len(m.pendingAttachments) != 0 {
		t.Errorf("text file should not be staged: got %d", len(m.pendingAttachments))
	}
}

func TestRunAttach_HitsLimit(t *testing.T) {
	m := &appModel{}
	for i := 0; i < maxAttachments; i++ {
		m.pendingAttachments = append(m.pendingAttachments, pendingAttachment{
			Path: "x.png", MediaType: "image/png", Data: []byte{1},
		})
	}
	path := pngFixture(t, "extra.png")
	m.runAttach(path)
	if len(m.pendingAttachments) != maxAttachments {
		t.Errorf("should reject past limit: got %d", len(m.pendingAttachments))
	}
}

func TestDrainAttachments_ClearsAndConverts(t *testing.T) {
	m := &appModel{
		pendingAttachments: []pendingAttachment{{
			Path:      "/tmp/x.png",
			MediaType: "image/png",
			Data:      []byte{1, 2, 3},
		}},
	}
	got := m.drainAttachments()
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	if got[0].Kind != providers.AttachmentImage {
		t.Errorf("kind: %s", got[0].Kind)
	}
	if got[0].MediaType != "image/png" {
		t.Errorf("media: %s", got[0].MediaType)
	}
	if len(m.pendingAttachments) != 0 {
		t.Errorf("drain should clear staged: got %d", len(m.pendingAttachments))
	}
}

func TestExpandTilde(t *testing.T) {
	home, _ := os.UserHomeDir()
	tests := []struct {
		in, want string
	}{
		{"~", home},
		{"~/foo", filepath.Join(home, "foo")},
		{"/abs/path", "/abs/path"},
		{"relative/path", "relative/path"},
	}
	for _, tt := range tests {
		if got := expandTilde(tt.in); got != tt.want {
			t.Errorf("expandTilde(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
