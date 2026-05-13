// Package tui — vision-capability guard (layer 3 of image paste).
//
// Before a message with image attachments goes to the agent, we check
// whether the active model actually accepts images. The check is local
// (against the cached models.dev catalog) so it's fast enough to run on
// every send; the cost is just a hash lookup. When the catalog isn't
// loaded yet we fall open — better UX than blocking a legitimate send
// on missing local state. The provider will reject if needed.
package tui

import (
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// visionGuardResult is the outcome of the pre-send capability check.
type visionGuardResult struct {
	// OK means the send is allowed to proceed.
	OK bool
	// Reason is a user-facing string for the toast when OK is false.
	// Empty when OK is true.
	Reason string
	// Suggest is a comma-separated list of vision-capable model IDs
	// in the same provider family the user can switch to. Empty when
	// no suggestion is available.
	Suggest string
}

// checkVisionCapability is the guard. It's a pure function over the
// catalog + active model ID + attachments, so it's trivially testable
// without the full TUI surface.
func checkVisionCapability(catalog *providers.Catalog, activeModelID string, attachments []providers.Attachment) visionGuardResult {
	if !hasImageAttachment(attachments) {
		return visionGuardResult{OK: true}
	}
	if catalog == nil {
		// Catalog not loaded — fall open. We're optimistic by design;
		// the provider will reject with a clear error if it has to.
		return visionGuardResult{OK: true}
	}
	model, providerID := lookupModel(catalog, activeModelID)
	if model == nil {
		// Active model isn't in the catalog (custom endpoint, gateway).
		// Fall open — we can't make a confident claim either way.
		return visionGuardResult{OK: true}
	}
	if modelSupportsVision(*model) {
		return visionGuardResult{OK: true}
	}
	suggest := suggestVisionAlternatives(catalog, providerID)
	reason := "model " + activeModelID + " does not accept images"
	if suggest != "" {
		reason += " — try /model " + suggest
	} else {
		reason += " — pick a vision-capable model via /model"
	}
	return visionGuardResult{
		OK:      false,
		Reason:  reason,
		Suggest: suggest,
	}
}

// hasImageAttachment reports whether any attachment is an image.
// Non-image kinds (future audio, pdf) are ignored — the guard only
// fires for image-vs-vision specifically.
func hasImageAttachment(atts []providers.Attachment) bool {
	for _, a := range atts {
		if a.Kind == providers.AttachmentImage {
			return true
		}
	}
	return false
}

// lookupModel resolves the active model ID against the catalog. The ID
// can be either bare ("gpt-4o") or provider-prefixed ("openai/gpt-4o");
// we accept both because different config surfaces use different forms.
// Returns the matched model and its provider ID, or (nil, "") on miss.
func lookupModel(catalog *providers.Catalog, modelID string) (*providers.CatalogModel, string) {
	if catalog == nil || modelID == "" {
		return nil, ""
	}
	wantProvider := ""
	wantID := modelID
	if idx := strings.Index(modelID, "/"); idx > 0 {
		wantProvider = modelID[:idx]
		wantID = modelID[idx+1:]
	}
	for _, p := range catalog.Providers() {
		if wantProvider != "" && p.ID != wantProvider {
			continue
		}
		if m, ok := p.Models[wantID]; ok {
			return &m, p.ID
		}
		// Some catalogs prefix the model ID inside their map ("anthropic/..").
		// Fall through to a full scan so the lookup is robust.
		for id, m := range p.Models {
			if id == wantID || strings.HasSuffix(id, "/"+wantID) {
				return &m, p.ID
			}
		}
	}
	return nil, ""
}

// modelSupportsVision returns true if the model declares "image" in
// its input modalities. We use Modalities rather than the older
// Attachment bool because modalities is the authoritative field on
// recent models.dev catalogs.
func modelSupportsVision(m providers.CatalogModel) bool {
	for _, mod := range m.Modalities.Input {
		if strings.EqualFold(mod, "image") {
			return true
		}
	}
	return false
}

// suggestVisionAlternatives picks at most one vision-capable model
// from the same provider family. Returning more than one would clutter
// the toast; the user can always /model to see the full picker.
func suggestVisionAlternatives(catalog *providers.Catalog, providerID string) string {
	if catalog == nil || providerID == "" {
		return ""
	}
	for _, p := range catalog.Providers() {
		if p.ID != providerID {
			continue
		}
		for id, m := range p.Models {
			if modelSupportsVision(m) {
				return id
			}
		}
	}
	return ""
}

// activeModelSupportsVision is the appModel-bound convenience wrapper.
// Returns true when the caller can safely send images.
func (m *appModel) activeModelSupportsVision() bool {
	if m.app == nil || m.app.Agent == nil {
		return false
	}
	res := checkVisionCapability(m.modelCatalog, m.app.Agent.Model(), []providers.Attachment{{Kind: providers.AttachmentImage}})
	return res.OK
}
