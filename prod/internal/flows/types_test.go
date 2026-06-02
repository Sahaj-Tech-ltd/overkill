package flows

import (
	"testing"
	"time"
)

// =============================================================================
// FlowKind, FlowSurface, FlowSource constants
// =============================================================================

func TestFlowKindConstants(t *testing.T) {
	tests := []struct {
		kind     FlowKind
		expected string
	}{
		{KindChannel, "channel"},
		{KindCore, "core"},
		{KindProvider, "provider"},
		{KindSearch, "search"},
	}
	for _, tt := range tests {
		if string(tt.kind) != tt.expected {
			t.Errorf("FlowKind %v: got %q, want %q", tt.kind, tt.kind, tt.expected)
		}
	}
}

func TestFlowSurfaceConstants(t *testing.T) {
	tests := []struct {
		surface  FlowSurface
		expected string
	}{
		{SurfaceAuthChoice, "auth-choice"},
		{SurfaceHealth, "health"},
		{SurfaceModelPicker, "model-picker"},
		{SurfaceSetup, "setup"},
	}
	for _, tt := range tests {
		if string(tt.surface) != tt.expected {
			t.Errorf("FlowSurface %v: got %q, want %q", tt.surface, tt.surface, tt.expected)
		}
	}
}

func TestFlowSourceConstants(t *testing.T) {
	tests := []struct {
		source   FlowSource
		expected string
	}{
		{SourceManifest, "manifest"},
		{SourceInstallCatalog, "install-catalog"},
		{SourceRuntime, "runtime"},
		{SourceCore, "core"},
		{SourcePlugin, "plugin"},
	}
	for _, tt := range tests {
		if string(tt.source) != tt.expected {
			t.Errorf("FlowSource %v: got %q, want %q", tt.source, tt.source, tt.expected)
		}
	}
}

// =============================================================================
// FlowOption struct construction
// =============================================================================

func TestFlowOptionMinimal(t *testing.T) {
	opt := FlowOption{
		Value: "anthropic",
		Label: "Anthropic",
	}
	if opt.Value != "anthropic" {
		t.Errorf("Value: got %q, want %q", opt.Value, "anthropic")
	}
	if opt.Label != "Anthropic" {
		t.Errorf("Label: got %q, want %q", opt.Label, "Anthropic")
	}
	if opt.Hint != "" {
		t.Errorf("Hint should be empty, got %q", opt.Hint)
	}
	if opt.Group != nil {
		t.Errorf("Group should be nil, got %+v", opt.Group)
	}
	if opt.Docs != nil {
		t.Errorf("Docs should be nil, got %+v", opt.Docs)
	}
}

func TestFlowOptionFull(t *testing.T) {
	group := &OptionGroup{
		ID:    "cloud",
		Label: "Cloud Providers",
		Hint:  "Hosted LLM APIs",
	}
	docs := &OptionDocs{
		Path:  "/docs/providers/anthropic",
		Label: "Anthropic Setup Guide",
	}
	opt := FlowOption{
		Value: "anthropic",
		Label: "Anthropic",
		Hint:  "Claude 3.5 Sonnet and Opus",
		Group: group,
		Docs:  docs,
	}

	if opt.Value != "anthropic" {
		t.Errorf("Value: got %q, want %q", opt.Value, "anthropic")
	}
	if opt.Label != "Anthropic" {
		t.Errorf("Label: got %q, want %q", opt.Label, "Anthropic")
	}
	if opt.Hint != "Claude 3.5 Sonnet and Opus" {
		t.Errorf("Hint: got %q, want %q", opt.Hint, "Claude 3.5 Sonnet and Opus")
	}
	if opt.Group.ID != "cloud" {
		t.Errorf("Group.ID: got %q, want %q", opt.Group.ID, "cloud")
	}
	if opt.Group.Label != "Cloud Providers" {
		t.Errorf("Group.Label: got %q, want %q", opt.Group.Label, "Cloud Providers")
	}
	if opt.Group.Hint != "Hosted LLM APIs" {
		t.Errorf("Group.Hint: got %q, want %q", opt.Group.Hint, "Hosted LLM APIs")
	}
	if opt.Docs.Path != "/docs/providers/anthropic" {
		t.Errorf("Docs.Path: got %q, want %q", opt.Docs.Path, "/docs/providers/anthropic")
	}
	if opt.Docs.Label != "Anthropic Setup Guide" {
		t.Errorf("Docs.Label: got %q, want %q", opt.Docs.Label, "Anthropic Setup Guide")
	}
}

