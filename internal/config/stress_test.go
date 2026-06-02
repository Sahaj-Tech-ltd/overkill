package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ==========================================================================
// Adversarial stress tests: Config loading with malformed inputs,
// edge cases, and concurrent access.
// ==========================================================================

// C-STRESS-1: Completely empty TOML file
func TestStress_EmptyTOML(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")
	requireNoErr(t, os.WriteFile(path, []byte(""), 0o600))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(empty toml): %v", err)
	}
	if cfg == nil {
		t.Fatal("Load(empty toml) returned nil config")
	}
	// Should have defaults filled in.
	if cfg.Version == 0 {
		t.Error("empty TOML should produce default version")
	}
}

// C-STRESS-2: NUL bytes in TOML
func TestStress_NULBytesInTOML(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")
	data := []byte("version = 1\n[agent]\nname = \"Bot\x00Evil\"\n")
	requireNoErr(t, os.WriteFile(path, data, 0o600))

	cfg, err := Load(path)
	// Either returns defaults gracefully or an error
	if err != nil {
		t.Logf("Load(NUL bytes) returned error (expected): %v", err)
		return
	}
	if cfg == nil {
		t.Fatal("Load(NUL bytes) returned nil config")
	}
	t.Logf("Load(NUL bytes): agent name=%q", cfg.Agent.Name)
}

// C-STRESS-3: Extremely long key names (> 10KB)
func TestStress_LongKeyNames(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")
	longName := strings.Repeat("x", 10000)
	data := []byte("version = 1\n[" + longName + "]\nkey = \"val\"\n")
	requireNoErr(t, os.WriteFile(path, data, 0o600))

	cfg, err := Load(path)
	if err != nil {
		t.Logf("Load(long key) returned error: %v", err)
		return
	}
	requireNotNil(t, cfg)
	t.Logf("Load(long key) succeeded, version=%d", cfg.Version)
}

// C-STRESS-4: Extremely long string value (1MB)
func TestStress_HugeStringValue(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")
	hugeVal := strings.Repeat("A", 1_000_000)
	data := []byte("version = 1\n[agent]\nname = \"" + hugeVal + "\"\n")
	requireNoErr(t, os.WriteFile(path, data, 0o600))

	cfg, err := Load(path)
	if err != nil {
		t.Logf("Load(huge string) returned error: %v", err)
		return
	}
	requireNotNil(t, cfg)
	t.Logf("Load(huge string) agent name len=%d", len(cfg.Agent.Name))
}

// C-STRESS-5: Emoji in provider name
func TestStress_EmojiProviderName(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")
	tomlContent := `version = 1

[[providers]]
name = "😀🎉🚀"
type = "openai"
api_key = "sk-🍕🍔🌮"
`
	requireNoErr(t, os.WriteFile(path, []byte(tomlContent), 0o600))

	cfg, err := Load(path)
	if err != nil {
		t.Logf("Load(emoji provider) returned error: %v", err)
		return
	}
	requireNotNil(t, cfg)
	if len(cfg.Providers) > 0 {
		t.Logf("Load(emoji provider): name=%q type=%q", cfg.Providers[0].Name, cfg.Providers[0].Type)
	}
}

// C-STRESS-6: Zero-length provider name
func TestStress_ZeroLengthProviderName(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")
	tomlContent := `version = 1

[[providers]]
name = ""
type = ""
api_key = ""
`
	requireNoErr(t, os.WriteFile(path, []byte(tomlContent), 0o600))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(empty provider): %v", err)
	}
	requireNotNil(t, cfg)
	if len(cfg.Providers) > 0 {
		t.Logf("Load(empty provider): name=%q type=%q (len=%d)", cfg.Providers[0].Name, cfg.Providers[0].Type, len(cfg.Providers))
	}
}

// C-STRESS-7: Deeply nested tables (100 levels)
func TestStress_DeeplyNestedTOML(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")
	var sb strings.Builder
	sb.WriteString("version = 1\n")
	// Build a deeply nested [a.b.c.d...] key
	key := "a"
	for i := 0; i < 100; i++ {
		key += ".x"
	}
	sb.WriteString("[" + key + "]\nval = 1\n")
	requireNoErr(t, os.WriteFile(path, []byte(sb.String()), 0o600))

	cfg, err := Load(path)
	if err != nil {
		t.Logf("Load(deeply nested) returned error (expected possibly): %v", err)
		return
	}
	requireNotNil(t, cfg)
}

