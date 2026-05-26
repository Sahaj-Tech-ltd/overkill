package tui

import (
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// catalogFromJSON builds a Catalog from a JSON literal so the test
// reads like the wire format the rest of the codebase consumes.
func catalogFromJSON(t *testing.T, raw string) *providers.Catalog {
	t.Helper()
	cat, err := providers.ParseCatalog([]byte(raw), providers.SourceLive)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return cat
}

const fixtureCatalog = `{
  "anthropic": {
    "id": "anthropic",
    "name": "Anthropic",
    "models": {
      "claude-3-5-sonnet": {
        "id": "claude-3-5-sonnet",
        "name": "Claude 3.5 Sonnet",
        "modalities": {"input": ["text", "image"], "output": ["text"]}
      },
      "claude-haiku-text": {
        "id": "claude-haiku-text",
        "name": "Claude Haiku (text only)",
        "modalities": {"input": ["text"], "output": ["text"]}
      }
    }
  },
  "openai": {
    "id": "openai",
    "name": "OpenAI",
    "models": {
      "gpt-4o": {
        "id": "gpt-4o",
        "name": "GPT-4o",
        "modalities": {"input": ["text", "image"], "output": ["text"]}
      }
    }
  }
}`

func TestCheckVisionCapability_NoAttachmentsAllows(t *testing.T) {
	cat := catalogFromJSON(t, fixtureCatalog)
	res := checkVisionCapability(cat, "claude-haiku-text", nil)
	if !res.OK {
		t.Errorf("no attachments should pass: %+v", res)
	}
}

func TestCheckVisionCapability_NilCatalogFallsOpen(t *testing.T) {
	atts := []providers.Attachment{{Kind: providers.AttachmentImage}}
	res := checkVisionCapability(nil, "claude-haiku-text", atts)
	if !res.OK {
		t.Errorf("nil catalog should fall open: %+v", res)
	}
}

func TestCheckVisionCapability_VisionModelPasses(t *testing.T) {
	cat := catalogFromJSON(t, fixtureCatalog)
	atts := []providers.Attachment{{Kind: providers.AttachmentImage}}
	res := checkVisionCapability(cat, "claude-3-5-sonnet", atts)
	if !res.OK {
		t.Errorf("vision-capable model should pass: %+v", res)
	}
}

func TestCheckVisionCapability_NonVisionModelBlocks(t *testing.T) {
	cat := catalogFromJSON(t, fixtureCatalog)
	atts := []providers.Attachment{{Kind: providers.AttachmentImage}}
	res := checkVisionCapability(cat, "claude-haiku-text", atts)
	if res.OK {
		t.Errorf("non-vision model should block")
	}
	if res.Suggest != "claude-3-5-sonnet" {
		t.Errorf("should suggest sibling vision model: got %q", res.Suggest)
	}
	if res.Reason == "" {
		t.Errorf("reason should be populated")
	}
}

func TestCheckVisionCapability_UnknownModelFallsOpen(t *testing.T) {
	cat := catalogFromJSON(t, fixtureCatalog)
	atts := []providers.Attachment{{Kind: providers.AttachmentImage}}
	res := checkVisionCapability(cat, "totally-custom-endpoint", atts)
	if !res.OK {
		t.Errorf("unknown model should fall open (can't make a confident claim): %+v", res)
	}
}

func TestLookupModel_ProviderPrefix(t *testing.T) {
	cat := catalogFromJSON(t, fixtureCatalog)
	m, pid := lookupModel(cat, "openai/gpt-4o")
	if m == nil {
		t.Fatal("provider-prefixed lookup failed")
	}
	if pid != "openai" {
		t.Errorf("provider id: %s", pid)
	}
}

func TestLookupModel_BareID(t *testing.T) {
	cat := catalogFromJSON(t, fixtureCatalog)
	m, pid := lookupModel(cat, "gpt-4o")
	if m == nil {
		t.Fatal("bare-id lookup failed")
	}
	if pid != "openai" {
		t.Errorf("bare-id should resolve provider: %s", pid)
	}
}

func TestModelSupportsVision(t *testing.T) {
	yes := providers.CatalogModel{
		Modalities: providers.CatalogModalities{Input: []string{"text", "image"}},
	}
	no := providers.CatalogModel{
		Modalities: providers.CatalogModalities{Input: []string{"text"}},
	}
	if !modelSupportsVision(yes) {
		t.Error("text+image model should support vision")
	}
	if modelSupportsVision(no) {
		t.Error("text-only model should not support vision")
	}
}

func TestHasImageAttachment(t *testing.T) {
	if hasImageAttachment(nil) {
		t.Error("nil should be false")
	}
	if !hasImageAttachment([]providers.Attachment{{Kind: providers.AttachmentImage}}) {
		t.Error("image kind should be true")
	}
}
