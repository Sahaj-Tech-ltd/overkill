package flows

import (
	"context"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
)

// =============================================================================
// Store integration tests (skip if no Postgres available)
// =============================================================================

func TestStoreMigrate(t *testing.T) {
	db, cleanup := testpg.Open(t)
	defer cleanup()

	ctx := context.Background()
	store := NewStore(db)

	// Migrate should succeed.
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Migrate should be idempotent.
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate (idempotent): %v", err)
	}
}

func TestStoreRegisterProvider(t *testing.T) {
	db, cleanup := testpg.Open(t)
	defer cleanup()

	ctx := context.Background()
	store := NewStore(db)

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	c := FlowContribution{
		ID:      "provider:setup:openai",
		Kind:    KindProvider,
		Surface: SurfaceSetup,
		Option: FlowOption{
			Value: "openai",
			Label: "OpenAI",
			Hint:  "GPT-4o and o3-mini",
		},
		Source: SourceCore,
	}

	if err := store.RegisterProvider(ctx, c); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	// List back and verify.
	contribs, err := store.ListContributions(ctx, KindProvider, SurfaceSetup)
	if err != nil {
		t.Fatalf("ListContributions: %v", err)
	}

	if len(contribs) != 1 {
		t.Fatalf("expected 1 contribution, got %d", len(contribs))
	}

	got := contribs[0]
	if got.ID != "provider:setup:openai" {
		t.Errorf("ID: got %q, want %q", got.ID, "provider:setup:openai")
	}
	if got.Kind != KindProvider {
		t.Errorf("Kind: got %q, want %q", got.Kind, KindProvider)
	}
	if got.Option.Value != "openai" {
		t.Errorf("Value: got %q, want %q", got.Option.Value, "openai")
	}
	if got.Option.Label != "OpenAI" {
		t.Errorf("Label: got %q, want %q", got.Option.Label, "OpenAI")
	}
	if got.Option.Hint != "GPT-4o and o3-mini" {
		t.Errorf("Hint: got %q, want %q", got.Option.Hint, "GPT-4o and o3-mini")
	}
	if got.Source != SourceCore {
		t.Errorf("Source: got %q, want %q", got.Source, SourceCore)
	}
}

func TestStoreRegisterChannel(t *testing.T) {
	db, cleanup := testpg.Open(t)
	defer cleanup()

	ctx := context.Background()
	store := NewStore(db)

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	c := FlowContribution{
		ID:      "channel:setup:telegram",
		Kind:    KindChannel,
		Surface: SurfaceSetup,
		Option: FlowOption{
			Value: "telegram",
			Label: "Telegram",
			Hint:  "Telegram bot",
			Group: &OptionGroup{
				ID:    "chat",
				Label: "Chat Apps",
			},
		},
		Source: SourceCore,
	}

	if err := store.RegisterChannel(ctx, c); err != nil {
		t.Fatalf("RegisterChannel: %v", err)
	}

	contribs, err := store.ListContributions(ctx, KindChannel, SurfaceSetup)
	if err != nil {
		t.Fatalf("ListContributions: %v", err)
	}

	if len(contribs) != 1 {
		t.Fatalf("expected 1 contribution, got %d", len(contribs))
	}

	got := contribs[0]
	if got.ID != "channel:setup:telegram" {
		t.Errorf("ID: got %q, want %q", got.ID, "channel:setup:telegram")
	}
	if got.Option.Group == nil {
		t.Fatal("Group should not be nil")
	}
	if got.Option.Group.ID != "chat" {
		t.Errorf("Group.ID: got %q, want %q", got.Option.Group.ID, "chat")
	}
}

