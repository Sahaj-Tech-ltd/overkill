// Package config — Onboarding wizard with ⭐ recommendations.
//
// Overkill's setup wizard presents available options (providers, gateways,
// TTS engines, databases) with star ratings, descriptions, and secure
// defaults. Designed for both the TUI onboarding pane and API-driven setup.

package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// Rating is a 0–5 star recommendation strength.
type Rating int

const (
	RatingNone     Rating = 0
	RatingOne      Rating = 1
	RatingTwo      Rating = 2
	RatingThree    Rating = 3
	RatingFour     Rating = 4
	RatingFive     Rating = 5
	RatingRecommended Rating = 5
)

func (r Rating) Stars() string {
	switch {
	case r >= RatingFive:
		return "⭐⭐⭐⭐⭐"
	case r == RatingFour:
		return "⭐⭐⭐⭐"
	case r == RatingThree:
		return "⭐⭐⭐"
	case r == RatingTwo:
		return "⭐⭐"
	case r == RatingOne:
		return "⭐"
	default:
		return ""
	}
}

func (r Rating) String() string {
	return r.Stars()
}

// Category groups setup options (provider, gateway, tts, database).
type Category string

const (
	CategoryProvider Category = "provider"
	CategoryGateway  Category = "gateway"
	CategoryTTS      Category = "tts"
	CategoryDatabase Category = "database"
)

// WizardOption is one selectable option in the setup wizard.
// Each option has a rating (⭐ 1–5), a description, and optional
// configuration hints (env var for API key, default base URL, etc).
type WizardOption struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Rating      Rating   `json:"rating"`
	Stars       string   `json:"stars"`       // pre-rendered for TUI
	Category    Category `json:"category"`
	APIKeyEnv   string   `json:"api_key_env,omitempty"`
	DefaultBase string   `json:"default_base,omitempty"`
	Models      []string `json:"models,omitempty"`
	RequiresKey bool     `json:"requires_key"`
	Tags        []string `json:"tags,omitempty"` // "free", "local", "cloud", "recommended"
}

// WizardCatalog is the complete set of setup options with ⭐ ratings.
type WizardCatalog struct {
	Providers []WizardOption `json:"providers"`
	Gateways  []WizardOption `json:"gateways"`
	TTS       []WizardOption `json:"tts"`
	Databases []WizardOption `json:"databases"`
}

// BuildWizardCatalog returns the full catalog of setup options with
// star ratings and descriptions. Ratings reflect Overkill's recommended
// defaults for a new user getting started quickly.
func BuildWizardCatalog() WizardCatalog {
	return WizardCatalog{
		Providers: buildProviderCatalog(),
		Gateways:  buildGatewayCatalog(),
		TTS:       buildTTSCatalog(),
		Databases: buildDatabaseCatalog(),
	}
}

func buildProviderCatalog() []WizardOption {
	return []WizardOption{
		{
			ID:          "deepseek",
			Name:        "DeepSeek",
			Description: "Best cost-to-quality ratio. DeepSeek V4 Pro for complex reasoning, V4 Flash for fast tasks. 1M token context.",
			Rating:      RatingRecommended,
			Category:    CategoryProvider,
			APIKeyEnv:   providers.CanonicalAPIKeyEnv("deepseek"),
			DefaultBase: providers.CanonicalBaseURL("deepseek"),
			Models:      []string{"deepseek-v4-pro", "deepseek-v4-flash", "deepseek-chat"},
			RequiresKey: true,
			Tags:        []string{"recommended", "cloud", "chinese"},
		},
		{
			ID:          "anthropic",
			Name:        "Anthropic Claude",
			Description: "Top-tier reasoning and code generation. Sonnet 4 for daily use, Opus 4 for complex tasks. Excellent tool use.",
			Rating:      RatingFour,
			Category:    CategoryProvider,
			APIKeyEnv:   providers.CanonicalAPIKeyEnv("anthropic"),
			DefaultBase: providers.CanonicalBaseURL("anthropic"),
			Models:      []string{"claude-sonnet-4-20250514", "claude-opus-4-20250514"},
			RequiresKey: true,
			Tags:        []string{"cloud"},
		},
		{
			ID:          "openai",
			Name:        "OpenAI",
			Description: "GPT-4o for general tasks. Reliable but more expensive than DeepSeek. Good ecosystem integration.",
			Rating:      RatingThree,
			Category:    CategoryProvider,
			APIKeyEnv:   providers.CanonicalAPIKeyEnv("openai"),
			DefaultBase: providers.CanonicalBaseURL("openai"),
			Models:      []string{"gpt-4o", "o3-mini"},
			RequiresKey: true,
			Tags:        []string{"cloud"},
		},
		{
			ID:          "ollama",
			Name:        "Ollama (Local)",
			Description: "Run models locally. Zero API costs, full privacy. Needs a GPU for good performance. Best for offline/air-gapped use.",
			Rating:      RatingThree,
			Category:    CategoryProvider,
			APIKeyEnv:   providers.CanonicalAPIKeyEnv("ollama"),
			DefaultBase: providers.CanonicalBaseURL("ollama"),
			Models:      []string{"llama3.1:8b", "codellama:7b", "mistral:7b", "deepseek-r1:8b"},
			RequiresKey: false,
			Tags:        []string{"free", "local", "privacy"},
		},
		{
			ID:          "openrouter",
			Name:        "OpenRouter",
			Description: "Multi-provider gateway. Access Claude, GPT, Gemini and more through one API key. Good for model comparison.",
			Rating:      RatingThree,
			Category:    CategoryProvider,
			APIKeyEnv:   providers.CanonicalAPIKeyEnv("openrouter"),
			DefaultBase: providers.CanonicalBaseURL("openrouter"),
			Models:      []string{"anthropic/claude-sonnet-4", "openai/gpt-4o", "google/gemini-2.5-pro"},
			RequiresKey: true,
			Tags:        []string{"cloud", "multi-provider"},
		},
		{
			ID:          "gemini",
			Name:        "Google Gemini",
			Description: "Gemini 2.5 Pro for long context tasks. Flash variant is fast and cheap. Good vision capabilities.",
			Rating:      RatingTwo,
			Category:    CategoryProvider,
			APIKeyEnv:   providers.CanonicalAPIKeyEnv("gemini"),
			DefaultBase: providers.CanonicalBaseURL("gemini"),
			Models:      []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.0-flash"},
			RequiresKey: true,
			Tags:        []string{"cloud"},
		},
	}
}

