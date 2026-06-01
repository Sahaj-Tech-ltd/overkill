package providers

import (
	"os"
	"path/filepath"
	"testing"
)

func setupCatalogDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(dir, "openai"), 0o755)

	os.WriteFile(filepath.Join(dir, "openai", "gpt-4o.toml"), []byte(`
name = "GPT-4o"
family = "gpt-4o"
max_tokens = 128000
tool_call = true
structured_output = true
temperature = true
attachment = true

[modalities]
input = ["text", "image"]
output = ["text"]

[cost]
input = 2.50
output = 10.00
cache_read = 1.25
cache_write = 10.00
`), 0o644)

	os.WriteFile(filepath.Join(dir, "openai", "gpt-4o-mini.toml"), []byte(`
name = "GPT-4o Mini"
family = "gpt-4o"

[extends]
from = "openai/gpt-4o"

[cost]
input = 0.15
output = 0.60
cache_read = 0.075
cache_write = 0.60
`), 0o644)

	os.WriteFile(filepath.Join(dir, "openai", "o3-mini.toml"), []byte(`
name = "o3 Mini"
family = "o3"
max_tokens = 128000
reasoning = true
tool_call = true
temperature = false

[modalities]
input = ["text"]
output = ["text"]

[cost]
input = 1.10
output = 4.40
cache_read = 0.55
cache_write = 4.40
`), 0o644)

	os.MkdirAll(filepath.Join(dir, "anthropic"), 0o755)

	os.WriteFile(filepath.Join(dir, "anthropic", "claude-sonnet-4.toml"), []byte(`
name = "Claude Sonnet 4"
family = "claude"
max_tokens = 200000
tool_call = true
structured_output = true
temperature = true
attachment = true

[modalities]
input = ["text", "image"]
output = ["text"]

[cost]
input = 3.00
output = 15.00
cache_read = 3.00
cache_write = 15.00
`), 0o644)

	return dir
}

func TestLoadCatalog_ParsesTOMLDir(t *testing.T) {
	dir := setupCatalogDir(t)
	mc, err := LoadCatalog(dir)
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}
	ids := mc.List()
	if len(ids) != 4 {
		t.Fatalf("expected 4 models, got %d: %v", len(ids), ids)
	}
}

func TestLoadCatalog_NonexistentDir(t *testing.T) {
	mc, err := LoadCatalog("/nonexistent/path/that/does/not/exist")
	if err != nil {
		t.Fatalf("expected no error for nonexistent dir, got: %v", err)
	}
	ids := mc.List()
	if len(ids) != 0 {
		t.Fatalf("expected 0 models for nonexistent dir, got %d", len(ids))
	}
}

func TestGet_ResolvesSimpleModel(t *testing.T) {
	dir := setupCatalogDir(t)
	mc, err := LoadCatalog(dir)
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	m, err := mc.Get("openai/gpt-4o")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if m.ID != "openai/gpt-4o" {
		t.Errorf("ID = %q, want %q", m.ID, "openai/gpt-4o")
	}
	if m.Name != "GPT-4o" {
		t.Errorf("Name = %q, want %q", m.Name, "GPT-4o")
	}
	if m.MaxTokens != 128000 {
		t.Errorf("MaxTokens = %d, want %d", m.MaxTokens, 128000)
	}
	if !m.SupportsTools {
		t.Error("SupportsTools = false, want true")
	}
	if !m.SupportsVision {
		t.Error("SupportsVision = false, want true")
	}
	if m.CostIn != 2.50 {
		t.Errorf("CostIn = %f, want %f", m.CostIn, 2.50)
	}
	if m.CostOut != 10.00 {
		t.Errorf("CostOut = %f, want %f", m.CostOut, 10.00)
	}
	if m.StructuredOutput != true {
		t.Error("StructuredOutput = false, want true")
	}
	if m.Temperature != true {
		t.Error("Temperature = false, want true")
	}
}

func TestGet_ResolvesExtendsChain(t *testing.T) {
	dir := setupCatalogDir(t)
	mc, err := LoadCatalog(dir)
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	m, err := mc.Get("openai/gpt-4o-mini")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if m.ID != "openai/gpt-4o-mini" {
		t.Errorf("ID = %q, want %q", m.ID, "openai/gpt-4o-mini")
	}
	if m.Name != "GPT-4o Mini" {
		t.Errorf("Name = %q, want %q", m.Name, "GPT-4o Mini")
	}
	if m.MaxTokens != 128000 {
		t.Errorf("MaxTokens = %d, want %d (inherited from parent)", m.MaxTokens, 128000)
	}
	if m.CostIn != 0.15 {
		t.Errorf("CostIn = %f, want %f (own value)", m.CostIn, 0.15)
	}
	if m.CostOut != 0.60 {
		t.Errorf("CostOut = %f, want %f (own value)", m.CostOut, 0.60)
	}
	if !m.SupportsTools {
		t.Error("SupportsTools = false, want true (inherited)")
	}
	if !m.SupportsVision {
		t.Error("SupportsVision = false, want true (inherited)")
	}
	if m.StructuredOutput != true {
		t.Error("StructuredOutput = false, want true (inherited)")
	}
	if m.Family != "gpt-4o" {
		t.Errorf("Family = %q, want %q", m.Family, "gpt-4o")
	}
}

