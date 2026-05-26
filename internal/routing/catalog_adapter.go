// Package routing — TOML catalog integration (master plan §4.2 + §5.2).
//
// The router was originally constructed from a network catalog
// (providers.FetchCatalog → models.dev mirror). The §4.2 plan calls for
// a LOCAL TOML catalog at `~/.overkill/models/` as the canonical
// source. This adapter bridges the local catalog into the existing
// SmartRouter API and adds family/capability operations the network
// catalog couldn't express.
//
// Two integration points:
//  1. Construction-time: ProviderModelsFromCatalog flattens local TOML
//     into the existing `[]ProviderModels` shape so NewSmartRouter
//     keeps the same signature.
//  2. Runtime: SmartRouter.WithCatalog attaches the catalog so
//     ModelInFamily / ModelWithCapabilities / FailoverInFamily can
//     consult it for family-aware decisions.
package routing

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/models"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// ProviderModelsFromCatalog converts a TOML model catalog into the
// []ProviderModels slice the SmartRouter constructor expects. One
// ProviderModels per provider, derived from the ID prefix before `/`
// ("openai/gpt-5" → provider "openai"). Returns nil when the catalog
// is empty so callers can fall back to a network catalog cleanly.
func ProviderModelsFromCatalog(cat *models.Catalog) []ProviderModels {
	if cat == nil {
		return nil
	}
	all := cat.List()
	if len(all) == 0 {
		return nil
	}
	byProvider := make(map[string][]providers.Model)
	for _, m := range all {
		providerID := providerOf(m.ID)
		flat := providers.Model{
			ID:               m.ID,
			Name:             m.DisplayName,
			Family:           m.Family,
			ContextWindow:    m.ContextWindow,
			DefaultMaxTokens: m.MaxOutputTokens,
			CostIn:           m.Cost.Input,
			CostOut:          m.Cost.Output,
			CostCacheIn:      m.Cost.CacheRead,
			CostCacheOut:     m.Cost.CacheWrite,
			SupportsTools:    m.Capabilities.ToolCall,
			SupportsVision:   stringSliceContains(m.Modalities.Input, "image"),
			Reasoning:        m.Capabilities.Reasoning,
			Temperature:      m.Capabilities.Temperature,
			Attachment:       m.Capabilities.Attachment,
			OpenWeights:      m.Capabilities.OpenWeights,
		}
		if flat.Name == "" {
			flat.Name = m.ID
		}
		byProvider[providerID] = append(byProvider[providerID], flat)
	}
	out := make([]ProviderModels, 0, len(byProvider))
	keys := make([]string, 0, len(byProvider))
	for k := range byProvider {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, ProviderModels{ProviderName: k, Models: byProvider[k]})
	}
	return out
}

// WithCatalog attaches a TOML catalog so the router can answer family
// + capability queries that the flat []ProviderModels view can't
// represent cheaply. Idempotent — replaces any previously attached
// catalog. Pass nil to detach.
func (r *SmartRouter) WithCatalog(cat *models.Catalog) *SmartRouter {
	r.catalog = cat
	return r
}

// ModelInFamily returns the cheapest non-deprecated model in the
// family, falling back to the registered providers when the catalog
// isn't attached. Empty family returns ErrNotFound.
//
// Returns (modelID, providerID, error). Provider derived from
// the model ID's prefix.
func (r *SmartRouter) ModelInFamily(family string) (string, string, error) {
	if family == "" {
		return "", "", fmt.Errorf("routing: family is required")
	}
	if r.catalog != nil {
		m, err := r.catalog.CheapestInFamily(family)
		if err == nil {
			return m.ID, providerOf(m.ID), nil
		}
		// Catalog miss falls through to legacy lookup below.
	}
	// Legacy fallback: scan registered providers for models whose
	// family field matches. Cheap to do here because the routes
	// slice is small.
	var best *providers.Model
	var bestProv string
	for _, pm := range r.providers {
		for i := range pm.Models {
			m := &pm.Models[i]
			if !strings.EqualFold(m.Family, family) {
				continue
			}
			if best == nil || m.CostOut < best.CostOut {
				best = m
				bestProv = pm.ProviderName
			}
		}
	}
	if best == nil {
		return "", "", fmt.Errorf("routing: no models in family %q", family)
	}
	return best.ID, bestProv, nil
}

// ModelWithCapabilities returns the cheapest model whose capability
// flags satisfy every requested flag in req. When the catalog is
// attached we use its rich Capabilities struct; otherwise fall back
// to the legacy providers.Model bool fields.
func (r *SmartRouter) ModelWithCapabilities(req models.Capabilities) (string, string, error) {
	if r.catalog != nil {
		hits := r.catalog.ListWithCapability(req)
		var best *models.Model
		for _, m := range hits {
			if m.Deprecated {
				continue
			}
			if best == nil || m.Cost.Output < best.Cost.Output {
				best = m
			}
		}
		if best != nil {
			return best.ID, providerOf(best.ID), nil
		}
	}
	// Legacy fallback over registered providers.
	var best *providers.Model
	var bestProv string
	for _, pm := range r.providers {
		for i := range pm.Models {
			m := &pm.Models[i]
			if req.Reasoning && !m.Reasoning {
				continue
			}
			if req.ToolCall && !m.SupportsTools {
				continue
			}
			if req.Attachment && !m.Attachment {
				continue
			}
			if req.OpenWeights && !m.OpenWeights {
				continue
			}
			if req.Temperature && !m.Temperature {
				continue
			}
			if best == nil || m.CostOut < best.CostOut {
				best = m
				bestProv = pm.ProviderName
			}
		}
	}
	if best == nil {
		return "", "", fmt.Errorf("routing: no model satisfies capabilities %+v", req)
	}
	return best.ID, bestProv, nil
}

// FailoverInFamily returns the family's members ordered by cost,
// non-deprecated only. Caller iterates and picks the first reachable
// provider — e.g. when claude-opus's primary endpoint is rate-limited,
// retry the next-cheapest opus before leaving the family.
func (r *SmartRouter) FailoverInFamily(family string) []string {
	if r.catalog == nil {
		// Legacy: scan registered providers.
		var ms []providers.Model
		for _, pm := range r.providers {
			for _, m := range pm.Models {
				if strings.EqualFold(m.Family, family) {
					ms = append(ms, m)
				}
			}
		}
		sort.Slice(ms, func(i, j int) bool { return ms[i].CostOut < ms[j].CostOut })
		ids := make([]string, len(ms))
		for i, m := range ms {
			ids[i] = m.ID
		}
		return ids
	}
	members := r.catalog.ListFamily(family)
	filtered := make([]*models.Model, 0, len(members))
	for _, m := range members {
		if m.Deprecated {
			continue
		}
		filtered = append(filtered, m)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Cost.Output < filtered[j].Cost.Output
	})
	ids := make([]string, len(filtered))
	for i, m := range filtered {
		ids[i] = m.ID
	}
	return ids
}

// providerOf returns the provider component of a catalog ID
// ("openai/gpt-5" → "openai"). Falls back to the full ID when there's
// no slash so we never return empty.
func providerOf(id string) string {
	i := strings.Index(id, "/")
	if i <= 0 {
		return id
	}
	return id[:i]
}

func stringSliceContains(xs []string, want string) bool {
	for _, x := range xs {
		if strings.EqualFold(x, want) {
			return true
		}
	}
	return false
}
