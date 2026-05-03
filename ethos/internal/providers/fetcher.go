package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

type ModelFetcher interface {
	Fetch(ctx context.Context, providerType, apiKey, baseURL string) ([]Model, error)
}

type HTTPModelFetcher struct {
	client *http.Client
}

func NewModelFetcher() *HTTPModelFetcher {
	return &HTTPModelFetcher{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type providerEndpoint struct {
	modelsPath string
	authStyle  string
}

var providerEndpoints = map[string]providerEndpoint{
	"openai":      {modelsPath: "/models", authStyle: "bearer"},
	"anthropic":   {modelsPath: "/models", authStyle: "x-api-key"},
	"gemini":      {modelsPath: "", authStyle: "query-key"},
	"deepseek":    {modelsPath: "/models", authStyle: "bearer"},
	"openrouter":  {modelsPath: "/models", authStyle: "bearer"},
	"ollama":      {modelsPath: "/api/tags", authStyle: "none"},
	"groq":        {modelsPath: "/openai/v1/models", authStyle: "bearer"},
	"xai":         {modelsPath: "/v1/models", authStyle: "bearer"},
	"mistral":     {modelsPath: "/v1/models", authStyle: "bearer"},
	"together-ai": {modelsPath: "/api/v1/models", authStyle: "bearer"},
	"perplexity":  {modelsPath: "/v1/models", authStyle: "bearer"},
}

func getEndpoint(providerType string) providerEndpoint {
	if ep, ok := providerEndpoints[providerType]; ok {
		return ep
	}
	return providerEndpoint{modelsPath: "/models", authStyle: "bearer"}
}

func DefaultBaseURL(providerType string) string {
	switch providerType {
	case "openai":
		return "https://api.openai.com/v1"
	case "anthropic":
		return "https://api.anthropic.com/v1"
	case "gemini":
		return "https://generativelanguage.googleapis.com/v1beta"
	case "deepseek":
		return "https://api.deepseek.com/v1"
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "ollama":
		return "http://localhost:11434"
	case "groq":
		return "https://api.groq.com"
	case "xai":
		return "https://api.x.ai"
	case "mistral":
		return "https://api.mistral.ai"
	case "together-ai":
		return "https://api.together.xyz"
	case "perplexity":
		return "https://api.perplexity.ai"
	default:
		return ""
	}
}

type listModelsResponse struct {
	Data []listModelEntry `json:"data"`
}

type listModelEntry struct {
	ID string `json:"id"`
}

type geminiListResponse struct {
	Models []geminiModelEntry `json:"models"`
}

type geminiModelEntry struct {
	Name string `json:"name"`
}

type ollamaListResponse struct {
	Models []ollamaModelEntry `json:"models"`
}

type ollamaModelEntry struct {
	Name string `json:"name"`
}

var nonChatKeywords = []string{"instruct", "embedding", "tts", "whisper", "dall-e", "moderation"}

func (f *HTTPModelFetcher) Fetch(ctx context.Context, providerType, apiKey, baseURL string) ([]Model, error) {
	models, err := f.fetch(ctx, providerType, apiKey, baseURL)
	if err != nil {
		return f.fallback(providerType), nil
	}
	return models, nil
}

func (f *HTTPModelFetcher) fetch(ctx context.Context, providerType, apiKey, baseURL string) ([]Model, error) {
	switch providerType {
	case "ollama":
		return f.fetchOllama(ctx, baseURL)
	case "gemini":
		return f.fetchGemini(ctx, apiKey, baseURL)
	default:
		ep := getEndpoint(providerType)
		useXAPIKey := ep.authStyle == "x-api-key"
		return f.fetchOpenAICompat(ctx, baseURL, apiKey, ep.modelsPath, useXAPIKey)
	}
}

func (f *HTTPModelFetcher) fetchOpenAICompat(ctx context.Context, baseURL, apiKey, path string, useXAPIKey bool) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("providers: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if apiKey != "" {
		if useXAPIKey {
			req.Header.Set("x-api-key", apiKey)
		} else {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("providers: fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("providers: HTTP %d fetching models", resp.StatusCode)
	}

	var result listModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("providers: decode response: %w", err)
	}

	return listToModels(result.Data), nil
}

func (f *HTTPModelFetcher) fetchGemini(ctx context.Context, apiKey, baseURL string) ([]Model, error) {
	url := fmt.Sprintf("%s/models?key=%s", baseURL, apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("providers: create request: %w", err)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("providers: fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("providers: HTTP %d fetching models", resp.StatusCode)
	}

	var result geminiListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("providers: decode response: %w", err)
	}

	models := make([]Model, 0, len(result.Models))
	for _, m := range result.Models {
		id := strings.TrimPrefix(m.Name, "models/")
		if !isChatModel(id) {
			continue
		}
		models = append(models, Model{
			ID:                id,
			Name:              id,
			SupportsTools:     true,
			SupportsStreaming: true,
		})
	}
	return models, nil
}

func (f *HTTPModelFetcher) fetchOllama(ctx context.Context, baseURL string) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("providers: create request: %w", err)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("providers: fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("providers: HTTP %d fetching models", resp.StatusCode)
	}

	var result ollamaListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("providers: decode response: %w", err)
	}

	models := make([]Model, 0, len(result.Models))
	for _, m := range result.Models {
		if !isChatModel(m.Name) {
			continue
		}
		models = append(models, Model{
			ID:                m.Name,
			Name:              m.Name,
			SupportsTools:     true,
			SupportsStreaming: true,
		})
	}
	return models, nil
}

