package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive provider setup wizard",
	Long: `Walk through selecting and configuring an LLM provider.

Supports: openai, anthropic, gemini, deepseek, ollama, openrouter.

Pick your providers at https://models.dev if you need help deciding.`,
	RunE: runSetup,
}

func runSetup(cmd *cobra.Command, args []string) error {
	wizard := config.NewSetupWizard(cfg)
	reader := bufio.NewReader(os.Stdin)

	fmt.Println()
	fmt.Printf("%s%s╔══════════════════════════════════════╗%s\n", colorBold, colorGreen, colorReset)
	fmt.Printf("%s%s║    Ethos Provider Setup Wizard       ║%s\n", colorBold, colorGreen, colorReset)
	fmt.Printf("%s%s╚══════════════════════════════════════╝%s\n", colorBold, colorGreen, colorReset)
	fmt.Println()

	// Step 1: Choose provider
	fmt.Printf("%sStep 1/4: Choose a provider%s\n", colorBold, colorReset)
	providers := wizard.AvailableProviders()
	for i, p := range providers {
		models := strings.Join(p.Models, ", ")
		if p.Name == "Ollama" {
			fmt.Printf("  %d) %s%s%s — %s (no API key needed)\n", i+1, colorBold, p.Name, colorReset, models)
		} else {
			fmt.Printf("  %d) %s%s%s — %s\n", i+1, colorBold, p.Name, colorReset, models)
		}
	}
	fmt.Println()

	var providerKey string
	for {
		fmt.Printf("Pick a number or provider name: ")
		raw, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading input: %w", err)
		}
		raw = strings.TrimSpace(raw)

		// Try name match first
		lower := strings.ToLower(raw)
		if _, ok := wizard.AvailableProvidersByName()[lower]; ok {
			providerKey = lower
			break
		}

		// Try number
		var idx int
		if _, err := fmt.Sscanf(raw, "%d", &idx); err == nil && idx >= 1 && idx <= len(providers) {
			providerKey = strings.ToLower(providers[idx-1].Name)
			if providerKey == "google gemini" {
				providerKey = "gemini"
			}
			break
		}

		fmt.Printf("%s✗ invalid choice — pick a number or provider name%s\n", colorRed, colorReset)
	}

	wizard.ApplyStep("provider", providerKey)

	ps, ok := wizard.AvailableProvidersByName()[providerKey]
	if !ok {
		ps = wizard.AvailableProvidersByName()["openai"]
	}

	// Step 2: API key (skip for Ollama)
	if ps.APIKeyEnv != "" {
		fmt.Printf("\n%sStep 2/4: API Key (%s)%s\n", colorBold, ps.APIKeyEnv, colorReset)
		fmt.Printf("The key will be stored in %s. Set it blank to use env var at runtime.\n", resolvedCfgPath)

		var key string
		for {
			fmt.Printf("API key (or press Enter to use env var): ")
			raw, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("reading input: %w", err)
			}
			key = strings.TrimSpace(raw)
			if key == "" || strings.HasPrefix(key, "sk-") || strings.HasPrefix(key, "sk-ant-") || strings.HasPrefix(key, "AIza") || len(key) > 10 {
				break
			}
			fmt.Printf("%s✗ doesn't look like a valid key — type it or press Enter to skip%s\n", colorRed, colorReset)
		}

		wizard.ApplyStep("api_key", key)
	} else {
		fmt.Printf("\n%sStep 2/4: API Key — skipped (Ollama runs locally)%s\n", colorBold, colorReset)
		wizard.ApplyStep("api_key", "")
	}

	// Step 3: Base URL
	fmt.Printf("\n%sStep 3/4: Base URL%s\n", colorBold, colorReset)
	fmt.Printf("Default: %s%s%s\n", colorDim, ps.DefaultBase, colorReset)
	fmt.Printf("Base URL (or press Enter for default): ")
	raw, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("reading input: %w", err)
	}
	baseURL := strings.TrimSpace(raw)
	if baseURL == "" {
		baseURL = ps.DefaultBase
	}
	wizard.ApplyStep("base_url", baseURL)

	// Step 4: Model
	fmt.Printf("\n%sStep 4/4: Choose a model%s\n", colorBold, colorReset)
	for i, m := range ps.Models {
		fmt.Printf("  %d) %s%s%s\n", i+1, colorBold, m, colorReset)
	}
	fmt.Println()

	var model string
	for {
		fmt.Printf("Pick a number or model name (default: %s): ", ps.Models[0])
		raw, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading input: %w", err)
		}
		raw = strings.TrimSpace(raw)

		if raw == "" {
			model = ps.Models[0]
			break
		}

		// Try name match
		for _, m := range ps.Models {
			if strings.EqualFold(raw, m) {
				model = m
				break
			}
		}
		if model != "" {
			break
		}

		// Try number
		var idx int
		if _, err := fmt.Sscanf(raw, "%d", &idx); err == nil && idx >= 1 && idx <= len(ps.Models) {
			model = ps.Models[idx-1]
			break
		}

		fmt.Printf("%s✗ invalid choice — pick a number or model name%s\n", colorRed, colorReset)
	}

	wizard.ApplyStep("model", model)

	// Save
	path := resolvedCfgPath
	if path == "" {
		p, err := config.ConfigPath()
		if err != nil {
			return fmt.Errorf("resolving config path: %w", err)
		}
		path = p
	}

	if err := cfg.Save(path); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Println()
	fmt.Printf("%s%s✓ Setup complete!%s\n", colorBold, colorGreen, colorReset)
	fmt.Printf("Provider: %s\n", providerKey)
	fmt.Printf("Model:    %s\n", model)
	fmt.Printf("Config:   %s\n", path)
	fmt.Println()
	fmt.Printf("Run %soverkill run%s to start the agent, or %soverkill doctor%s to verify.\n", colorBold, colorReset, colorBold, colorReset)

	return nil
}

func init() {
	configCmd.AddCommand(setupCmd)
}