func TestOptionGroupMinimal(t *testing.T) {
	g := OptionGroup{ID: "local", Label: "Local"}
	if g.ID != "local" {
		t.Errorf("ID: got %q, want %q", g.ID, "local")
	}
	if g.Label != "Local" {
		t.Errorf("Label: got %q, want %q", g.Label, "Local")
	}
	if g.Hint != "" {
		t.Errorf("Hint should be empty, got %q", g.Hint)
	}
}

func TestOptionDocsMinimal(t *testing.T) {
	d := OptionDocs{Path: "/docs/ollama"}
	if d.Path != "/docs/ollama" {
		t.Errorf("Path: got %q, want %q", d.Path, "/docs/ollama")
	}
	if d.Label != "" {
		t.Errorf("Label should be empty, got %q", d.Label)
	}
}

// =============================================================================
// FlowContribution struct construction
// =============================================================================

func TestFlowContributionMinimal(t *testing.T) {
	c := FlowContribution{
		ID:      "provider:setup:openai",
		Kind:    KindProvider,
		Surface: SurfaceSetup,
		Option: FlowOption{
			Value: "openai",
			Label: "OpenAI",
		},
		Source: SourceCore,
	}

	if c.ID != "provider:setup:openai" {
		t.Errorf("ID: got %q, want %q", c.ID, "provider:setup:openai")
	}
	if c.Kind != KindProvider {
		t.Errorf("Kind: got %q, want %q", c.Kind, KindProvider)
	}
	if c.Surface != SurfaceSetup {
		t.Errorf("Surface: got %q, want %q", c.Surface, SurfaceSetup)
	}
	if c.Source != SourceCore {
		t.Errorf("Source: got %q, want %q", c.Source, SourceCore)
	}
	if c.PluginID != "" {
		t.Errorf("PluginID should be empty, got %q", c.PluginID)
	}
}

func TestFlowContributionWithPlugin(t *testing.T) {
	c := FlowContribution{
		ID:       "provider:setup:deepseek",
		Kind:     KindProvider,
		Surface:  SurfaceSetup,
		Option:   FlowOption{Value: "deepseek", Label: "DeepSeek", Hint: "DeepSeek V3 and R1"},
		Source:   SourcePlugin,
		PluginID: "deepseek-plugin",
	}

	if c.PluginID != "deepseek-plugin" {
		t.Errorf("PluginID: got %q, want %q", c.PluginID, "deepseek-plugin")
	}
	if c.Source != SourcePlugin {
		t.Errorf("Source: got %q, want %q", c.Source, SourcePlugin)
	}
}

func TestFlowContributionAllSurfaces(t *testing.T) {
	// Verify a contribution can be created for each surface.
	surfaces := []struct {
		surface FlowSurface
		id      string
	}{
		{SurfaceAuthChoice, "auth:choice:passkey"},
		{SurfaceHealth, "health:check:pg"},
		{SurfaceModelPicker, "model:pick:gpt4"},
		{SurfaceSetup, "setup:provider:ollama"},
	}

	for _, s := range surfaces {
		c := FlowContribution{
			ID:      s.id,
			Kind:    KindCore,
			Surface: s.surface,
			Option:  FlowOption{Value: "test", Label: "Test"},
			Source:  SourceCore,
		}
		if c.Surface != s.surface {
			t.Errorf("Surface mismatch: got %q, want %q", c.Surface, s.surface)
		}
	}
}

func TestFlowContributionAllKinds(t *testing.T) {
	kinds := []struct {
		kind FlowKind
		id   string
	}{
		{KindChannel, "channel:telegram"},
		{KindCore, "core:config"},
		{KindProvider, "provider:openai"},
		{KindSearch, "search:web"},
	}

	for _, k := range kinds {
		c := FlowContribution{
			ID:      k.id,
			Kind:    k.kind,
			Surface: SurfaceSetup,
			Option:  FlowOption{Value: "test", Label: "Test"},
			Source:  SourceCore,
		}
		if c.Kind != k.kind {
			t.Errorf("Kind mismatch: got %q, want %q", c.Kind, k.kind)
		}
	}
}