func listToModels(entries []listModelEntry) []Model {
	models := make([]Model, 0, len(entries))
	for _, e := range entries {
		if !isChatModel(e.ID) {
			continue
		}
		models = append(models, Model{
			ID:                e.ID,
			Name:              e.ID,
			SupportsTools:     true,
			SupportsStreaming: true,
		})
	}
	return models
}

func isChatModel(id string) bool {
	lower := strings.ToLower(id)
	for _, kw := range nonChatKeywords {
		if strings.Contains(lower, kw) {
			return false
		}
	}
	return true
}

func (f *HTTPModelFetcher) fallback(providerType string) []Model {
	switch providerType {
	case "openai":
		return OpenAIModels()
	case "anthropic":
		return AnthropicModels()
	case "gemini":
		return GeminiModels()
	case "deepseek":
		return DeepSeekModels()
	case "ollama":
		return OllamaModels()
	case "openrouter":
		return OpenRouterModels()
	case "groq":
		return GroqModels()
	case "xai":
		return XAIModels()
	case "mistral":
		return MistralModels()
	case "together-ai":
		return TogetherAIModels()
	case "perplexity":
		return PerplexityModels()
	case "custom":
		return OpenAIModels()
	default:
		return OpenAIModels()
	}
}

func (f *HTTPModelFetcher) FetchAndPersist(ctx context.Context, providerName, providerType, apiKey, baseURL, outputDir string) ([]Model, int, error) {
	models, err := f.Fetch(ctx, providerType, apiKey, baseURL)
	if err != nil {
		return nil, 0, fmt.Errorf("providers: fetch models for %s: %w", providerName, err)
	}

	modelsDir := filepath.Join(outputDir, providerName, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		return nil, 0, fmt.Errorf("providers: create models dir: %w", err)
	}

	countCreated := 0
	for _, m := range models {
		modelFile := filepath.Join(modelsDir, sanitizeModelFileName(m.ID)+".toml")
		if _, err := os.Stat(modelFile); os.IsNotExist(err) {
			countCreated++
		}

		tomlData, merr := toml.Marshal(modelToTOML(m))
		if merr != nil {
			return nil, 0, fmt.Errorf("providers: marshal model %s: %w", m.ID, merr)
		}

		if wErr := os.WriteFile(modelFile, tomlData, 0o644); wErr != nil {
			return nil, 0, fmt.Errorf("providers: write model %s: %w", m.ID, wErr)
		}
	}

	providerFile := filepath.Join(outputDir, providerName, "provider.toml")
	providerData := providerTOML{
		Name:        providerName,
		Type:        providerType,
		BaseURL:     baseURL,
		LastFetched: time.Now().UTC().Format(time.RFC3339),
		ModelCount:  len(models),
	}
	pData, err := toml.Marshal(providerData)
	if err != nil {
		return nil, 0, fmt.Errorf("providers: marshal provider: %w", err)
	}
	if err := os.WriteFile(providerFile, pData, 0o644); err != nil {
		return nil, 0, fmt.Errorf("providers: write provider: %w", err)
	}

	return models, countCreated, nil
}

func sanitizeModelFileName(id string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return r.Replace(id)
}

type modelTOML struct {
	Name             string            `toml:"name"`
	Family           string            `toml:"family"`
	ReleaseDate      string            `toml:"release_date,omitempty"`
	LastUpdated      string            `toml:"last_updated,omitempty"`
	Attachment       bool              `toml:"attachment"`
	Reasoning        bool              `toml:"reasoning"`
	Temperature      bool              `toml:"temperature"`
	Knowledge        string            `toml:"knowledge,omitempty"`
	ToolCall         bool              `toml:"tool_call"`
	StructuredOutput bool              `toml:"structured_output"`
	OpenWeights      bool              `toml:"open_weights"`
	Cost             modelTOMLCost     `toml:"cost"`
	Limit            modelTOMLLimit    `toml:"limit"`
	Modalities       modelTOMLModalities `toml:"modalities"`
}

type modelTOMLCost struct {
	Input     float64 `toml:"input"`
	Output    float64 `toml:"output"`
	CacheRead float64 `toml:"cache_read"`
}

type modelTOMLLimit struct {
	Context int `toml:"context"`
	Output  int `toml:"output"`
}

type modelTOMLModalities struct {
	Input  []string `toml:"input"`
	Output []string `toml:"output"`
}

type providerTOML struct {
	Name        string `toml:"name"`
	Type        string `toml:"type"`
	BaseURL     string `toml:"base_url"`
	LastFetched string `toml:"last_fetched"`
	ModelCount  int    `toml:"model_count"`
}

func modelToTOML(m Model) modelTOML {
	inputMods := m.InputModalities
	if inputMods == nil {
		inputMods = []string{"text"}
	}
	outputMods := m.OutputModalities
	if outputMods == nil {
		outputMods = []string{"text"}
	}

	if m.SupportsVision {
		inputMods = append(inputMods, "image")
	}

	return modelTOML{
		Name:             m.Name,
		Family:           m.Family,
		Attachment:       m.Attachment,
		Reasoning:        m.Reasoning,
		Temperature:      m.Temperature,
		Knowledge:        m.Knowledge,
		ToolCall:         m.SupportsTools,
		StructuredOutput: m.StructuredOutput,
		OpenWeights:      m.OpenWeights,
		Cost: modelTOMLCost{
			Input:     m.CostIn,
			Output:    m.CostOut,
			CacheRead: m.CostCacheIn,
		},
		Limit: modelTOMLLimit{
			Context: m.ContextWindow,
			Output:  m.DefaultMaxTokens,
		},
		Modalities: modelTOMLModalities{
			Input:  inputMods,
			Output: outputMods,
		},
	}
}
