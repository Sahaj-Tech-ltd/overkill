// Package models is the TOML-as-data model catalog (master plan §4.2).
//
// Models live as `<provider>/<model>.toml` files under a catalog root
// (typically `~/.overkill/models/`). The filename derives the ID
// (`openai/gpt-5.toml` → ID `openai/gpt-5`) so there's no
// ID-vs-filename mismatch risk.
//
// Wrapper models (OpenRouter, Groq) use `[extends] from = "..."` to
// inherit a canonical model's shape and override only cost. 240+ model
// files collapse to 5-line stubs.
//
// Loading is a single pass:
//  1. Walk the root, parse every `.toml` into a raw Model.
//  2. Resolve `extends` references — wrappers inherit from base.
//  3. Validate required fields and capability flag combinations.
//  4. Build the in-memory Catalog (map[ID]*Model + family index).
//
// The Catalog is read-only after Load. Hot-reload is future work; for
// now a config change requires a process restart (or a new Catalog
// instance via Load).
package models

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Model is the canonical record for one model in the catalog. Fields
// loaded from TOML; ID is derived from filename, not declared.
type Model struct {
	ID              string       `toml:"-" json:"id"`
	Family          string       `toml:"family" json:"family"`
	DisplayName     string       `toml:"display_name" json:"display_name,omitempty"`
	ContextWindow   int          `toml:"context_window" json:"context_window"`
	MaxOutputTokens int          `toml:"max_output_tokens" json:"max_output_tokens,omitempty"`
	Capabilities    Capabilities `toml:"capabilities" json:"capabilities"`
	Cost            Cost         `toml:"cost" json:"cost"`
	Modalities      Modalities   `toml:"modalities" json:"modalities"`
	Extends         *Extends     `toml:"extends" json:"extends,omitempty"`
	Deprecated      bool         `toml:"deprecated" json:"deprecated,omitempty"`
}

// Capabilities is the boolean flag set used for family-aware routing
// + capability filtering. "reasoning" + "tool_call" being booleans
// means routers can match on `cap.reasoning && cap.tool_call` cheaply.
type Capabilities struct {
	Reasoning        bool `toml:"reasoning" json:"reasoning"`
	ToolCall         bool `toml:"tool_call" json:"tool_call"`
	StructuredOutput bool `toml:"structured_output" json:"structured_output"`
	Temperature      bool `toml:"temperature" json:"temperature"`
	Attachment       bool `toml:"attachment" json:"attachment"`
	OpenWeights      bool `toml:"open_weights" json:"open_weights"`
}

// Cost is per-million-tokens by default. Tiered pricing for very-long
// contexts (typically >200K) goes in TieredOver200K when present.
type Cost struct {
	Input         float64  `toml:"input" json:"input"`
	Output        float64  `toml:"output" json:"output"`
	CacheRead     float64  `toml:"cache_read" json:"cache_read,omitempty"`
	CacheWrite    float64  `toml:"cache_write" json:"cache_write,omitempty"`
	AudioIn       float64  `toml:"audio_in" json:"audio_in,omitempty"`
	AudioOut      float64  `toml:"audio_out" json:"audio_out,omitempty"`
	Reasoning     float64  `toml:"reasoning" json:"reasoning,omitempty"`
	TieredOver200K *Cost `toml:"tiered_over_200k" json:"tiered_over_200k,omitempty"`
}

// Modalities lists the input/output media types a model accepts /
// produces. Strings stay free-form (text, image, audio, video) so
// future modalities don't need a code change.
type Modalities struct {
	Input  []string `toml:"input" json:"input,omitempty"`
	Output []string `toml:"output" json:"output,omitempty"`
}

// Extends is the wrapper-model declaration. `From` is the canonical
// model ID we inherit from. Wrappers typically override only Cost
// (different per-token pricing through OpenRouter/Groq/etc.) and
// optionally Deprecated.
type Extends struct {
	From string `toml:"from" json:"from"`
}

