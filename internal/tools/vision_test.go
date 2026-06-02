package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
)

type fakeDescriber struct {
	desc      string
	gotMime   string
	gotPrompt string
	wasCalled bool
}

func (f *fakeDescriber) Describe(_ context.Context, imgs []vision.Image, prompt string) (string, error) {
	f.wasCalled = true
	f.gotPrompt = prompt
	if len(imgs) > 0 {
		f.gotMime = imgs[0].Mime
	}
	return f.desc, nil
}

func TestVisionDescribe_FileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	// Minimal valid PNG header so MIMEFromBytes returns image/png.
	if err := os.WriteFile(path, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	d := &fakeDescriber{desc: "a tiny png"}
	tool := NewVisionDescribeTool(d, nil, BrowserHostPolicy{})

	in, _ := json.Marshal(map[string]any{"file": path, "prompt": "what is this?"})
	raw, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out["description"] != "a tiny png" {
		t.Fatalf("description = %v", out["description"])
	}
	if !strings.HasPrefix(out["source"].(string), "file:") {
		t.Fatalf("source = %v", out["source"])
	}
	if d.gotMime != "image/png" {
		t.Fatalf("mime sniff = %s", d.gotMime)
	}
	if d.gotPrompt != "what is this?" {
		t.Fatalf("prompt forwarded = %q", d.gotPrompt)
	}
}

func TestVisionDescribe_NoDescriberConfigured(t *testing.T) {
	tool := NewVisionDescribeTool(nil, nil, BrowserHostPolicy{})
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "no vision describer") {
		t.Fatalf("expected friendly error, got %s", raw)
	}
}

func TestVisionDescribe_RejectsURLWithoutBrowser(t *testing.T) {
	tool := NewVisionDescribeTool(&fakeDescriber{desc: "x"}, nil, BrowserHostPolicy{})
	in, _ := json.Marshal(map[string]any{"url": "https://example.com"})
	raw, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "url mode requires the browser") {
		t.Fatalf("expected browser-required error, got %s", raw)
	}
}