// C-STRESS-8: Binary garbage as config
func TestStress_BinaryGarbage(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")
	garbage := make([]byte, 10000)
	for i := range garbage {
		garbage[i] = byte(i % 256)
	}
	requireNoErr(t, os.WriteFile(path, garbage, 0o600))

	cfg, err := Load(path)
	// Should either fail with error OR return defaults
	if err != nil {
		t.Logf("Load(binary garbage) returned error: %v", err)
		return
	}
	requireNotNil(t, cfg)
	t.Logf("Load(binary garbage) returned defaults, version=%d", cfg.Version)
}

// C-STRESS-9: Concurrent Load+Save on same file
func TestStress_ConcurrentLoadSave(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")

	cfg := Default()
	cfg.Agent.Name = "BaseConfig"
	requireNoErr(t, cfg.Save(path))

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// Load
			loaded, err := Load(path)
			if err != nil {
				errs <- err
				return
			}
			// Mutate and save
			loaded.Agent.Name = "Concurrent_" + string(rune('A'+n))
			if err := loaded.Save(path); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent load/save error: %v", err)
	}

	// Final load should succeed
	final, err := Load(path)
	if err != nil {
		t.Fatalf("final Load: %v", err)
	}
	t.Logf("Final config agent name: %q", final.Agent.Name)
}

// C-STRESS-10: Save to path with no write permissions
func TestStress_SaveNoPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	noPermDir := filepath.Join(tmpDir, "noperm")
	requireNoErr(t, os.Mkdir(noPermDir, 0o500)) // r-x only
	defer os.Chmod(noPermDir, 0o700)

	path := filepath.Join(noPermDir, "config.toml")
	cfg := Default()
	err := cfg.Save(path)
	if err == nil {
		t.Error("Save(no-permissions) should have failed")
	} else {
		t.Logf("Save(no-permissions) correctly failed: %v", err)
	}
}

// C-STRESS-11: Extremely large number of providers (1000+)
func TestStress_ManyProviders(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")
	var sb strings.Builder
	sb.WriteString("version = 1\n")
	for i := 0; i < 1000; i++ {
		sb.WriteString("[[providers]]\n")
		sb.WriteString("name = \"provider_")
		sb.WriteString(string(rune('A' + (i % 26))))
		sb.WriteString("\"\ntype = \"custom\"\napi_key = \"key\"\nbase_url = \"https://api.example.com\"\n\n")
	}
	requireNoErr(t, os.WriteFile(path, []byte(sb.String()), 0o600))

	cfg, err := Load(path)
	if err != nil {
		t.Logf("Load(1000 providers) returned error: %v", err)
		return
	}
	requireNotNil(t, cfg)
	t.Logf("Load(1000 providers): loaded %d providers", len(cfg.Providers))
}

// C-STRESS-12: Validate with nil providers slice
func TestStress_ValidateNilProviders(t *testing.T) {
	cfg := &Config{
		Version:   CurrentVersion,
		Providers: nil,
	}
	errs := cfg.Validate()
	// Should handle nil gracefully
	t.Logf("Validate(nil providers): %d errors", len(errs))
	for _, e := range errs {
		t.Logf("  - %v", e)
	}
}

// C-STRESS-13: ResolveSecrets with empty provider list
func TestStress_ResolveSecretsEmpty(t *testing.T) {
	cfg := Default()
	cfg.Providers = []ProviderConfig{}
	err := cfg.ResolveSecrets()
	if err != nil {
		t.Errorf("ResolveSecrets(empty): %v", err)
	}
}

// C-STRESS-14: ConfigDir with HOME unset
func TestStress_ConfigDirNoHome(t *testing.T) {
	// Only test OVERKILL_HOME path since UserHomeDir depends on env
	t.Setenv("OVERKILL_HOME", t.TempDir())
	dir, err := ConfigDir()
	if err != nil {
		t.Errorf("ConfigDir with OVERKILL_HOME set: %v", err)
	}
	if dir == "" {
		t.Error("ConfigDir returned empty string")
	}
	t.Logf("ConfigDir: %s", dir)
}

// C-STRESS-15: MaskSecrets on nil config
func TestStress_MaskSecretsNil(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: MaskSecrets on nil config panicked: %v", r)
		}
	}()
	var cfg *Config
	masked := cfg.MaskSecrets()
	_ = masked
}

// Helpers
func requireNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireNotNil(t *testing.T, v interface{}) {
	t.Helper()
	if v == nil {
		t.Fatalf("expected non-nil value")
	}
}