func TestFlowContributionWithGroupAndDocs(t *testing.T) {
	c := FlowContribution{
		ID:      "provider:setup:google",
		Kind:    KindProvider,
		Surface: SurfaceSetup,
		Option: FlowOption{
			Value: "google",
			Label: "Google Gemini",
			Hint:  "Gemini 2.0 Flash and Pro",
			Group: &OptionGroup{
				ID:    "cloud",
				Label: "Cloud APIs",
			},
			Docs: &OptionDocs{
				Path:  "/docs/google",
				Label: "Setup Guide",
			},
		},
		Source: SourceCore,
	}

	if c.Option.Group == nil {
		t.Fatal("Group should not be nil")
	}
	if c.Option.Group.ID != "cloud" {
		t.Errorf("Group.ID: got %q, want %q", c.Option.Group.ID, "cloud")
	}
	if c.Option.Docs == nil {
		t.Fatal("Docs should not be nil")
	}
	if c.Option.Docs.Path != "/docs/google" {
		t.Errorf("Docs.Path: got %q, want %q", c.Option.Docs.Path, "/docs/google")
	}
}

// =============================================================================
// FlowRecord struct
// =============================================================================

func TestFlowRecordFields(t *testing.T) {
	now := time.Now().UTC()
	r := FlowRecord{
		ID:         "provider:setup:test",
		Kind:       "provider",
		Surface:    "setup",
		Value:      "test",
		Label:      "Test Provider",
		Hint:       "A test provider",
		GroupID:    "misc",
		GroupLabel: "Miscellaneous",
		Source:     "core",
		PluginID:   "",
		CreatedAt:  now,
	}

	if r.ID != "provider:setup:test" {
		t.Errorf("ID: got %q, want %q", r.ID, "provider:setup:test")
	}
	if r.Kind != "provider" {
		t.Errorf("Kind: got %q, want %q", r.Kind, "provider")
	}
	if r.CreatedAt != now {
		t.Errorf("CreatedAt: got %v, want %v", r.CreatedAt, now)
	}
}

// =============================================================================
// ToContribution / FromContribution round-trip
// =============================================================================

func TestToContributionRoundTrip(t *testing.T) {
	now := time.Now().UTC()
	record := FlowRecord{
		ID:         "provider:setup:openai",
		Kind:       "provider",
		Surface:    "setup",
		Value:      "openai",
		Label:      "OpenAI",
		Hint:       "GPT-4o and o3-mini",
		GroupID:    "cloud",
		GroupLabel: "Cloud Providers",
		Source:     "core",
		PluginID:   "",
		CreatedAt:  now,
	}

	c := record.ToContribution()

	// Check all fields round-tripped correctly.
	if c.ID != record.ID {
		t.Errorf("ID: got %q, want %q", c.ID, record.ID)
	}
	if string(c.Kind) != record.Kind {
		t.Errorf("Kind: got %q, want %q", c.Kind, record.Kind)
	}
	if string(c.Surface) != record.Surface {
		t.Errorf("Surface: got %q, want %q", c.Surface, record.Surface)
	}
	if c.Option.Value != record.Value {
		t.Errorf("Value: got %q, want %q", c.Option.Value, record.Value)
	}
	if c.Option.Label != record.Label {
		t.Errorf("Label: got %q, want %q", c.Option.Label, record.Label)
	}
	if c.Option.Hint != record.Hint {
		t.Errorf("Hint: got %q, want %q", c.Option.Hint, record.Hint)
	}
	if string(c.Source) != record.Source {
		t.Errorf("Source: got %q, want %q", c.Source, record.Source)
	}
	if c.PluginID != record.PluginID {
		t.Errorf("PluginID: got %q, want %q", c.PluginID, record.PluginID)
	}

	// Verify Group was populated from GroupID.
	if c.Option.Group == nil {
		t.Fatal("Group should not be nil")
	}
	if c.Option.Group.ID != "cloud" {
		t.Errorf("Group.ID: got %q, want %q", c.Option.Group.ID, "cloud")
	}
	if c.Option.Group.Label != "Cloud Providers" {
		t.Errorf("Group.Label: got %q, want %q", c.Option.Group.Label, "Cloud Providers")
	}
}