func buildGatewayCatalog() []WizardOption {
	return []WizardOption{
		{
			ID:          "telegram",
			Name:        "Telegram",
			Description: "Best messaging integration. Native markdown, voice notes, inline buttons. Fast, reliable, excellent bot API.",
			Rating:      RatingRecommended,
			Category:    CategoryGateway,
			APIKeyEnv:   "TELEGRAM_BOT_TOKEN",
			RequiresKey: true,
			Tags:        []string{"recommended", "messaging", "free"},
		},
		{
			ID:          "discord",
			Name:        "Discord",
			Description: "Good for community bots and servers. Rich embeds, slash commands, voice channels. Needs bot token + app setup.",
			Rating:      RatingThree,
			Category:    CategoryGateway,
			APIKeyEnv:   "DISCORD_BOT_TOKEN",
			RequiresKey: true,
			Tags:        []string{"messaging", "community"},
		},
		{
			ID:          "slack",
			Name:        "Slack",
			Description: "Enterprise-focused. Socket mode for firewalled deployments. Good for team/internal bots.",
			Rating:      RatingTwo,
			Category:    CategoryGateway,
			APIKeyEnv:   "SLACK_BOT_TOKEN",
			RequiresKey: true,
			Tags:        []string{"messaging", "enterprise"},
		},
		{
			ID:          "whatsapp",
			Name:        "WhatsApp",
			Description: "Most users, hardest setup. Needs Meta Business account + phone number. Best reach, worst developer experience.",
			Rating:      RatingTwo,
			Category:    CategoryGateway,
			APIKeyEnv:   "WHATSAPP_CLOUD_API_TOKEN",
			RequiresKey: true,
			Tags:        []string{"messaging", "enterprise", "hard-setup"},
		},
		{
			ID:          "matrix",
			Name:        "Matrix",
			Description: "Decentralized, open protocol. Self-host your own server. Good for privacy-focused setups.",
			Rating:      RatingOne,
			Category:    CategoryGateway,
			APIKeyEnv:   "MATRIX_ACCESS_TOKEN",
			RequiresKey: true,
			Tags:        []string{"messaging", "privacy", "self-hosted"},
		},
	}
}

func buildTTSCatalog() []WizardOption {
	return []WizardOption{
		{
			ID:          "edge",
			Name:        "Edge TTS",
			Description: "Microsoft Edge's free TTS. Natural voices, no API key needed. Best free option for English. ~20 voices available.",
			Rating:      RatingRecommended,
			Category:    CategoryTTS,
			APIKeyEnv:   "",
			RequiresKey: false,
			Tags:        []string{"recommended", "free", "english"},
		},
		{
			ID:          "openai",
			Name:        "OpenAI TTS",
			Description: "High quality voices (Alloy, Echo, Fable, Onyx, Nova, Shimmer). Costs per character. Needs API key.",
			Rating:      RatingThree,
			Category:    CategoryTTS,
			APIKeyEnv:   "OPENAI_API_KEY",
			RequiresKey: true,
			Tags:        []string{"cloud", "paid"},
		},
		{
			ID:          "kittentts",
			Name:        "KittenTTS (Local)",
			Description: "Local neural TTS. 8 expressive voices. ~25MB model, runs on CPU. Zero cost, full privacy.",
			Rating:      RatingTwo,
			Category:    CategoryTTS,
			APIKeyEnv:   "",
			RequiresKey: false,
			Tags:        []string{"local", "free", "privacy"},
		},
		{
			ID:          "elevenlabs",
			Name:        "ElevenLabs",
			Description: "Best quality voices. Voice cloning, emotion control. Expensive per-character pricing. Needs API key.",
			Rating:      RatingTwo,
			Category:    CategoryTTS,
			APIKeyEnv:   "ELEVENLABS_API_KEY",
			RequiresKey: true,
			Tags:        []string{"cloud", "paid", "premium"},
		},
	}
}

