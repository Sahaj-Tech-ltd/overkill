package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive provider setup wizard",
	Long: `Walk through selecting and configuring an LLM provider.

Supports: openai, anthropic, gemini, deepseek, ollama, openrouter, groq, xai,
mistral, togetherai, perplexity, deepinfra, cerebras, fireworks, bedrock,
vertex, azure, copilot.

Pick your providers at https://models.dev if you need help deciding.`,
	RunE: runSetup,
}

type providerInfo struct {
	Key     string
	Display config.ProviderSetup
	HasKey  bool // env var already set
}

func runSetup(cmd *cobra.Command, args []string) error {
	wizard := config.NewSetupWizard(cfg)
	reader := bufio.NewReader(os.Stdin)

	// Welcome
	fmt.Println()
	fmt.Printf("%s%s╔══════════════════════════════════════════╗%s\n", colorBold, colorGreen, colorReset)
	fmt.Printf("%s%s║       Overkill · Provider Setup           ║%s\n", colorBold, colorGreen, colorReset)
	fmt.Printf("%s%s╚══════════════════════════════════════════╝%s\n", colorBold, colorGreen, colorReset)
	fmt.Println()
	fmt.Println("  I'll help you pick an LLM provider and configure it.")
	fmt.Println("  You only need ONE to get started — you can add more later.")
	fmt.Println()

	// Gather providers with key detection.
	// Iterate the builtin map directly so we have canonical keys.
	all := wizard.AllProviders()
	infos := make([]providerInfo, 0, len(all))
	for key, p := range all {
		info := providerInfo{Key: key, Display: p}
		if p.APIKeyEnv != "" {
			if val := os.Getenv(p.APIKeyEnv); val != "" {
				info.HasKey = true
				info.Display.APIKeyEnv = fmt.Sprintf("%s=%s***%s (detected from env)", p.APIKeyEnv, val[:4], val[len(val)-4:])
			}
		}
		infos = append(infos, info)
	}
	// Put providers with existing keys first
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].HasKey != infos[j].HasKey {
			return infos[i].HasKey
		}
		return infos[i].Display.Name < infos[j].Display.Name
	})

	// Step 1: Choose provider
	fmt.Printf("%s▸ Step 1/4: Choose a provider%s\n", colorBold, colorReset)
	fmt.Println()
	for i, info := range infos {
		models := strings.Join(info.Display.Models, ", ")
		if models == "" {
			models = "configure models manually"
		}
		marker := " "
		if info.HasKey {
			marker = "🔑"
		}
		tag := ""
		if info.Key == "ollama" {
			tag = " (no API key needed)"
		}
		fmt.Printf("  %2d) %s %s%s%s — %s%s\n", i+1, marker, colorBold, info.Display.Name, colorReset, colorDim+models+colorReset, tag)
	}
	fmt.Println()
	if hasAnyKey(infos) {
		fmt.Printf("  %s🔑 = API key already set in environment%s\n", colorDim, colorReset)
		fmt.Println()
	}

	var providerKey string
	for {
		fmt.Printf("  Pick a number or provider name: ")
		raw, err := reader.ReadString('\n')
		if err != nil {
			fmt.Printf("\n%s✗ setup cancelled%s\n", colorYellow, colorReset)
			return nil
		}
		raw = strings.TrimSpace(raw)

		// Try name match against canonical keys
		lower := strings.ToLower(raw)
		if _, ok := all[lower]; ok {
			providerKey = lower
			break
		}

		// Try number
		var idx int
		if _, err := fmt.Sscanf(raw, "%d", &idx); err == nil && idx >= 1 && idx <= len(infos) {
			providerKey = infos[idx-1].Key
			break
		}

		fmt.Printf("  %s✗ invalid — pick a number or provider name%s\n", colorRed, colorReset)
	}

	wizard.ApplyStep("provider", providerKey)
	ps := all[providerKey]
	fmt.Printf("\n  %s✓ %s%s\n", colorGreen, ps.Name, colorReset)

	// Step 2: API key
	if ps.APIKeyEnv != "" {
		// Check if already in env
		envVal := os.Getenv(ps.APIKeyEnv)
		if envVal != "" {
			fmt.Printf("\n%s▸ Step 2/4: API Key — using %s%s\n", colorBold, ps.APIKeyEnv, colorReset)
			fmt.Printf("  %s✓ %s=%s***%s (from environment)%s\n", colorGreen, ps.APIKeyEnv, envVal[:4], envVal[len(envVal)-4:], colorReset)
			wizard.ApplyStep("api_key", envVal)
		} else {
			fmt.Printf("\n%s▸ Step 2/4: API Key%s\n", colorBold, colorReset)
			fmt.Printf("  Required: %s\n", ps.APIKeyEnv)
			fmt.Printf("  (stored in ~/.overkill/config.toml — leave blank to use env var at runtime)\n\n")

			var key string
			for {
				fmt.Printf("  API key (or Enter to use env var): ")
				raw, err := reader.ReadString('\n')
				if err != nil {
					fmt.Printf("\n%s✗ setup cancelled%s\n", colorYellow, colorReset)
					return nil
				}
				key = strings.TrimSpace(raw)
				if key == "" {
					fmt.Printf("  %s⚠ no key — set %s before running%s\n", colorYellow, ps.APIKeyEnv, colorReset)
					break
				}
				if len(key) > 8 {
					break
				}
				fmt.Printf("  %s✗ doesn't look right — enter full key or press Enter to skip%s\n", colorRed, colorReset)
			}
			wizard.ApplyStep("api_key", key)
		}
	} else {
		fmt.Printf("\n%s▸ Step 2/4: API Key — skipped (runs locally)%s\n", colorBold, colorReset)
		wizard.ApplyStep("api_key", "")
	}

	// Step 3: Base URL
	fmt.Printf("\n%s▸ Step 3/4: Base URL%s\n", colorBold, colorReset)
	fmt.Printf("  Default: %s%s%s\n\n", colorDim, ps.DefaultBase, colorReset)
	fmt.Printf("  URL (or Enter for default): ")
	raw, err := reader.ReadString('\n')
	if err != nil {
		fmt.Printf("\n%s✗ setup cancelled%s\n", colorYellow, colorReset)
		return nil
	}
	baseURL := strings.TrimSpace(raw)
	if baseURL == "" {
		baseURL = ps.DefaultBase
	}
	wizard.ApplyStep("base_url", baseURL)
	fmt.Printf("  %s✓ %s%s\n", colorGreen, baseURL, colorReset)

	// Step 4: Model
	fmt.Printf("\n%s▸ Step 4/4: Choose a model%s\n", colorBold, colorReset)
	fmt.Println()
	for i, m := range ps.Models {
		fmt.Printf("  %2d) %s%s%s\n", i+1, colorBold, m, colorReset)
	}
	fmt.Println()

	var model string
	for {
		fmt.Printf("  Pick a number or model name (default: %s): ", ps.Models[0])
		raw, err := reader.ReadString('\n')
		if err != nil {
			// Ctrl+D — use default
			model = ps.Models[0]
			break
		}
		raw = strings.TrimSpace(raw)

		if raw == "" {
			model = ps.Models[0]
			break
		}

		for _, m := range ps.Models {
			if strings.EqualFold(raw, m) {
				model = m
				break
			}
		}
		if model != "" {
			break
		}

		var idx int
		if _, err := fmt.Sscanf(raw, "%d", &idx); err == nil && idx >= 1 && idx <= len(ps.Models) {
			model = ps.Models[idx-1]
			break
		}

		fmt.Printf("  %s✗ invalid — pick a number or model name%s\n", colorRed, colorReset)
	}
	wizard.ApplyStep("model", model)
	fmt.Printf("  %s✓ %s%s\n", colorGreen, model, colorReset)

	// Save
	path := resolvedCfgPath
	if path == "" {
		p, err := config.ConfigPath()
		if err != nil {
			return fmt.Errorf("config path: %w", err)
		}
		path = p
	}

	if err := cfg.Save(path); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Println()
	fmt.Printf("  %s%s✓ Setup complete!%s\n", colorBold, colorGreen, colorReset)
	fmt.Println()
	fmt.Printf("  Provider: %s\n", ps.Name)
	fmt.Printf("  Model:    %s\n", model)
	fmt.Printf("  Config:   %s\n", path)
	fmt.Println()
	fmt.Printf("  Run %soverkill%s to start the agent.\n", colorBold, colorReset)
	fmt.Printf("  Run %soverkill doctor%s to verify everything works.\n", colorBold, colorReset)
	fmt.Println()

	return nil
}

func hasAnyKey(infos []providerInfo) bool {
	for _, info := range infos {
		if info.HasKey {
			return true
		}
	}
	return false
}

func init() {
	rootCmd.AddCommand(setupCmd)
}
