// Package providers — modelsdev.go fetches the live model catalog from
// https://models.dev/api.json with a disk cache fallback. The catalog is the
// source of truth for the TUI model picker; it is independent of which
// providers the user has actually configured.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ModelsDevURL is the canonical models.dev API endpoint. Exposed as a var so
// tests can swap it for an httptest server.
var ModelsDevURL = "https://models.dev/api.json"

// FetchTimeout caps how long the live HTTP fetch may run before falling back
// to disk cache. 5 seconds aligns with the spec.
const FetchTimeout = 5 * time.Second

// CatalogSource indicates which tier produced the loaded catalog.
type CatalogSource string

const (
	SourceLive  CatalogSource = "live"
	SourceCache CatalogSource = "cache"
	SourceBaked CatalogSource = "baked"
)

// CatalogProvider is one provider entry in the live API.
type CatalogProvider struct {
	ID     string                  `json:"id"`
	Name   string                  `json:"name"`
	NPM    string                  `json:"npm,omitempty"`
	API    string                  `json:"api,omitempty"`
	Doc    string                  `json:"doc,omitempty"`
	Env    []string                `json:"env,omitempty"`
	Models map[string]CatalogModel `json:"models"`
}

// CatalogModel is one model entry under a provider.
type CatalogModel struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Family      string            `json:"family,omitempty"`
	Attachment  bool              `json:"attachment,omitempty"`
	Reasoning   bool              `json:"reasoning,omitempty"`
	ToolCall    bool              `json:"tool_call,omitempty"`
	Temperature bool              `json:"temperature,omitempty"`
	OpenWeights bool              `json:"open_weights,omitempty"`
	Knowledge   string            `json:"knowledge,omitempty"`
	ReleaseDate string            `json:"release_date,omitempty"`
	LastUpdated string            `json:"last_updated,omitempty"`
	Modalities  CatalogModalities `json:"modalities,omitempty"`
	Cost        CatalogCost       `json:"cost,omitempty"`
	Limit       CatalogLimit      `json:"limit,omitempty"`
}

type CatalogModalities struct {
	Input  []string `json:"input,omitempty"`
	Output []string `json:"output,omitempty"`
}