// ErrNotFound is returned by Catalog.Get when no model matches.
var ErrNotFound = errors.New("models: not found")

// Catalog is the in-memory model database. Read-only after Load.
type Catalog struct {
	root    string
	byID    map[string]*Model
	byFamily map[string][]*Model
}

// Root returns the directory the catalog was loaded from.
func (c *Catalog) Root() string { return c.root }

// Get returns a copy of the model with the given ID. Copy semantics
// prevent callers from mutating the catalog's internal state.
func (c *Catalog) Get(id string) (*Model, error) {
	m, ok := c.byID[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	cp := *m
	return &cp, nil
}

// List returns every model, sorted by ID for stable iteration.
func (c *Catalog) List() []*Model {
	out := make([]*Model, 0, len(c.byID))
	for _, m := range c.byID {
		cp := *m
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListFamily returns every model whose Family matches name. Case-
// sensitive (families are canonical short strings like "claude-opus").
func (c *Catalog) ListFamily(name string) []*Model {
	src := c.byFamily[name]
	out := make([]*Model, 0, len(src))
	for _, m := range src {
		cp := *m
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListWithCapability returns every model whose Capabilities set
// contains ALL requested flags as true. Family/tag/etc. filtering can
// be layered on top by callers.
func (c *Catalog) ListWithCapability(req Capabilities) []*Model {
	var out []*Model
	for _, m := range c.byID {
		if req.Reasoning && !m.Capabilities.Reasoning {
			continue
		}
		if req.ToolCall && !m.Capabilities.ToolCall {
			continue
		}
		if req.StructuredOutput && !m.Capabilities.StructuredOutput {
			continue
		}
		if req.Temperature && !m.Capabilities.Temperature {
			continue
		}
		if req.Attachment && !m.Capabilities.Attachment {
			continue
		}
		if req.OpenWeights && !m.Capabilities.OpenWeights {
			continue
		}
		cp := *m
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CheapestInFamily returns the lowest-output-cost model in the family,
// honouring deprecation (skipped). Returns ErrNotFound when no
// non-deprecated models exist in the family.
func (c *Catalog) CheapestInFamily(family string) (*Model, error) {
	members := c.byFamily[family]
	var best *Model
	for _, m := range members {
		if m.Deprecated {
			continue
		}
		if best == nil || m.Cost.Output < best.Cost.Output {
			best = m
		}
	}
	if best == nil {
		return nil, fmt.Errorf("%w: family %s", ErrNotFound, family)
	}
	cp := *best
	return &cp, nil
}

// Load reads every `.toml` file under root, resolves extends
// references, validates, and returns a Catalog. Missing root returns
// (empty Catalog, nil) — running with an empty catalog is supported.
// Parse / validation errors are aggregated and returned as a single
// error wrapping all per-file failures.
func Load(root string) (*Catalog, error) {
	c := &Catalog{
		root:     root,
		byID:     make(map[string]*Model),
		byFamily: make(map[string][]*Model),
	}
	if root == "" {
		return c, nil
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("models: stat root: %w", err)
	}

	// Pass 1: load raw models.
	var pass1Errs []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".toml") {
			return nil
		}
		m, perr := parseModelFile(root, path)
		if perr != nil {
			pass1Errs = append(pass1Errs, fmt.Sprintf("%s: %v", path, perr))
			return nil
		}
		c.byID[m.ID] = m
		return nil
	}); err != nil {
		return nil, fmt.Errorf("models: walk: %w", err)
	}

	// Pass 2: resolve extends.
	for _, m := range c.byID {
		if m.Extends == nil || m.Extends.From == "" {
			continue
		}
		base, ok := c.byID[m.Extends.From]
		if !ok {
			pass1Errs = append(pass1Errs, fmt.Sprintf("%s: extends unknown base %q", m.ID, m.Extends.From))
			continue
		}
		mergeExtends(m, base)
	}

	// Pass 3: validate + index by family.
	for id, m := range c.byID {
		if err := validate(m); err != nil {
			pass1Errs = append(pass1Errs, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		c.byFamily[m.Family] = append(c.byFamily[m.Family], m)
	}

	if len(pass1Errs) > 0 {
		return c, fmt.Errorf("models: %d catalog errors:\n  %s",
			len(pass1Errs), strings.Join(pass1Errs, "\n  "))
	}
	return c, nil
}

// parseModelFile reads one .toml file and derives the ID from the path
// relative to root. `models/openai/gpt-5.toml` → ID `openai/gpt-5`.
func parseModelFile(root, path string) (*Model, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	var m Model
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, fmt.Errorf("rel: %w", err)
	}
	id := strings.TrimSuffix(filepath.ToSlash(rel), ".toml")
	if id == "" {
		return nil, fmt.Errorf("derived empty ID from %s", path)
	}
	m.ID = id
	return &m, nil
}

// mergeExtends copies missing fields from base into m. Caller-declared
// fields on m win — extends is inheritance, not replacement.
func mergeExtends(m, base *Model) {
	if m.Family == "" {
		m.Family = base.Family
	}
	if m.DisplayName == "" {
		m.DisplayName = base.DisplayName
	}
	if m.ContextWindow == 0 {
		m.ContextWindow = base.ContextWindow
	}
	if m.MaxOutputTokens == 0 {
		m.MaxOutputTokens = base.MaxOutputTokens
	}
	// Capabilities: OR each flag — wrappers can DECLARE additional
	// capabilities but cannot disable a base's capabilities (use a
	// separate model file if you need to).
	m.Capabilities.Reasoning = m.Capabilities.Reasoning || base.Capabilities.Reasoning
	m.Capabilities.ToolCall = m.Capabilities.ToolCall || base.Capabilities.ToolCall
	m.Capabilities.StructuredOutput = m.Capabilities.StructuredOutput || base.Capabilities.StructuredOutput
	m.Capabilities.Temperature = m.Capabilities.Temperature || base.Capabilities.Temperature
	m.Capabilities.Attachment = m.Capabilities.Attachment || base.Capabilities.Attachment
	m.Capabilities.OpenWeights = m.Capabilities.OpenWeights || base.Capabilities.OpenWeights
	// Cost: caller-declared values stick; missing zeros inherit.
	if m.Cost.Input == 0 {
		m.Cost.Input = base.Cost.Input
	}
	if m.Cost.Output == 0 {
		m.Cost.Output = base.Cost.Output
	}
	if m.Cost.CacheRead == 0 {
		m.Cost.CacheRead = base.Cost.CacheRead
	}
	if m.Cost.CacheWrite == 0 {
		m.Cost.CacheWrite = base.Cost.CacheWrite
	}
	if m.Cost.AudioIn == 0 {
		m.Cost.AudioIn = base.Cost.AudioIn
	}
	if m.Cost.AudioOut == 0 {
		m.Cost.AudioOut = base.Cost.AudioOut
	}
	if m.Cost.Reasoning == 0 {
		m.Cost.Reasoning = base.Cost.Reasoning
	}
	if m.Cost.TieredOver200K == nil && base.Cost.TieredOver200K != nil {
		t := *base.Cost.TieredOver200K
		m.Cost.TieredOver200K = &t
	}
	if len(m.Modalities.Input) == 0 {
		m.Modalities.Input = append(m.Modalities.Input, base.Modalities.Input...)
	}
	if len(m.Modalities.Output) == 0 {
		m.Modalities.Output = append(m.Modalities.Output, base.Modalities.Output...)
	}
}

// validate enforces the required-field set for a final (post-extends)
// model record.
func validate(m *Model) error {
	if m.Family == "" {
		return fmt.Errorf("family is required")
	}
	if m.ContextWindow <= 0 {
		return fmt.Errorf("context_window must be > 0")
	}
	if m.Cost.Input < 0 || m.Cost.Output < 0 {
		return fmt.Errorf("cost cannot be negative")
	}
	return nil
}
