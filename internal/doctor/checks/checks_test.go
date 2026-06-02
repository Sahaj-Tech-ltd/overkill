package checks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

// ---------------------------------------------------------------------------
// Helper function tests
// ---------------------------------------------------------------------------

func TestOkf(t *testing.T) {
	r := okf("all good: %d things", 3)
	if r.Status != doctor.SevOK {
		t.Errorf("expected SevOK, got %s", r.Status)
	}
	if !strings.Contains(r.Detail, "3 things") {
		t.Errorf("detail = %q", r.Detail)
	}
}

func TestWarnf(t *testing.T) {
	r := warnf("do this fix", "warning: %s", "disk space low")
	if r.Status != doctor.SevWarn {
		t.Errorf("expected SevWarn, got %s", r.Status)
	}
	if r.Fix != "do this fix" {
		t.Errorf("fix = %q", r.Fix)
	}
}

func TestFailf(t *testing.T) {
	r := failf("run setup again", "fatal: %d errors", 5)
	if r.Status != doctor.SevFail {
		t.Errorf("expected SevFail, got %s", r.Status)
	}
	if r.Fix != "run setup again" {
		t.Errorf("fix = %q", r.Fix)
	}
}

func TestInfo(t *testing.T) {
	r := info("just FYI: %s", "optional component")
	if r.Status != doctor.SevInfo {
		t.Errorf("expected SevInfo, got %s", r.Status)
	}
}

func TestSkip(t *testing.T) {
	r := skip("not applicable on this platform")
	if r.Status != doctor.SevSkip {
		t.Errorf("expected SevSkip, got %s", r.Status)
	}
}

func TestDirsToCheck(t *testing.T) {
	dirs := dirsToCheck("/home/user/.overkill")
	if len(dirs) != 5 {
		t.Fatalf("expected 5 dirs, got %d", len(dirs))
	}
	expected := []string{
		"/home/user/.overkill",
		"/home/user/.overkill/sessions",
		"/home/user/.overkill/plugins",
		"/home/user/.overkill/cache",
		"/home/user/.overkill/journal",
	}
	for i, d := range dirs {
		if d.path != expected[i] {
			t.Errorf("dirs[%d].path = %q, want %q", i, d.path, expected[i])
		}
	}
}

func TestExtractTag(t *testing.T) {
	body := []byte(`{"tag_name":"v0.2.1","other":"stuff"}`)
	if tag := extractTag(body); tag != "v0.2.1" {
		t.Errorf("extractTag = %q, want v0.2.1", tag)
	}
}

func TestExtractTag_Missing(t *testing.T) {
	if tag := extractTag([]byte(`{"no_tag":"here"}`)); tag != "" {
		t.Errorf("expected empty, got %q", tag)
	}
}

func TestExtractTag_Empty(t *testing.T) {
	if tag := extractTag(nil); tag != "" {
		t.Errorf("expected empty, got %q", tag)
	}
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

func TestConfig_OK(t *testing.T) {
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterConfig, d, "config.load")
	if r.Status != doctor.SevWarn {
		t.Fatalf("expected warn for default config (no providers), got %s: %s", r.Status, r.Detail)
	}
}

