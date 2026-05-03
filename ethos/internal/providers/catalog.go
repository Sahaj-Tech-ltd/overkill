package providers

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

type TOMLModel struct {
	Name             string                `toml:"name"`
	Family           string                `toml:"family"`
	MaxTokens        int                   `toml:"max_tokens"`
	Reasoning        bool                  `toml:"reasoning"`
	ToolCall         bool                  `toml:"tool_call"`
	StructuredOutput bool                  `toml:"structured_output"`
	Temperature      bool                  `toml:"temperature"`
	Attachment       bool                  `toml:"attachment"`
	OpenWeights      bool                  `toml:"open_weights"`
	Modalities       TOMLModalities        `toml:"modalities"`
	Cost             TOMLCost              `toml:"cost"`
	Extends          *TOMLExtends          `toml:"extends"`
	ReleaseDate      string                `toml:"release_date"`
	LastUpdated      string                `toml:"last_updated"`
	Knowledge        string                `toml:"knowledge"`
	Status           string                `toml:"status"`
	Interleaved      any                   `toml:"interleaved"`
	Limit            TOMLLimit             `toml:"limit"`
	Experimental     *TOMLExperimental     `toml:"experimental,omitempty"`
	ProviderOverride *TOMLProviderOverride `toml:"provider,omitempty"`
}

type TOMLModalities struct {
	Input  []string `toml:"input"`
	Output []string `toml:"output"`
}

type TOMLCost struct {
	Input      float64 `toml:"input"`
	Output     float64 `toml:"output"`
	CacheRead  float64 `toml:"cache_read"`
	CacheWrite float64 `toml:"cache_write"`
}

type TOMLLimit struct {
	Context int `toml:"context"`
	Output  int `toml:"output"`
	Input   int `toml:"input"`
}

type TOMLExtends struct {
	From string `toml:"from"`
}

type TOMLExperimental struct {
	Modes map[string]TOMLExperimentalMode `toml:"modes"`
}

type TOMLExperimentalMode struct {
	Cost     TOMLCost              `toml:"cost"`
	Provider *TOMLProviderOverride `toml:"provider,omitempty"`
}

type TOMLProviderOverride struct {
	NPM     string            `toml:"npm"`
	API     string            `toml:"api"`
	Body    map[string]any    `toml:"body"`
	Headers map[string]string `toml:"headers"`
	Shape   string            `toml:"shape"`
}

type ModelCatalog struct {
	models map[string]*TOMLModel
}

func LoadCatalog(dir string) (*ModelCatalog, error) {
	mc := &ModelCatalog{
		models: make(map[string]*TOMLModel),
	}

	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return mc, nil
		}
		return nil, fmt.Errorf("stat catalog dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return mc, nil
	}

	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".toml" {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "catalog: skipping %s: %v\n", path, readErr)
			return nil
		}

		var m TOMLModel
		if decodeErr := toml.Unmarshal(data, &m); decodeErr != nil {
			fmt.Fprintf(os.Stderr, "catalog: skipping %s: %v\n", path, decodeErr)
			return nil
		}

		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		id := strings.TrimSuffix(rel, filepath.Ext(rel))
		id = filepath.ToSlash(id)
		id = strings.Replace(id, "models/", "", 1)

		mc.models[id] = &m
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk catalog dir %s: %w", dir, err)
	}

	return mc, nil
}

func (mc *ModelCatalog) Get(id string) (*Model, error) {
	visited := make(map[string]bool)
	return mc.resolve(id, visited)
}

func (mc *ModelCatalog) resolve(id string, visited map[string]bool) (*Model, error) {
	if visited[id] {
		return nil, fmt.Errorf("circular extends detected: %s", id)
	}
	visited[id] = true

	raw, ok := mc.models[id]
	if !ok {
		return nil, fmt.Errorf("model not found: %s", id)
	}

	resolved := *raw

	if raw.Extends != nil && raw.Extends.From != "" {
		parent, err := mc.resolve(raw.Extends.From, visited)
		if err != nil {
			return nil, fmt.Errorf("resolve extends from %s for %s: %w", raw.Extends.From, id, err)
		}

		resolved = mc.merge(parent, raw)
	}

	return mc.toModel(id, &resolved), nil
}