func TestStoreUpsert(t *testing.T) {
	db, cleanup := testpg.Open(t)
	defer cleanup()

	ctx := context.Background()
	store := NewStore(db)

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Insert initial version.
	c1 := FlowContribution{
		ID:      "provider:setup:test",
		Kind:    KindProvider,
		Surface: SurfaceSetup,
		Option: FlowOption{
			Value: "test",
			Label: "Test v1",
		},
		Source: SourceCore,
	}
	if err := store.RegisterProvider(ctx, c1); err != nil {
		t.Fatalf("RegisterProvider v1: %v", err)
	}

	// Upsert with updated fields.
	c2 := FlowContribution{
		ID:      "provider:setup:test", // Same ID
		Kind:    KindProvider,
		Surface: SurfaceSetup,
		Option: FlowOption{
			Value: "test",
			Label: "Test v2",
			Hint:  "Updated hint",
		},
		Source:   SourcePlugin,
		PluginID: "test-plugin",
	}
	if err := store.RegisterProvider(ctx, c2); err != nil {
		t.Fatalf("RegisterProvider v2: %v", err)
	}

	// Should still only have 1 row (upsert).
	contribs, err := store.ListContributions(ctx, KindProvider, SurfaceSetup)
	if err != nil {
		t.Fatalf("ListContributions: %v", err)
	}
	if len(contribs) != 1 {
		t.Fatalf("expected 1 contribution after upsert, got %d", len(contribs))
	}

	got := contribs[0]
	if got.Option.Label != "Test v2" {
		t.Errorf("Label after upsert: got %q, want %q", got.Option.Label, "Test v2")
	}
	if got.Option.Hint != "Updated hint" {
		t.Errorf("Hint after upsert: got %q, want %q", got.Option.Hint, "Updated hint")
	}
	if got.Source != SourcePlugin {
		t.Errorf("Source after upsert: got %q, want %q", got.Source, SourcePlugin)
	}
	if got.PluginID != "test-plugin" {
		t.Errorf("PluginID after upsert: got %q, want %q", got.PluginID, "test-plugin")
	}
}

func TestStoreRemove(t *testing.T) {
	db, cleanup := testpg.Open(t)
	defer cleanup()

	ctx := context.Background()
	store := NewStore(db)

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	c := FlowContribution{
		ID:      "provider:setup:to-remove",
		Kind:    KindProvider,
		Surface: SurfaceSetup,
		Option: FlowOption{
			Value: "remove-me",
			Label: "Remove Me",
		},
		Source:   SourcePlugin,
		PluginID: "bad-plugin",
	}
	if err := store.RegisterProvider(ctx, c); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	// Verify it's there.
	contribs, err := store.ListContributions(ctx, KindProvider, SurfaceSetup)
	if err != nil {
		t.Fatalf("ListContributions: %v", err)
	}
	if len(contribs) != 1 {
		t.Fatalf("expected 1 contribution before remove, got %d", len(contribs))
	}

	// Remove it.
	if err := store.Remove(ctx, "provider:setup:to-remove"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// Verify it's gone.
	contribs, err = store.ListContributions(ctx, KindProvider, SurfaceSetup)
	if err != nil {
		t.Fatalf("ListContributions: %v", err)
	}
	if len(contribs) != 0 {
		t.Fatalf("expected 0 contributions after remove, got %d", len(contribs))
	}
}

func TestStoreRemoveNonExistent(t *testing.T) {
	db, cleanup := testpg.Open(t)
	defer cleanup()

	ctx := context.Background()
	store := NewStore(db)

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Removing a non-existent ID should not error (DELETE with no match is OK).
	if err := store.Remove(ctx, "does:not:exist"); err != nil {
		t.Errorf("Remove non-existent should not error: %v", err)
	}
}

func TestStoreListContributionsEmpty(t *testing.T) {
	db, cleanup := testpg.Open(t)
	defer cleanup()

	ctx := context.Background()
	store := NewStore(db)

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Listing an empty table should return empty slice, not nil.
	contribs, err := store.ListContributions(ctx, KindProvider, SurfaceSetup)
	if err != nil {
		t.Fatalf("ListContributions: %v", err)
	}
	if contribs == nil {
		t.Error("ListContributions should return empty slice, not nil")
	}
	if len(contribs) != 0 {
		t.Errorf("expected 0 contributions, got %d", len(contribs))
	}
}

func TestStoreListContributionsFiltered(t *testing.T) {
	db, cleanup := testpg.Open(t)
	defer cleanup()

	ctx := context.Background()
	store := NewStore(db)

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Insert a provider flow.
	p := FlowContribution{
		ID:      "provider:setup:openai",
		Kind:    KindProvider,
		Surface: SurfaceSetup,
		Option:  FlowOption{Value: "openai", Label: "OpenAI"},
		Source:  SourceCore,
	}
	if err := store.RegisterProvider(ctx, p); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	// Insert a channel flow.
	ch := FlowContribution{
		ID:      "channel:setup:telegram",
		Kind:    KindChannel,
		Surface: SurfaceSetup,
		Option:  FlowOption{Value: "telegram", Label: "Telegram"},
		Source:  SourceCore,
	}
	if err := store.RegisterChannel(ctx, ch); err != nil {
		t.Fatalf("RegisterChannel: %v", err)
	}

	// Listing by Provider kind should NOT include the channel.
	providerContribs, err := store.ListContributions(ctx, KindProvider, SurfaceSetup)
	if err != nil {
		t.Fatalf("ListContributions provider: %v", err)
	}
	if len(providerContribs) != 1 {
		t.Fatalf("expected 1 provider contribution, got %d", len(providerContribs))
	}
	if providerContribs[0].ID != "provider:setup:openai" {
		t.Errorf("expected openai, got %q", providerContribs[0].ID)
	}

	// Listing by Channel kind should NOT include the provider.
	channelContribs, err := store.ListContributions(ctx, KindChannel, SurfaceSetup)
	if err != nil {
		t.Fatalf("ListContributions channel: %v", err)
	}
	if len(channelContribs) != 1 {
		t.Fatalf("expected 1 channel contribution, got %d", len(channelContribs))
	}
	if channelContribs[0].ID != "channel:setup:telegram" {
		t.Errorf("expected telegram, got %q", channelContribs[0].ID)
	}

	// Different surface should return empty.
	healthContribs, err := store.ListContributions(ctx, KindProvider, SurfaceHealth)
	if err != nil {
		t.Fatalf("ListContributions health: %v", err)
	}
	if len(healthContribs) != 0 {
		t.Errorf("expected 0 health contributions, got %d", len(healthContribs))
	}
}

func TestStoreListAll(t *testing.T) {
	db, cleanup := testpg.Open(t)
	defer cleanup()

	ctx := context.Background()
	store := NewStore(db)

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Insert several contributions of different kinds/surfaces.
	items := []FlowContribution{
		{ID: "provider:setup:a", Kind: KindProvider, Surface: SurfaceSetup, Option: FlowOption{Value: "a", Label: "A"}, Source: SourceCore},
		{ID: "provider:health:b", Kind: KindProvider, Surface: SurfaceHealth, Option: FlowOption{Value: "b", Label: "B"}, Source: SourceCore},
		{ID: "channel:setup:c", Kind: KindChannel, Surface: SurfaceSetup, Option: FlowOption{Value: "c", Label: "C"}, Source: SourceCore},
	}

	store.RegisterProvider(ctx, items[0])
	store.RegisterProvider(ctx, items[1])
	store.RegisterChannel(ctx, items[2])

	all, err := store.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 contributions, got %d", len(all))
	}

	// ListAll returns newest first, so items[2] should be first.
	if all[0].ID != "channel:setup:c" {
		t.Errorf("newest should be first, got %q", all[0].ID)
	}
}