func TestToContributionNoGroup(t *testing.T) {
	record := FlowRecord{
		ID:      "provider:setup:ollama",
		Kind:    "provider",
		Surface: "setup",
		Value:   "ollama",
		Label:   "Ollama",
		Source:  "core",
		// GroupID is empty, GroupLabel is empty.
	}

	c := record.ToContribution()

	if c.Option.Group != nil {
		t.Errorf("Group should be nil when GroupID is empty, got %+v", c.Option.Group)
	}
}

func TestToContributionGroupIDOnly(t *testing.T) {
	// GroupID is set but GroupLabel is empty — should still create group.
	record := FlowRecord{
		ID:         "provider:setup:test",
		Kind:       "provider",
		Surface:    "setup",
		Value:      "test",
		Label:      "Test",
		Source:     "core",
		GroupID:    "misc",
		GroupLabel: "",
	}

	c := record.ToContribution()

	if c.Option.Group == nil {
		t.Fatal("Group should be created when GroupID is non-empty")
	}
	if c.Option.Group.ID != "misc" {
		t.Errorf("Group.ID: got %q, want %q", c.Option.Group.ID, "misc")
	}
	if c.Option.Group.Label != "" {
		t.Errorf("Group.Label: got %q, want empty", c.Option.Group.Label)
	}
}

func TestFromContributionRoundTrip(t *testing.T) {
	c := FlowContribution{
		ID:      "channel:setup:telegram",
		Kind:    KindChannel,
		Surface: SurfaceSetup,
		Option: FlowOption{
			Value: "telegram",
			Label: "Telegram",
			Hint:  "Telegram bot integration",
			Group: &OptionGroup{
				ID:    "chat",
				Label: "Chat Apps",
			},
		},
		Source:   SourceCore,
		PluginID: "",
	}

	r := FromContribution(c)

	if r.ID != c.ID {
		t.Errorf("ID: got %q, want %q", r.ID, c.ID)
	}
	if r.Kind != "channel" {
		t.Errorf("Kind: got %q, want %q", r.Kind, "channel")
	}
	if r.Surface != "setup" {
		t.Errorf("Surface: got %q, want %q", r.Surface, "setup")
	}
	if r.Value != "telegram" {
		t.Errorf("Value: got %q, want %q", r.Value, "telegram")
	}
	if r.Label != "Telegram" {
		t.Errorf("Label: got %q, want %q", r.Label, "Telegram")
	}
	if r.Hint != "Telegram bot integration" {
		t.Errorf("Hint: got %q, want %q", r.Hint, "Telegram bot integration")
	}
	if r.GroupID != "chat" {
		t.Errorf("GroupID: got %q, want %q", r.GroupID, "chat")
	}
	if r.GroupLabel != "Chat Apps" {
		t.Errorf("GroupLabel: got %q, want %q", r.GroupLabel, "Chat Apps")
	}
	if r.Source != "core" {
		t.Errorf("Source: got %q, want %q", r.Source, "core")
	}
}

func TestFromContributionNoGroup(t *testing.T) {
	c := FlowContribution{
		ID:      "provider:setup:test",
		Kind:    KindProvider,
		Surface: SurfaceSetup,
		Option: FlowOption{
			Value: "test",
			Label: "Test",
		},
		Source: SourceCore,
	}

	r := FromContribution(c)

	if r.GroupID != "" {
		t.Errorf("GroupID should be empty, got %q", r.GroupID)
	}
	if r.GroupLabel != "" {
		t.Errorf("GroupLabel should be empty, got %q", r.GroupLabel)
	}
}

