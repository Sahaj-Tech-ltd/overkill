package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakePayload = `{
  "openai": {
    "id": "openai", "name": "OpenAI", "env": ["OPENAI_API_KEY"],
    "models": {
      "gpt-4o": {
        "id": "gpt-4o", "name": "GPT-4o",
        "tool_call": true, "attachment": true,
        "modalities": {"input": ["text","image"], "output":["text"]},
        "cost": {"input": 2.5, "output": 10.0, "cache_read": 1.25},
        "limit": {"context": 128000, "output": 16384}
      }
    }
  },
  "anthropic": {
    "id": "anthropic", "name": "Anthropic",
    "models": {
      "claude-sonnet-4-5": {
        "id": "claude-sonnet-4-5", "name": "Claude Sonnet 4.5",
        "tool_call": true, "reasoning": true,
        "modalities": {"input":["text","image"],"output":["text"]},
        "cost": {"input": 3.0, "output": 15.0},
        "limit": {"context": 200000, "output": 8192}
      }
    }
  }
}`

func withTempHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
}

func TestFetchCatalog_Live(t *testing.T) {
	withTempHome(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "overkill/") {
			t.Errorf("User-Agent missing/invalid: %q", got)
		}
		_, _ = w.Write([]byte(fakePayload))
	}))
	defer srv.Close()

	prev := ModelsDevURL
	ModelsDevURL = srv.URL
	defer func() { ModelsDevURL = prev }()

	cat, err := FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if cat.Source() != SourceLive {
		t.Errorf("source = %q want live", cat.Source())
	}

	provs := cat.Providers()
	if len(provs) != 2 {
		t.Fatalf("providers = %d want 2", len(provs))
	}

	models := cat.Models("openai")
	if len(models) != 1 || models[0].ID != "gpt-4o" {
		t.Fatalf("openai models = %+v", models)
	}
	if models[0].Cost.Input != 2.5 {
		t.Errorf("cost not parsed: %+v", models[0].Cost)
	}

	// Cache must have been written.
	path, _ := cachePath()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("cache not written: %v", err)
	}
}

func TestFetchCatalog_FallsBackToCache(t *testing.T) {
	withTempHome(t)

	// Pre-seed the cache.
	path, _ := cachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fakePayload), 0o600); err != nil {
		t.Fatal(err)
	}

	// Point at a server that returns 500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()
	prev := ModelsDevURL
	ModelsDevURL = srv.URL
	defer func() { ModelsDevURL = prev }()

	cat, err := FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if cat.Source() != SourceCache {
		t.Errorf("source = %q want cache", cat.Source())
	}
	if len(cat.Models("anthropic")) != 1 {
		t.Errorf("anthropic models missing in cache load")
	}
}

func TestFetchCatalog_FallsBackToBaked(t *testing.T) {
	withTempHome(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()
	prev := ModelsDevURL
	ModelsDevURL = srv.URL
	defer func() { ModelsDevURL = prev }()

	cat, err := FetchCatalog(context.Background())
	// We expect a warning error AND a usable catalog.
	if cat == nil {
		t.Fatalf("expected baked catalog, got nil (err=%v)", err)
	}
	if cat.Source() != SourceBaked {
		t.Errorf("source = %q want baked", cat.Source())
	}
	if len(cat.Providers()) == 0 {
		t.Error("baked catalog has no providers")
	}
}

func TestParseCatalog_BackfillsIDs(t *testing.T) {
	cat, err := parseCatalog([]byte(`{"x":{"models":{"foo":{}}}}`), SourceLive)
	if err != nil {
		t.Fatal(err)
	}
	provs := cat.Providers()
	if provs[0].ID != "x" || provs[0].Name != "x" {
		t.Errorf("provider id/name not backfilled: %+v", provs[0])
	}
	models := cat.Models("x")
	if len(models) != 1 || models[0].ID != "foo" || models[0].Name != "foo" {
		t.Errorf("model id/name not backfilled: %+v", models)
	}
}

func TestAll_FlattenedAndSorted(t *testing.T) {
	cat, _ := parseCatalog([]byte(fakePayload), SourceLive)
	all := cat.All()
	if len(all) != 2 {
		t.Fatalf("All() = %d, want 2", len(all))
	}
	// Provider sort: anthropic before openai
	if all[0].ProviderID != "anthropic" || all[1].ProviderID != "openai" {
		t.Errorf("not sorted by provider: %+v", all)
	}
}

func TestFormatHelpers(t *testing.T) {
	if got := FormatContext(128_000); got != "128K" {
		t.Errorf("FormatContext(128K) = %q", got)
	}
	if got := FormatContext(2_000_000); got != "2M" {
		t.Errorf("FormatContext(2M) = %q", got)
	}
	if got := FormatCost(0, 0); got != "free" {
		t.Errorf("FormatCost(0,0) = %q", got)
	}
	if got := FormatCost(2.5, 10); !strings.Contains(got, "$2.50") {
		t.Errorf("FormatCost = %q", got)
	}
}