func TestGet_NotFound(t *testing.T) {
	dir := setupCatalogDir(t)
	mc, err := LoadCatalog(dir)
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	_, err = mc.Get("nonexistent/model")
	if err == nil {
		t.Fatal("expected error for unknown model, got nil")
	}
}

func TestList_ReturnsSorted(t *testing.T) {
	dir := setupCatalogDir(t)
	mc, err := LoadCatalog(dir)
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	ids := mc.List()
	for i := 1; i < len(ids); i++ {
		if ids[i] < ids[i-1] {
			t.Errorf("List not sorted: %q before %q", ids[i-1], ids[i])
		}
	}
	expected := []string{"anthropic/claude-sonnet-4", "openai/gpt-4o", "openai/gpt-4o-mini", "openai/o3-mini"}
	if len(ids) != len(expected) {
		t.Fatalf("expected %d IDs, got %d", len(expected), len(ids))
	}
	for i, id := range ids {
		if id != expected[i] {
			t.Errorf("ids[%d] = %q, want %q", i, id, expected[i])
		}
	}
}

func TestByFamily_ReturnsMatchingModels(t *testing.T) {
	dir := setupCatalogDir(t)
	mc, err := LoadCatalog(dir)
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	models := mc.ByFamily("gpt-4o")
	if len(models) != 2 {
		t.Fatalf("expected 2 models in gpt-4o family, got %d", len(models))
	}

	found := make(map[string]bool)
	for _, m := range models {
		found[m.ID] = true
		if m.Family != "gpt-4o" {
			t.Errorf("Family = %q, want %q for %s", m.Family, "gpt-4o", m.ID)
		}
	}
	if !found["openai/gpt-4o"] {
		t.Error("missing openai/gpt-4o in family results")
	}
	if !found["openai/gpt-4o-mini"] {
		t.Error("missing openai/gpt-4o-mini in family results")
	}
}

func TestByCapability_FiltersToolCall(t *testing.T) {
	dir := setupCatalogDir(t)
	mc, err := LoadCatalog(dir)
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	models := mc.ByCapability(true, false)
	if len(models) != 4 {
		t.Fatalf("expected 4 models with tool_call, got %d", len(models))
	}
	for _, m := range models {
		if !m.SupportsTools {
			t.Errorf("model %s has SupportsTools=false but was returned by ByCapability(true,false)", m.ID)
		}
	}
}

func TestByCapability_FiltersVision(t *testing.T) {
	dir := setupCatalogDir(t)
	mc, err := LoadCatalog(dir)
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	models := mc.ByCapability(false, true)
	if len(models) != 3 {
		t.Fatalf("expected 3 models with vision, got %d", len(models))
	}
	for _, m := range models {
		if !m.SupportsVision {
			t.Errorf("model %s has SupportsVision=false but was returned by ByCapability(false,true)", m.ID)
		}
	}
}

func TestLoadCatalog_WithLimitAndNewFields(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "openai"), 0o755)

	os.WriteFile(filepath.Join(dir, "openai", "gpt-5.toml"), []byte(`
name = "GPT-5"
family = "gpt"
release_date = "2025-08-07"
last_updated = "2025-08-07"
knowledge = "2024-09-30"
status = "stable"
tool_call = true
reasoning = true
temperature = false
attachment = true
structured_output = true
open_weights = false

[cost]
input = 1.25
output = 10.00
cache_read = 0.125

[limit]
context = 400_000
input = 272_000
output = 128_000

[modalities]
input = ["text", "image"]
output = ["text"]
`), 0o644)

	mc, err := LoadCatalog(dir)
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	m, err := mc.Get("openai/gpt-5")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}

	if m.ContextWindow != 400_000 {
		t.Errorf("ContextWindow = %d, want 400_000", m.ContextWindow)
	}
	if m.DefaultMaxTokens != 128_000 {
		t.Errorf("DefaultMaxTokens = %d, want 128_000", m.DefaultMaxTokens)
	}
	if m.ReleaseDate != "2025-08-07" {
		t.Errorf("ReleaseDate = %q, want '2025-08-07'", m.ReleaseDate)
	}
	if m.LastUpdated != "2025-08-07" {
		t.Errorf("LastUpdated = %q, want '2025-08-07'", m.LastUpdated)
	}
	if m.Knowledge != "2024-09-30" {
		t.Errorf("Knowledge = %q, want '2024-09-30'", m.Knowledge)
	}
	if m.Status != "stable" {
		t.Errorf("Status = %q, want 'stable'", m.Status)
	}
	if m.SupportsTools != true {
		t.Error("SupportsTools = false, want true")
	}
	if m.CostIn != 1.25 {
		t.Errorf("CostIn = %f, want 1.25", m.CostIn)
	}
}

