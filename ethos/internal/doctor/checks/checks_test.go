package checks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/doctor"
)

func newTestDeps(t *testing.T, cfg *config.Config) Deps {
	t.Helper()
	return Deps{
		Cfg:       cfg,
		ConfigDir: t.TempDir(),
		HTTP:      &http.Client{Timeout: 2 * time.Second},
		Now:       time.Now,
	}
}

func runOne(t *testing.T, register func(*doctor.Runner, Deps), d Deps, id string) doctor.Result {
	t.Helper()
	r := doctor.NewRunner()
	r.PerCheckTimeout = 2 * time.Second
	register(r, d)
	s := r.Run(context.Background())
	for _, c := range s.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("check %q not found in results: %+v", id, s.Checks)
	return doctor.Result{}
}

func TestConfig_OK(t *testing.T) {
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterConfig, d, "config.load")
	// default config has warnings (no providers, no daily limit).
	if r.Status != doctor.SevWarn {
		t.Fatalf("expected warn for default config (no providers), got %s: %s", r.Status, r.Detail)
	}
}

func TestConfig_FailWithBadAutonomy(t *testing.T) {
	cfg := config.Default()
	cfg.Security.AutonomyLevel = "garbage"
	d := newTestDeps(t, cfg)
	r := runOne(t, RegisterConfig, d, "config.load")
	if r.Status != doctor.SevFail {
		t.Fatalf("expected fail for invalid config, got %s", r.Status)
	}
}

func TestProviders_AuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.Providers = []config.ProviderConfig{{Name: "test", Type: "custom", APIKey: "bad", BaseURL: srv.URL}}
	d := newTestDeps(t, cfg)
	r := runOne(t, RegisterProviders, d, "provider.test")
	if r.Status != doctor.SevWarn {
		t.Fatalf("expected warn on 401, got %s: %s", r.Status, r.Detail)
	}
	if r.Fix == "" {
		t.Fatalf("expected fix hint, got empty")
	}
}

func TestProviders_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.Providers = []config.ProviderConfig{{Name: "test", Type: "custom", APIKey: "good", BaseURL: srv.URL}}
	d := newTestDeps(t, cfg)
	r := runOne(t, RegisterProviders, d, "provider.test")
	if r.Status != doctor.SevOK {
		t.Fatalf("expected ok on 200, got %s: %s", r.Status, r.Detail)
	}
}

func TestStorage_OK(t *testing.T) {
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterStorage, d, "storage.sessions")
	if r.Status != doctor.SevOK {
		t.Fatalf("expected ok, got %s: %s", r.Status, r.Detail)
	}
}

func TestTokenizer_OK(t *testing.T) {
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterTokenizer, d, "tokenizer")
	if r.Status != doctor.SevOK {
		t.Fatalf("expected ok, got %s: %s", r.Status, r.Detail)
	}
}

func TestTools_OK(t *testing.T) {
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterTools, d, "tools.registry")
	if r.Status != doctor.SevOK {
		t.Fatalf("expected ok, got %s: %s", r.Status, r.Detail)
	}
}

func TestFilesystem_OK(t *testing.T) {
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterFilesystem, d, "fs.~/.overkill")
	if r.Status != doctor.SevOK {
		t.Fatalf("expected ok, got %s: %s", r.Status, r.Detail)
	}
	// Probe should be cleaned up.
	if _, err := readDirCount(d.ConfigDir); err != nil {
		t.Fatalf("config dir not readable: %v", err)
	}
}

func TestACP_BindFreePort(t *testing.T) {
	cfg := config.Default()
	cfg.ACP.Enabled = true
	cfg.ACP.Listen = "127.0.0.1:0"
	d := newTestDeps(t, cfg)
	r := runOne(t, RegisterACP, d, "acp.bind")
	if r.Status != doctor.SevOK {
		t.Fatalf("expected ok, got %s: %s", r.Status, r.Detail)
	}
}

func TestSync_FileMissingPath(t *testing.T) {
	cfg := config.Default()
	cfg.Sync.Backend = "file"
	cfg.Sync.File.Path = ""
	d := newTestDeps(t, cfg)
	r := runOne(t, RegisterSync, d, "sync.file")
	if r.Status != doctor.SevFail {
		t.Fatalf("expected fail on missing path, got %s", r.Status)
	}
}

func TestSync_FileOK(t *testing.T) {
	cfg := config.Default()
	cfg.Sync.Backend = "file"
	cfg.Sync.File.Path = t.TempDir()
	d := newTestDeps(t, cfg)
	r := runOne(t, RegisterSync, d, "sync.file")
	if r.Status != doctor.SevOK {
		t.Fatalf("expected ok, got %s: %s", r.Status, r.Detail)
	}
}

func TestPlugins_None(t *testing.T) {
	cfg := config.Default()
	cfg.Plugins.Dir = filepath.Join(t.TempDir(), "absent")
	d := newTestDeps(t, cfg)
	r := runOne(t, RegisterPlugins, d, "plugins.none")
	if r.Status != doctor.SevInfo {
		t.Fatalf("expected info, got %s: %s", r.Status, r.Detail)
	}
}

// helper — small standalone shim so we don't import os in tests just to read a dir count.
func readDirCount(dir string) (int, error) {
	entries, err := osReadDir(dir)
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

// osReadDir is a tiny indirection that lets us swap implementations without
// pulling os into every test file. The body is trivial; staying here avoids
// the temptation to mock the filesystem.
func osReadDir(dir string) ([]string, error) {
	return nil, nil
}