func buildDatabaseCatalog() []WizardOption {
	return []WizardOption{
		{
			ID:          "postgres",
			Name:        "PostgreSQL",
			Description: "The goat. Full SQL, JSONB, vector search (pgvector). Set DATABASE_URL env var or database_url in config.",
			Rating:      RatingRecommended,
			Category:    CategoryDatabase,
			APIKeyEnv:   "",
			DefaultBase: "postgres://user:***@localhost:5432/overkill?sslmode=disable",
			RequiresKey: false,
			Tags:        []string{"recommended", "sql", "production"},
		},
	}
}

// Recommend returns the top-rated option for a category.
func (c WizardCatalog) Recommend(cat Category) *WizardOption {
	var options []WizardOption
	switch cat {
	case CategoryProvider:
		options = c.Providers
	case CategoryGateway:
		options = c.Gateways
	case CategoryTTS:
		options = c.TTS
	case CategoryDatabase:
		options = c.Databases
	}

	var best *WizardOption
	for i := range options {
		opt := &options[i]
		if best == nil || opt.Rating > best.Rating {
			best = opt
		}
	}
	return best
}

// Sorted returns options sorted by rating (highest first).
func SortedOptions(options []WizardOption) []WizardOption {
	sorted := make([]WizardOption, len(options))
	copy(sorted, options)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Rating != sorted[j].Rating {
			return sorted[i].Rating > sorted[j].Rating
		}
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

// QuickSetup bundles the recommended defaults for a one-click setup.
type QuickSetup struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Gateway  string `json:"gateway"`
	TTS      string `json:"tts"`
	Database string `json:"database"`
}

// RecommendedQuickSetup returns the one-click recommended setup.
func (c WizardCatalog) RecommendedQuickSetup() QuickSetup {
	qs := QuickSetup{}

	if p := c.Recommend(CategoryProvider); p != nil {
		qs.Provider = p.ID
		if len(p.Models) > 0 {
			qs.Model = p.Models[0]
		}
	}
	if g := c.Recommend(CategoryGateway); g != nil {
		qs.Gateway = g.ID
	}
	if t := c.Recommend(CategoryTTS); t != nil {
		qs.TTS = t.ID
	}
	if d := c.Recommend(CategoryDatabase); d != nil {
		qs.Database = d.ID
	}

	return qs
}

// ApplyQuickSetup applies the recommended defaults to a config.
func (c WizardCatalog) ApplyQuickSetup(cfg *Config) error {
	qs := c.RecommendedQuickSetup()

	if qs.Provider != "" {
		cfg.Agent.DefaultProvider = qs.Provider
	}
	if qs.Model != "" {
		cfg.Agent.DefaultModel = qs.Model
	}
	if qs.Gateway != "" {
		switch qs.Gateway {
		case "telegram":
			cfg.Gateways.Telegram.Enabled = true
		case "discord":
			cfg.Gateways.Discord.Enabled = true
		case "slack":
			cfg.Gateways.Slack.Enabled = true
		case "whatsapp":
			cfg.Gateways.WhatsApp.Enabled = true
		}
	}
	// TTS and Database are set through their own config sections.
	_ = qs.TTS
	_ = qs.Database

	return nil
}

// FormatOption renders a WizardOption for terminal/TUI display.
func FormatOption(opt WizardOption) string {
	var b strings.Builder

	b.WriteString(opt.Stars)
	b.WriteString(" ")
	b.WriteString(opt.Name)

	if len(opt.Tags) > 0 {
		b.WriteString(" [")
		for i, tag := range opt.Tags {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(tag)
		}
		b.WriteString("]")
	}

	b.WriteString("\n   ")
	b.WriteString(opt.Description)

	if opt.RequiresKey && opt.APIKeyEnv != "" {
		b.WriteString(fmt.Sprintf("\n   🔑 Needs: %s", opt.APIKeyEnv))
	} else if !opt.RequiresKey {
		b.WriteString("\n   ✅ No API key needed")
	}

	return b.String()
}