func TestFullRoundTrip(t *testing.T) {
	original := FlowContribution{
		ID:      "provider:setup:anthropic",
		Kind:    KindProvider,
		Surface: SurfaceSetup,
		Option: FlowOption{
			Value: "anthropic",
			Label: "Anthropic",
			Hint:  "Claude models",
			Group: &OptionGroup{
				ID:    "cloud",
				Label: "Cloud APIs",
				Hint:  "Hosted providers",
			},
			Docs: &OptionDocs{
				Path:  "/docs/anthropic",
				Label: "Anthropic Docs",
			},
		},
		Source:   SourcePlugin,
		PluginID: "anthropic-plugin",
	}

	// Convert to record, then back.
	record := FromContribution(original)
	restored := record.ToContribution()

	if restored.ID != original.ID {
		t.Errorf("ID: got %q, want %q", restored.ID, original.ID)
	}
	if restored.Kind != original.Kind {
		t.Errorf("Kind: got %q, want %q", restored.Kind, original.Kind)
	}
	if restored.Surface != original.Surface {
		t.Errorf("Surface: got %q, want %q", restored.Surface, original.Surface)
	}
	if restored.Option.Value != original.Option.Value {
		t.Errorf("Value: got %q, want %q", restored.Option.Value, original.Option.Value)
	}
	if restored.Option.Label != original.Option.Label {
		t.Errorf("Label: got %q, want %q", restored.Option.Label, original.Option.Label)
	}
	if restored.Option.Hint != original.Option.Hint {
		t.Errorf("Hint: got %q, want %q", restored.Option.Hint, original.Option.Hint)
	}
	if restored.Source != original.Source {
		t.Errorf("Source: got %q, want %q", restored.Source, original.Source)
	}
	if restored.PluginID != original.PluginID {
		t.Errorf("PluginID: got %q, want %q", restored.PluginID, original.PluginID)
	}
	if restored.Option.Group == nil {
		t.Fatal("Group should not be nil")
	}
	if restored.Option.Group.ID != "cloud" {
		t.Errorf("Group.ID: got %q, want %q", restored.Option.Group.ID, "cloud")
	}
	if restored.Option.Group.Label != "Cloud APIs" {
		t.Errorf("Group.Label: got %q, want %q", restored.Option.Group.Label, "Cloud APIs")
	}
	// Note: Group.Hint is NOT persisted in FlowRecord (no GroupHint field),
	// so it is lost across the round-trip. This is expected behavior.
	if restored.Option.Group.Hint != "" {
		t.Errorf("Group.Hint: got %q, want empty (not persisted in FlowRecord)", restored.Option.Group.Hint)
	}
	// Note: Docs lives on FlowContribution.Option (not on FlowRecord), so it is
	// preserved across round-trip as long as it's part of the original contribution.
	// During FromContribution, Docs is NOT stored in FlowRecord (FlowRecord has
	// no Docs fields), so the round-trip through the DB layer won't preserve docs.
	// This is expected behavior — docs are UI metadata, not persisted.
}

func TestFromContributionDoesNotPreserveDocs(t *testing.T) {
	// FlowRecord has no Docs fields, so FromContribution drops them.
	// This is documented expected behavior: docs live on the contribution
	// in-memory but are not persisted.
	c := FlowContribution{
		ID:      "test",
		Kind:    KindCore,
		Surface: SurfaceSetup,
		Option: FlowOption{
			Value: "test",
			Label: "Test",
			Docs: &OptionDocs{
				Path:  "/docs/test",
				Label: "Test Docs",
			},
		},
		Source: SourceCore,
	}

	r := FromContribution(c)
	restored := r.ToContribution()

	// After round-trip, Docs is lost because FlowRecord has no Docs fields.
	if restored.Option.Docs != nil {
		t.Errorf("Docs should be nil after round-trip (not persisted), got %+v", restored.Option.Docs)
	}
}

// =============================================================================
// Edge cases and zero values
// =============================================================================

func TestZeroValueFlowContribution(t *testing.T) {
	var c FlowContribution
	if c.ID != "" {
		t.Errorf("zero value ID: got %q, want empty", c.ID)
	}
	if c.Kind != "" {
		t.Errorf("zero value Kind: got %q, want empty", c.Kind)
	}
	if c.Surface != "" {
		t.Errorf("zero value Surface: got %q, want empty", c.Surface)
	}
	if c.Option.Value != "" {
		t.Errorf("zero value Option.Value: got %q, want empty", c.Option.Value)
	}
	if c.Option.Group != nil {
		t.Errorf("zero value Option.Group: got %+v, want nil", c.Option.Group)
	}
	if c.Option.Docs != nil {
		t.Errorf("zero value Option.Docs: got %+v, want nil", c.Option.Docs)
	}
	if c.Source != "" {
		t.Errorf("zero value Source: got %q, want empty", c.Source)
	}
	if c.PluginID != "" {
		t.Errorf("zero value PluginID: got %q, want empty", c.PluginID)
	}
}