func TestStoreRegisterProviderWithGroup(t *testing.T) {
	db, cleanup := testpg.Open(t)
	defer cleanup()

	ctx := context.Background()
	store := NewStore(db)

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	c := FlowContribution{
		ID:      "provider:setup:ollama",
		Kind:    KindProvider,
		Surface: SurfaceSetup,
		Option: FlowOption{
			Value: "ollama",
			Label: "Ollama",
			Group: &OptionGroup{
				ID:    "local",
				Label: "Local Models",
			},
		},
		Source: SourceCore,
	}

	if err := store.RegisterProvider(ctx, c); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	contribs, err := store.ListContributions(ctx, KindProvider, SurfaceSetup)
	if err != nil {
		t.Fatalf("ListContributions: %v", err)
	}
	if len(contribs) != 1 {
		t.Fatalf("expected 1 contribution, got %d", len(contribs))
	}

	got := contribs[0]
	if got.Option.Group == nil {
		t.Fatal("Group should not be nil after round-trip")
	}
	if got.Option.Group.ID != "local" {
		t.Errorf("Group.ID: got %q, want %q", got.Option.Group.ID, "local")
	}
	if got.Option.Group.Label != "Local Models" {
		t.Errorf("Group.Label: got %q, want %q", got.Option.Group.Label, "Local Models")
	}
}

func TestStoreEnsureSchema(t *testing.T) {
	db, cleanup := testpg.Open(t)
	defer cleanup()

	ctx := context.Background()
	store := NewStore(db)

	// EnsureSchema is just an alias for Migrate.
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
}