func TestLoadCatalog_WithInterleaved(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "deepseek"), 0o755)

	os.WriteFile(filepath.Join(dir, "deepseek", "v4-pro.toml"), []byte(`
name = "DeepSeek V4 Pro"
family = "deepseek"
release_date = "2026-04-24"
last_updated = "2026-04-24"
reasoning = true
temperature = true
tool_call = true
structured_output = true
attachment = false
open_weights = true

[interleaved]
field = "reasoning_content"

[cost]
input = 1.74
output = 3.48

[limit]
context = 1_000_000
output = 384_000

[modalities]
input = ["text"]
output = ["text"]
`), 0o644)

	mc, err := LoadCatalog(dir)
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	m, err := mc.Get("deepseek/v4-pro")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}

	if m.ContextWindow != 1_000_000 {
		t.Errorf("ContextWindow = %d, want 1_000_000", m.ContextWindow)
	}
	if m.DefaultMaxTokens != 384_000 {
		t.Errorf("DefaultMaxTokens = %d, want 384_000", m.DefaultMaxTokens)
	}
}

func TestLoadCatalog_LoadsModelsDir(t *testing.T) {
	dir := "../../models"
	info, err := os.Stat(dir)
	if err != nil {
		t.Skipf("models directory not found, skipping: %v", err)
	}
	if !info.IsDir() {
		t.Skip("models path is not a directory, skipping")
	}

	mc, err := LoadCatalog(dir)
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	ids := mc.List()
	if len(ids) == 0 {
		t.Fatal("no models loaded from models/ directory")
	}
	t.Logf("loaded %d models from models/ directory", len(ids))

	m, err := mc.Get("openai/gpt-5")
	if err != nil {
		t.Errorf("failed to get openai/gpt-5: %v", err)
		return
	}

	if m.Name == "" {
		t.Error("Model Name is empty")
	}
	if m.ContextWindow == 0 {
		t.Error("ContextWindow is 0")
	}
	if m.CostIn == 0 {
		t.Error("CostIn is 0")
	}
	t.Logf("gpt-5: Name=%s ContextWindow=%d OutputTokens=%d CostIn=%.2f CostOut=%.2f",
		m.Name, m.ContextWindow, m.DefaultMaxTokens, m.CostIn, m.CostOut)
}

func TestLoadCatalog_OpenRouterNestedModels(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "openrouter", "models", "openai"), 0o755)
	os.MkdirAll(filepath.Join(dir, "openrouter", "models", "anthropic"), 0o755)

	os.WriteFile(filepath.Join(dir, "openrouter", "models", "openai", "gpt-4o.toml"), []byte(`
name = "GPT-4o (OpenRouter)"
family = "gpt"
release_date = "2024-05-13"
last_updated = "2024-08-06"
reasoning = false
temperature = true
tool_call = true
attachment = true
open_weights = false

[cost]
input = 2.50
output = 10.00

[limit]
context = 128_000
output = 16_384

[modalities]
input = ["text", "image"]
output = ["text"]
`), 0o644)

	os.WriteFile(filepath.Join(dir, "openrouter", "models", "anthropic", "claude-sonnet-4.toml"), []byte(`
name = "Claude Sonnet 4 (OpenRouter)"
family = "claude"
release_date = "2025-06-01"
last_updated = "2025-06-01"
reasoning = true
temperature = true
tool_call = true
attachment = true
open_weights = false

[cost]
input = 3.00
output = 15.00

[limit]
context = 200_000
output = 64_000

[modalities]
input = ["text", "image"]
output = ["text"]
`), 0o644)

	mc, err := LoadCatalog(dir)
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	m, err := mc.Get("openrouter/openai/gpt-4o")
	if err != nil {
		t.Fatalf("Get openrouter/openai/gpt-4o: %v", err)
	}
	if m.Name != "GPT-4o (OpenRouter)" {
		t.Errorf("Name = %q, want 'GPT-4o (OpenRouter)'", m.Name)
	}

	m2, err := mc.Get("openrouter/anthropic/claude-sonnet-4")
	if err != nil {
		t.Fatalf("Get openrouter/anthropic/claude-sonnet-4: %v", err)
	}
	if m2.ContextWindow != 200_000 {
		t.Errorf("ContextWindow = %d, want 200_000", m2.ContextWindow)
	}
}

func TestGet_CircularExtends(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "loop"), 0o755)

	os.WriteFile(filepath.Join(dir, "loop", "a.toml"), []byte(`
name = "Model A"

[extends]
from = "loop/b"
`), 0o644)

	os.WriteFile(filepath.Join(dir, "loop", "b.toml"), []byte(`
name = "Model B"

[extends]
from = "loop/a"
`), 0o644)

	mc, err := LoadCatalog(dir)
	if err != nil {
		t.Fatalf("LoadCatalog returned error: %v", err)
	}

	_, err = mc.Get("loop/a")
	if err == nil {
		t.Fatal("expected error for circular extends, got nil")
	}
}