func (mc *ModelCatalog) merge(parent *Model, child *TOMLModel) TOMLModel {
	result := TOMLModel{
		Name:             parent.Name,
		Family:           parent.Family,
		MaxTokens:        parent.MaxTokens,
		Reasoning:        parent.Reasoning,
		ToolCall:         parent.SupportsTools,
		StructuredOutput: parent.StructuredOutput,
		Temperature:      parent.Temperature,
		Attachment:       parent.Attachment,
		OpenWeights:      parent.OpenWeights,
		ReleaseDate:      parent.ReleaseDate,
		LastUpdated:      parent.LastUpdated,
		Knowledge:        parent.Knowledge,
		Status:           parent.Status,
		Cost: TOMLCost{
			Input:      parent.CostIn,
			Output:     parent.CostOut,
			CacheRead:  parent.CostCacheIn,
			CacheWrite: parent.CostCacheOut,
		},
		Limit: TOMLLimit{
			Context: parent.ContextWindow,
			Output:  parent.DefaultMaxTokens,
		},
	}

	if len(parent.InputModalities) > 0 {
		result.Modalities.Input = make([]string, len(parent.InputModalities))
		copy(result.Modalities.Input, parent.InputModalities)
	}
	if len(parent.OutputModalities) > 0 {
		result.Modalities.Output = make([]string, len(parent.OutputModalities))
		copy(result.Modalities.Output, parent.OutputModalities)
	}

	if child.Name != "" {
		result.Name = child.Name
	}
	if child.Family != "" {
		result.Family = child.Family
	}
	if child.MaxTokens != 0 {
		result.MaxTokens = child.MaxTokens
	}
	if child.Reasoning {
		result.Reasoning = child.Reasoning
	}
	if child.ToolCall {
		result.ToolCall = child.ToolCall
	}
	if child.StructuredOutput {
		result.StructuredOutput = child.StructuredOutput
	}
	if child.Temperature {
		result.Temperature = child.Temperature
	}
	if child.Attachment {
		result.Attachment = child.Attachment
	}
	if child.OpenWeights {
		result.OpenWeights = child.OpenWeights
	}

	if child.ReleaseDate != "" {
		result.ReleaseDate = child.ReleaseDate
	}
	if child.LastUpdated != "" {
		result.LastUpdated = child.LastUpdated
	}
	if child.Knowledge != "" {
		result.Knowledge = child.Knowledge
	}
	if child.Status != "" {
		result.Status = child.Status
	}
	if child.Interleaved != nil {
		result.Interleaved = child.Interleaved
	}

	if child.Cost.Input != 0 {
		result.Cost.Input = child.Cost.Input
	}
	if child.Cost.Output != 0 {
		result.Cost.Output = child.Cost.Output
	}
	if child.Cost.CacheRead != 0 {
		result.Cost.CacheRead = child.Cost.CacheRead
	}
	if child.Cost.CacheWrite != 0 {
		result.Cost.CacheWrite = child.Cost.CacheWrite
	}

	if child.Limit.Context != 0 {
		result.Limit.Context = child.Limit.Context
	}
	if child.Limit.Output != 0 {
		result.Limit.Output = child.Limit.Output
	}
	if child.Limit.Input != 0 {
		result.Limit.Input = child.Limit.Input
	}

	if child.Modalities.Input != nil {
		result.Modalities.Input = child.Modalities.Input
	}
	if child.Modalities.Output != nil {
		result.Modalities.Output = child.Modalities.Output
	}

	if child.Experimental != nil {
		result.Experimental = child.Experimental
	}
	if child.ProviderOverride != nil {
		result.ProviderOverride = child.ProviderOverride
	}

	return result
}

func (mc *ModelCatalog) toModel(id string, t *TOMLModel) *Model {
	contextWindow := t.Limit.Context
	if contextWindow == 0 {
		contextWindow = t.MaxTokens
	}
	defaultMaxTokens := t.Limit.Output

	m := &Model{
		ID:               id,
		Name:             t.Name,
		Family:           t.Family,
		MaxTokens:        t.MaxTokens,
		ContextWindow:    contextWindow,
		DefaultMaxTokens: defaultMaxTokens,
		SupportsTools:    t.ToolCall,
		Reasoning:        t.Reasoning,
		StructuredOutput: t.StructuredOutput,
		Temperature:      t.Temperature,
		Attachment:       t.Attachment,
		OpenWeights:      t.OpenWeights,
		CostIn:           t.Cost.Input,
		CostOut:          t.Cost.Output,
		CostCacheIn:      t.Cost.CacheRead,
		CostCacheOut:     t.Cost.CacheWrite,
		InputModalities:  t.Modalities.Input,
		OutputModalities: t.Modalities.Output,
		ReleaseDate:      t.ReleaseDate,
		LastUpdated:      t.LastUpdated,
		Knowledge:        t.Knowledge,
		Status:           t.Status,
	}

	if t.Modalities.Input != nil {
		for _, mod := range t.Modalities.Input {
			if mod == "image" {
				m.SupportsVision = true
				break
			}
		}
	}

	return m
}

func (mc *ModelCatalog) List() []string {
	ids := make([]string, 0, len(mc.models))
	for id := range mc.models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (mc *ModelCatalog) ByFamily(family string) []*Model {
	var result []*Model
	lower := strings.ToLower(family)
	for id, m := range mc.models {
		if strings.ToLower(m.Family) == lower {
			model, err := mc.Get(id)
			if err != nil {
				continue
			}
			result = append(result, model)
		}
	}
	return result
}

func (mc *ModelCatalog) ByCapability(requiresToolCall bool, requiresVision bool) []*Model {
	var result []*Model
	for id := range mc.models {
		model, err := mc.Get(id)
		if err != nil {
			continue
		}
		if requiresToolCall && !model.SupportsTools {
			continue
		}
		if requiresVision && !model.SupportsVision {
			continue
		}
		result = append(result, model)
	}
	return result
}