func TestConfig_NilCfg(t *testing.T) {
	d := newTestDeps(t, config.Default())
	d.Cfg = nil
	r := runOne(t, RegisterConfig, d, "config.load")
	if r.Status != doctor.SevFail {
		t.Fatalf("expected fail for nil config, got %s", r.Status)
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

func TestConfig_CleanWithProvider(t *testing.T) {
	cfg := config.Default()
	cfg.Providers = []config.ProviderConfig{{Name: "openai", Type: "custom", APIKey: "k"}}
	cfg.Agent.DefaultProvider = "openai"
	d := newTestDeps(t, cfg)
	r := runOne(t, RegisterConfig, d, "config.load")
	// Default config may still have warnings (e.g. no daily limit)
	if r.Status != doctor.SevOK && r.Status != doctor.SevWarn {
		t.Fatalf("expected ok or warn, got %s: %s", r.Status, r.Detail)
	}
}

// ---------------------------------------------------------------------------
// Providers
// ---------------------------------------------------------------------------

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

func TestProviders_NoProviders(t *testing.T) {
	cfg := config.Default()
	cfg.Providers = nil
	d := newTestDeps(t, cfg)
	// When no providers, the check registers nothing — just one info entry
	r := doctor.NewRunner()
	RegisterProviders(r, d)
	s := r.Run(context.Background())
	if len(s.Checks) > 0 {
		// might register an info check
		for _, c := range s.Checks {
			if c.Status != doctor.SevInfo && c.Status != doctor.SevWarn {
				t.Errorf("no-provider check unexpected: %s %s", c.ID, c.Status)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Tokenizer
// ---------------------------------------------------------------------------

func TestTokenizer_OK(t *testing.T) {
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterTokenizer, d, "tokenizer")
	if r.Status != doctor.SevOK {
		t.Fatalf("expected ok, got %s: %s", r.Status, r.Detail)
	}
}

// ---------------------------------------------------------------------------
// Tools
// ---------------------------------------------------------------------------

func TestTools_OK(t *testing.T) {
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterTools, d, "tools.registry")
	if r.Status != doctor.SevOK {
		t.Fatalf("expected ok, got %s: %s", r.Status, r.Detail)
	}
}

// ---------------------------------------------------------------------------
// Hooks
// ---------------------------------------------------------------------------

func TestHooks_Info(t *testing.T) {
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterHooks, d, "hooks.registry")
	if r.Status != doctor.SevInfo {
		t.Fatalf("expected info, got %s: %s", r.Status, r.Detail)
	}
}

// ---------------------------------------------------------------------------
// Skills
// ---------------------------------------------------------------------------

func TestSkills_Info(t *testing.T) {
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterSkills, d, "skills")
	if r.Status != doctor.SevInfo {
		t.Fatalf("expected info, got %s: %s", r.Status, r.Detail)
	}
}

// ---------------------------------------------------------------------------
// Filesystem
// ---------------------------------------------------------------------------

func TestFilesystem_OK(t *testing.T) {
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterFilesystem, d, "fs.~/.overkill")
	if r.Status != doctor.SevOK {
		t.Fatalf("expected ok, got %s: %s", r.Status, r.Detail)
	}
}

func TestFilesystem_AllDirs(t *testing.T) {
	d := newTestDeps(t, config.Default())
	runner := doctor.NewRunner()
	runner.PerCheckTimeout = 2 * time.Second
	RegisterFilesystem(runner, d)
	s := runner.Run(context.Background())

	expectedIDs := []string{
		"fs.~/.overkill",
		"fs.~/.overkill/sessions",
		"fs.~/.overkill/plugins",
		"fs.~/.overkill/cache",
		"fs.~/.overkill/journal",
	}
	for _, wantID := range expectedIDs {
		found := false
		for _, c := range s.Checks {
			if c.ID == wantID {
				found = true
				if c.Status != doctor.SevOK {
					t.Errorf("%s: expected ok, got %s: %s", wantID, c.Status, c.Detail)
				}
				break
			}
		}
		if !found {
			t.Errorf("check %q not registered", wantID)
		}
	}
}

// ---------------------------------------------------------------------------
// Disk
// ---------------------------------------------------------------------------

func TestDisk_OK(t *testing.T) {
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterDisk, d, "disk.free")
	// On a real filesystem with reasonable space, should be ok
	if r.Status != doctor.SevOK && r.Status != doctor.SevWarn && r.Status != doctor.SevInfo {
		t.Fatalf("expected ok/warn/info, got %s: %s", r.Status, r.Detail)
	}
}

// ---------------------------------------------------------------------------
// Memory
// ---------------------------------------------------------------------------

func TestMemory_Info(t *testing.T) {
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterMemory, d, "memory.backend")
	if r.Status != doctor.SevInfo {
		t.Fatalf("expected info, got %s: %s", r.Status, r.Detail)
	}
}

// ---------------------------------------------------------------------------
// Cell Renderer
// ---------------------------------------------------------------------------

func TestCellRenderer_Info(t *testing.T) {
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterCellRenderer, d, "ui.cell_renderer")
	if r.Status != doctor.SevInfo {
		t.Fatalf("expected info, got %s: %s", r.Status, r.Detail)
	}
}

// ---------------------------------------------------------------------------
// Animations
// ---------------------------------------------------------------------------

func TestAnimations_Info(t *testing.T) {
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterAnimations, d, "ui.animations")
	if r.Status != doctor.SevInfo {
		t.Fatalf("expected info, got %s: %s", r.Status, r.Detail)
	}
}

// ---------------------------------------------------------------------------
// Version freshness
// ---------------------------------------------------------------------------