type CatalogCost struct {
	Input      float64 `json:"input,omitempty"`
	Output     float64 `json:"output,omitempty"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

type CatalogLimit struct {
	Context int `json:"context,omitempty"`
	Output  int `json:"output,omitempty"`
}

// Catalog is the in-memory queryable view over the API payload.
type Catalog struct {
	mu        sync.RWMutex
	source    CatalogSource
	providers []CatalogProvider
	byID      map[string]*CatalogProvider // providerID -> provider
}

// Source returns where this catalog came from (live, cache, or baked).
func (c *Catalog) Source() CatalogSource {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.source
}

// Providers returns the providers sorted by id.
func (c *Catalog) Providers() []CatalogProvider {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]CatalogProvider, len(c.providers))
	copy(out, c.providers)
	return out
}

// Models returns the models for one provider, sorted by id. Empty if the
// providerID is unknown.
func (c *Catalog) Models(providerID string) []CatalogModel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.byID[providerID]
	if !ok {
		return nil
	}
	out := make([]CatalogModel, 0, len(p.Models))
	for _, m := range p.Models {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// All returns every model across every provider, sorted by provider then id.
// Each entry's Family is set to the provider id so callers can group easily.
func (c *Catalog) All() []FlatModel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []FlatModel
	for _, p := range c.providers {
		ids := make([]string, 0, len(p.Models))
		for id := range p.Models {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			out = append(out, FlatModel{
				ProviderID:   p.ID,
				ProviderName: p.Name,
				Model:        p.Models[id],
			})
		}
	}
	return out
}

// FlatModel pairs a model with its owning provider for flat list rendering.
type FlatModel struct {
	ProviderID   string
	ProviderName string
	Model        CatalogModel
}

// CacheTTL is how long a disk-cached catalog is considered fresh enough to
// skip the network entirely. Set generously — model lists don't move much
// hour-to-hour and SSH users feel every network call.
const CacheTTL = 24 * time.Hour

// FetchCatalog returns the model catalog with this priority:
//  1. fresh disk cache (mtime < CacheTTL old) — no network call
//  2. live API — refreshes the cache on success
//  3. stale disk cache — any mtime
//  4. baked Go fallback
//
// This keeps the model picker snappy: first launch fetches, every subsequent
// open within 24h is a single file read.
func FetchCatalog(ctx context.Context) (*Catalog, error) {
	if cached, mtime, err := readCacheWithTime(); err == nil {
		if time.Since(mtime) < CacheTTL {
			return parseCatalog(cached, SourceCache)
		}
	}

	live, liveErr := fetchLive(ctx)
	if liveErr == nil {
		_ = writeCache(live) // best-effort
		return parseCatalog(live, SourceLive)
	}

	// Network failed — fall back to whatever's on disk regardless of age.
	cached, cacheErr := readCache()
	if cacheErr == nil {
		return parseCatalog(cached, SourceCache)
	}

	baked := bakedCatalog()
	if baked != nil {
		baked.source = SourceBaked
		return baked, fmt.Errorf("live: %w; cache: %v", liveErr, cacheErr)
	}
	return nil, fmt.Errorf("live: %w; cache: %v; no baked fallback", liveErr, cacheErr)
}

// RefreshCatalog forces a network fetch, updating the disk cache. Used by
// background warmers or an explicit "refresh" command.
func RefreshCatalog(ctx context.Context) (*Catalog, error) {
	live, err := fetchLive(ctx)
	if err != nil {
		return nil, err
	}
	_ = writeCache(live)
	return parseCatalog(live, SourceLive)
}

func readCacheWithTime() ([]byte, time.Time, error) {
	path, err := cachePath()
	if err != nil {
		return nil, time.Time{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	return body, info.ModTime(), nil
}

func fetchLive(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ModelsDevURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(body) == 0 {
		return nil, errors.New("empty body")
	}
	return body, nil
}

// userAgent matches the spec: ethos/<version>. We avoid an import cycle by
// reading the version at call time from a package-level variable that the
// caller (cmd/ethos) may overwrite if it changes.
var Version = "0.1.0-dev"

func userAgent() string { return "ethos/" + Version }

func cachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ethos", "cache", "models.json"), nil
}

func writeCache(body []byte) error {
	path, err := cachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

func readCache() ([]byte, error) {
	path, err := cachePath()
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// parseCatalog decodes the models.dev payload into a Catalog. The payload is a
// flat object keyed by provider id; each value carries an optional `id` and a
// `models` map.
func parseCatalog(body []byte, source CatalogSource) (*Catalog, error) {
	raw := map[string]CatalogProvider{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	if len(raw) == 0 {
		return nil, errors.New("catalog: empty")
	}

	providerIDs := make([]string, 0, len(raw))
	for k := range raw {
		providerIDs = append(providerIDs, k)
	}
	sort.Strings(providerIDs)

	c := &Catalog{
		source: source,
		byID:   make(map[string]*CatalogProvider, len(raw)),
	}
	for _, id := range providerIDs {
		p := raw[id]
		if p.ID == "" {
			p.ID = id
		}
		if p.Name == "" {
			p.Name = id
		}
		// Backfill missing model IDs from the map key.
		if p.Models != nil {
			for mid, m := range p.Models {
				if m.ID == "" {
					m.ID = mid
				}
				if m.Name == "" {
					m.Name = mid
				}
				p.Models[mid] = m
			}
		}
		c.providers = append(c.providers, p)
	}
	for i := range c.providers {
		c.byID[c.providers[i].ID] = &c.providers[i]
	}
	return c, nil
}

// bakedCatalog returns a Catalog assembled from the in-source provider lists in
// models.go so the picker still works in fully air-gapped builds.
func bakedCatalog() *Catalog {
	type src struct {
		id     string
		name   string
		models []Model
	}
	srcs := []src{
		{"openai", "OpenAI", OpenAIModels()},
		{"anthropic", "Anthropic", AnthropicModels()},
		{"google", "Google", GeminiModels()},
		{"deepseek", "DeepSeek", DeepSeekModels()},
		{"ollama", "Ollama", OllamaModels()},
		{"openrouter", "OpenRouter", OpenRouterModels()},
		{"groq", "Groq", GroqModels()},
		{"xai", "xAI", XAIModels()},
		{"mistral", "Mistral", MistralModels()},
		{"togetherai", "Together AI", TogetherAIModels()},
		{"perplexity", "Perplexity", PerplexityModels()},
	}
	c := &Catalog{
		source: SourceBaked,
		byID:   map[string]*CatalogProvider{},
	}
	for _, s := range srcs {
		p := CatalogProvider{
			ID:     s.id,
			Name:   s.name,
			Models: map[string]CatalogModel{},
		}
		for _, m := range s.models {
			cm := CatalogModel{
				ID:        m.ID,
				Name:      m.Name,
				Family:    m.Family,
				ToolCall:  m.SupportsTools,
				Reasoning: m.Reasoning,
				Cost: CatalogCost{
					Input:      m.CostIn,
					Output:     m.CostOut,
					CacheRead:  m.CostCacheIn,
					CacheWrite: m.CostCacheOut,
				},
				Limit: CatalogLimit{Context: m.MaxTokens, Output: m.DefaultMaxTokens},
			}
			if m.SupportsVision {
				cm.Modalities.Input = []string{"text", "image"}
			} else {
				cm.Modalities.Input = []string{"text"}
			}
			cm.Modalities.Output = []string{"text"}
			p.Models[m.ID] = cm
		}
		c.providers = append(c.providers, p)
	}
	for i := range c.providers {
		c.byID[c.providers[i].ID] = &c.providers[i]
	}
	return c
}

// FormatCost renders "$in/$out per 1M" — used by the dialog rendering.
func FormatCost(in, out float64) string {
	if in == 0 && out == 0 {
		return "free"
	}
	return fmt.Sprintf("$%.2f/$%.2f /1M", in, out)
}

// FormatContext renders "128K", "1M", etc. Used by the dialog.
func FormatContext(n int) string {
	switch {
	case n <= 0:
		return ""
	case n >= 1_000_000:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dK", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// CapsTags returns short capability tags ("tools", "vision", "reasoning").
func CapsTags(m CatalogModel) []string {
	var tags []string
	if m.ToolCall {
		tags = append(tags, "tools")
	}
	for _, mod := range m.Modalities.Input {
		if strings.EqualFold(mod, "image") {
			tags = append(tags, "vision")
			break
		}
	}
	if m.Reasoning {
		tags = append(tags, "reasoning")
	}
	return tags
}