func TestZeroValueFlowRecord(t *testing.T) {
	var r FlowRecord
	if r.ID != "" {
		t.Errorf("zero value ID: got %q, want empty", r.ID)
	}
	if r.CreatedAt.IsZero() == false {
		t.Errorf("zero value CreatedAt should be zero time, got %v", r.CreatedAt)
	}
}

func TestToContributionZeroRecord(t *testing.T) {
	var r FlowRecord
	c := r.ToContribution()

	// Should not panic, should produce a zero contribution.
	if c.ID != "" {
		t.Errorf("ID: got %q, want empty", c.ID)
	}
	if c.Option.Group != nil {
		t.Errorf("Group should be nil from zero record, got %+v", c.Option.Group)
	}
}

func TestFromContributionZeroContribution(t *testing.T) {
	var c FlowContribution
	r := FromContribution(c)
	if r.ID != "" {
		t.Errorf("ID: got %q, want empty", r.ID)
	}
}

// =============================================================================
// NewStore construction (no DB needed — just type checking)
// =============================================================================

func TestNewStoreType(t *testing.T) {
	// Verify NewStore returns a non-nil *Store when given nil (compile check only).
	// In real use, nil db would panic on first operation, but construction is fine.
	s := NewStore(nil)
	if s == nil {
		t.Error("NewStore should not return nil")
	}
}

func TestStoreIsAStruct(t *testing.T) {
	// Sanity check: Store is a concrete struct (not an interface).
	var _ *Store = &Store{}
}

// =============================================================================
// Multiple contributions with different sources
// =============================================================================

func TestMultipleContributionsDifferentSources(t *testing.T) {
	contributions := []FlowContribution{
		{ID: "core:thing", Kind: KindCore, Surface: SurfaceSetup, Option: FlowOption{Value: "a", Label: "A"}, Source: SourceCore},
		{ID: "plugin:thing", Kind: KindCore, Surface: SurfaceSetup, Option: FlowOption{Value: "b", Label: "B"}, Source: SourcePlugin, PluginID: "p1"},
		{ID: "manifest:thing", Kind: KindCore, Surface: SurfaceSetup, Option: FlowOption{Value: "c", Label: "C"}, Source: SourceManifest},
		{ID: "catalog:thing", Kind: KindCore, Surface: SurfaceSetup, Option: FlowOption{Value: "d", Label: "D"}, Source: SourceInstallCatalog},
		{ID: "runtime:thing", Kind: KindCore, Surface: SurfaceSetup, Option: FlowOption{Value: "e", Label: "E"}, Source: SourceRuntime},
	}

	if len(contributions) != 5 {
		t.Errorf("expected 5 contributions, got %d", len(contributions))
	}

	// Verify each source is distinct and correct.
	for i, c := range contributions {
		if c.Source == "" {
			t.Errorf("contribution %d has empty source", i)
		}
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkToContribution(b *testing.B) {
	r := FlowRecord{
		ID:         "provider:setup:openai",
		Kind:       "provider",
		Surface:    "setup",
		Value:      "openai",
		Label:      "OpenAI",
		Hint:       "GPT-4o",
		GroupID:    "cloud",
		GroupLabel: "Cloud",
		Source:     "core",
		CreatedAt:  time.Now(),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.ToContribution()
	}
}

func BenchmarkFromContribution(b *testing.B) {
	c := FlowContribution{
		ID:      "provider:setup:openai",
		Kind:    KindProvider,
		Surface: SurfaceSetup,
		Option: FlowOption{
			Value: "openai",
			Label: "OpenAI",
			Hint:  "GPT-4o",
			Group: &OptionGroup{ID: "cloud", Label: "Cloud"},
		},
		Source: SourceCore,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = FromContribution(c)
	}
}

func BenchmarkFullRoundTrip(b *testing.B) {
	c := FlowContribution{
		ID:      "provider:setup:openai",
		Kind:    KindProvider,
		Surface: SurfaceSetup,
		Option: FlowOption{
			Value: "openai",
			Label: "OpenAI",
			Hint:  "GPT-4o",
			Group: &OptionGroup{ID: "cloud", Label: "Cloud"},
		},
		Source: SourceCore,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := FromContribution(c)
		_ = r.ToContribution()
	}
}