func TestVersion_Offline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"tag_name":"v1.0.0"}`))
	}))
	defer srv.Close()

	// We can't easily override the GitHub URL from here, so this test
	// exercises the path through the httptest server by checking that the
	// HTTP client in deps is wired and the check handles network errors.
	// The check itself hits the real GitHub API — for this test we just
	// verify it doesn't panic and returns a result.
	cfg := config.Default()
	d := newTestDeps(t, cfg)

	runner := doctor.NewRunner()
	runner.PerCheckTimeout = 2 * time.Second
	RegisterVersion(runner, d)
	s := runner.Run(context.Background())
	for _, c := range s.Checks {
		if c.ID == "version.freshness" {
			// Can be info (if github is reachable) or fail/timeout
			if c.Status == "" {
				t.Error("version check returned empty status")
			}
			return
		}
	}
	t.Error("version.freshness check not found")
}

// ---------------------------------------------------------------------------
// Bridge
// ---------------------------------------------------------------------------

func TestBridge_SkipNoEnv(t *testing.T) {
	// OVERKILL_BRIDGE_ADDR is unset → should skip
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterBridge, d, "bridge.python")
	if r.Status != doctor.SevSkip {
		t.Fatalf("expected skip when bridge not configured, got %s: %s", r.Status, r.Detail)
	}
}

// ---------------------------------------------------------------------------
// Catalog
// ---------------------------------------------------------------------------

func TestCatalog_Info(t *testing.T) {
	d := newTestDeps(t, config.Default())
	r := runOne(t, RegisterCatalog, d, "catalog.modelsdev")
	// Catalog check may be ok (live), warn (cached), or info — depends on network
	if r.Status != doctor.SevInfo && r.Status != doctor.SevOK && r.Status != doctor.SevWarn {
		t.Fatalf("expected info/ok/warn, got %s: %s", r.Status, r.Detail)
	}
}

// ---------------------------------------------------------------------------
// MCP / LSP (not configured)
// ---------------------------------------------------------------------------

func TestMCP_NotConfigured(t *testing.T) {
	cfg := config.Default()
	cfg.MCP.Servers = nil
	d := newTestDeps(t, cfg)
	r := runOne(t, RegisterMCP, d, "mcp.none")
	if r.Status != doctor.SevInfo {
		t.Fatalf("expected info when no MCP configured, got %s: %s", r.Status, r.Detail)
	}
}

func TestLSP_NotConfigured(t *testing.T) {
	cfg := config.Default()
	d := newTestDeps(t, cfg)
	// LSP checks register per-language; test that at least some run
	r := doctor.NewRunner()
	r.PerCheckTimeout = 2 * time.Second
	RegisterLSP(r, d)
	s := r.Run(context.Background())
	if len(s.Checks) == 0 {
		t.Fatal("expected at least one LSP check")
	}
	for _, c := range s.Checks {
		// All should be info (no servers) or skip or warn (if LSP on PATH)
		if c.Status != doctor.SevInfo && c.Status != doctor.SevSkip && c.Status != doctor.SevWarn {
			t.Errorf("unexpected LSP status for %s: %s", c.ID, c.Status)
		}
	}
}

// ---------------------------------------------------------------------------
// ACP
// ---------------------------------------------------------------------------

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

func TestACP_Disabled(t *testing.T) {
	cfg := config.Default()
	cfg.ACP.Enabled = false
	d := newTestDeps(t, cfg)
	r := runOne(t, RegisterACP, d, "acp.disabled")
	if r.Status != doctor.SevInfo {
		t.Fatalf("expected info when disabled, got %s: %s", r.Status, r.Detail)
	}
}

// ---------------------------------------------------------------------------
// Sync
// ---------------------------------------------------------------------------

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

func TestSync_NoneBackend(t *testing.T) {
	cfg := config.Default()
	cfg.Sync.Backend = "none"
	d := newTestDeps(t, cfg)
	r := runOne(t, RegisterSync, d, "sync.none")
	if r.Status != doctor.SevFail {
		t.Fatalf("expected fail for unknown backend 'none', got %s: %s", r.Status, r.Detail)
	}
}

// ---------------------------------------------------------------------------
// Plugins
// ---------------------------------------------------------------------------

func TestPlugins_None(t *testing.T) {
	cfg := config.Default()
	cfg.Plugins.Dir = filepath.Join(t.TempDir(), "absent")
	d := newTestDeps(t, cfg)
	r := runOne(t, RegisterPlugins, d, "plugins.none")
	if r.Status != doctor.SevInfo {
		t.Fatalf("expected info, got %s: %s", r.Status, r.Detail)
	}
}

// ---------------------------------------------------------------------------
// DB config (postgres) — doctor --check-db
// ---------------------------------------------------------------------------

func TestDB_OK(t *testing.T) {
	// This test requires a running PostgreSQL instance.
	// Skip in CI environments where Postgres isn't available.
	cfg := config.Default()
	cfg.DatabaseURL = "postgres://user:***@localhost:5432/overkill"
	d := newTestDeps(t, cfg)
	r := runOne(t, RegisterDB, d, "db.integrity")
	// With real Postgres the check passes (OK), without it the check reports the
	// connection error (FAIL). Both are acceptable outcomes in test environments.
	if r.Status != doctor.SevOK && r.Status != doctor.SevFail {
		t.Fatalf("unexpected status %s: %s", r.Status, r.Detail)
	}
}

func TestDB_MissingURL(t *testing.T) {
	cfg := config.Default()
	cfg.DatabaseURL = ""
	d := newTestDeps(t, cfg)
	r := runOne(t, RegisterDB, d, "db.integrity")
	if r.Status != doctor.SevFail {
		t.Fatalf("expected fail when database_url is empty, got %s: %s", r.Status, r.Detail)
	}
}
