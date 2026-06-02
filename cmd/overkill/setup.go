package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive provider setup wizard",
	Long: `Walk through selecting and configuring an LLM provider.

Supports 18 built-in providers plus custom OpenAI-compatible endpoints.
Arrow keys to navigate, Enter to select, Ctrl+C to cancel.`,
	RunE: runSetup,
}

const (
	customKey  = "custom"
	customName = "Custom (your own endpoint)"
)

type providerInfo struct {
	Key     string
	Display config.ProviderSetup
	HasKey  bool
}

func runSetup(cmd *cobra.Command, args []string) error {
	wizard := config.NewSetupWizard(cfg)
	all := wizard.AllProviders()

	// Welcome
	fmt.Println()
	fmt.Printf("%s%s╔══════════════════════════════════════════╗%s\n", colorBold, colorGreen, colorReset)
	fmt.Printf("%s%s║       Overkill · Provider Setup           ║%s\n", colorBold, colorGreen, colorReset)
	fmt.Printf("%s%s╚══════════════════════════════════════════╝%s\n", colorBold, colorGreen, colorReset)
	fmt.Println()
	fmt.Println("  I'll help you pick an LLM provider and configure it.")
	fmt.Println("  You only need ONE to get started — you can add more later.")
	fmt.Println()

	// Build provider list with key detection
	infos := make([]providerInfo, 0, len(all)+1)
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
	// Sort: keys first, then alphabetical
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].HasKey != infos[j].HasKey {
			return infos[i].HasKey
		}
		return infos[i].Key < infos[j].Key
	})
	// Add custom option at the end
	infos = append(infos, providerInfo{
		Key: customKey,
		Display: config.ProviderSetup{
			Name: customName,
		},
	})

	// Step 1: Choose provider (arrow keys)
	providerKey, err := arrowSelect("Choose a provider", infos)
	if err != nil {
		fmt.Printf("\n%s✗ setup cancelled%s\n", colorYellow, colorReset)
		return nil
	}

	isCustom := providerKey == customKey
	var ps config.ProviderSetup
	var customName_, customURL string

	if isCustom {
		// Custom provider flow
		fmt.Printf("\n%s▸ Custom provider%s\n", colorBold, colorReset)
		fmt.Println()

		reader := bufio.NewReader(os.Stdin)

		// Name
		fmt.Printf("  Provider name (e.g. 'my-llm'): ")
		raw, err := reader.ReadString('\n')
		if err != nil {
			return nil
		}
		customName_ = strings.TrimSpace(raw)
		if customName_ == "" {
			customName_ = "custom"
		}

		// Endpoint
		fmt.Printf("  Base URL (e.g. 'https://api.openai.com/v1'): ")
		raw, err = reader.ReadString('\n')
		if err != nil {
			return nil
		}
		customURL = strings.TrimSpace(raw)
		if customURL == "" {
			fmt.Printf("\n  %s✗ base URL is required%s\n", colorRed, colorReset)
			return nil
		}
		customURL = strings.TrimSuffix(customURL, "/")

		// API key
		fmt.Printf("  API key (or Enter to skip): ")
		raw, err = reader.ReadString('\n')
		if err != nil {
			return nil
		}
		apiKey := strings.TrimSpace(raw)

		// Fetch models
		fmt.Printf("\n  %sFetching models from %s/v1/models...%s\n", colorDim, customURL, colorReset)
		models, fetchErr := fetchModels(customURL, apiKey)

		var model string
		if fetchErr != nil || len(models) == 0 {
			fmt.Printf("  %s⚠ could not fetch models: %v%s\n", colorYellow, fetchErr, colorReset)
			fmt.Printf("  Enter model IDs (comma-separated): ")
			raw, err = reader.ReadString('\n')
			if err != nil {
				return nil
			}
			modelList := strings.TrimSpace(raw)
			if modelList == "" {
				fmt.Printf("\n  %s✗ at least one model is required%s\n", colorRed, colorReset)
				return nil
			}
			models = strings.Split(modelList, ",")
			for i := range models {
				models[i] = strings.TrimSpace(models[i])
			}
			// Pick first model by default since we asked manually
			model = models[0]
		} else {
			fmt.Printf("  %s✓ found %d models%s\n", colorGreen, len(models), colorReset)
			// Arrow-key model selection
			modelInfos := make([]providerInfo, len(models))
			for i, m := range models {
				modelInfos[i] = providerInfo{
					Key:     m,
					Display: config.ProviderSetup{Name: m},
				}
			}
			model, err = arrowSelect("Choose a model", modelInfos)
			if err != nil {
				return nil
			}
		}

		// Register custom provider in config
		cfg.Providers = append(cfg.Providers, config.ProviderConfig{
			Name:    customName_,
			Type:    "custom",
			BaseURL: customURL,
			APIKey:  apiKey,
			Models:  buildModelConfigsFromStrings(models),
		})
		cfg.Agent.DefaultProvider = customName_
		if model != "" {
			cfg.Agent.DefaultModel = model
		}

		ps = config.ProviderSetup{Name: customName_, DefaultBase: customURL}
		providerKey = customName_
		model = model
	} else {
		// Built-in provider
		ps = all[providerKey]
		wizard.ApplyStep("provider", providerKey)
		fmt.Printf("\n\n  %s✓ %s%s\n", colorGreen, ps.Name, colorReset)

		// Step 2: API key
		if ps.APIKeyEnv != "" {
			envVal := os.Getenv(ps.APIKeyEnv)
			if envVal != "" {
				fmt.Printf("\n%s▸ API Key — using %s%s\n", colorBold, ps.APIKeyEnv, colorReset)
				fmt.Printf("  %s✓ %s=%s***%s (from environment)%s\n", colorGreen, ps.APIKeyEnv, envVal[:4], envVal[len(envVal)-4:], colorReset)
				wizard.ApplyStep("api_key", envVal)
			} else {
				fmt.Printf("\n%s▸ API Key%s\n", colorBold, colorReset)
				fmt.Printf("  Required: %s\n\n", ps.APIKeyEnv)
				fmt.Printf("  API key (or Enter to use env var): ")
				raw, err := bufio.NewReader(os.Stdin).ReadString('\n')
				if err != nil {
					return nil
				}
				key := strings.TrimSpace(raw)
				if key == "" {
					fmt.Printf("  %s⚠ no key — set %s before running%s\n", colorYellow, ps.APIKeyEnv, colorReset)
				}
				wizard.ApplyStep("api_key", key)
			}
		} else {
			wizard.ApplyStep("api_key", "")
		}

		// Step 3: Base URL
		fmt.Printf("\n%s▸ Base URL%s\n", colorBold, colorReset)
		fmt.Printf("  Default: %s%s%s\n\n", colorDim, ps.DefaultBase, colorReset)
		fmt.Printf("  URL (Enter for default): ")
		raw, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return nil
		}
		baseURL := strings.TrimSpace(raw)
		if baseURL == "" {
			baseURL = ps.DefaultBase
		}
		wizard.ApplyStep("base_url", baseURL)
		fmt.Printf("  %s✓ %s%s\n", colorGreen, baseURL, colorReset)

		// Step 4: Model (arrow keys)
		if len(ps.Models) > 1 {
			fmt.Println()
			modelInfos := make([]providerInfo, len(ps.Models))
			for i, m := range ps.Models {
				modelInfos[i] = providerInfo{
					Key:     m,
					Display: config.ProviderSetup{Name: m},
				}
			}
			model, err := arrowSelect("Choose a model", modelInfos)
			if err != nil {
				return nil
			}
			wizard.ApplyStep("model", model)
		} else if len(ps.Models) == 1 {
			wizard.ApplyStep("model", ps.Models[0])
			fmt.Printf("\n  %s✓ model: %s%s\n", colorGreen, ps.Models[0], colorReset)
		}
	}

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
	if isCustom {
		fmt.Printf("  Provider: %s (custom)\n", customName_)
		fmt.Printf("  Endpoint: %s\n", customURL)
	} else {
		fmt.Printf("  Provider: %s\n", ps.Name)
	}
	fmt.Printf("  Config:   %s\n", path)
	fmt.Println()
	fmt.Printf("  Run %soverkill%s to start the agent.\n", colorBold, colorReset)
	fmt.Println()

	return nil
}

// arrowSelect renders a scrollable list with arrow-key navigation.
// The selected item is highlighted purple with a > marker.
func arrowSelect(title string, items []providerInfo) (string, error) {
	if len(items) == 0 {
		return "", fmt.Errorf("no items")
	}

	fmt.Printf("\n%s▸ %s%s  (↑↓ to move, Enter to select)\n\n", colorBold, title, colorReset)

	// Switch terminal to raw mode for arrow keys
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(fd, oldState)

	// Handle Ctrl+C / resize
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-sigCh:
			term.Restore(fd, oldState)
			fmt.Print("\n\r")
			os.Exit(0)
		case <-done:
		}
	}()

	selected := 0
	renderList(items, selected)

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return "", err
		}

		switch {
		case n == 1 && buf[0] == 13: // Enter
			// Clear the list and return
			clearLines(len(items))
			return items[selected].Key, nil

		case n == 1 && buf[0] == 3: // Ctrl+C
			clearLines(len(items))
			return "", fmt.Errorf("cancelled")

		case n == 1 && buf[0] == 'q': // q to quit
			clearLines(len(items))
			return "", fmt.Errorf("cancelled")

		case n == 3 && buf[0] == 27 && buf[1] == 91: // Escape sequences
			switch buf[2] {
			case 'A': // Up
				if selected > 0 {
					selected--
					renderList(items, selected)
				}
			case 'B': // Down
				if selected < len(items)-1 {
					selected++
					renderList(items, selected)
				}
			}
		}
	}
}

func renderList(items []providerInfo, selected int) {
	// Move cursor up to first item
	if selected > 0 {
		fmt.Printf("\033[%dA", selected)
	}

	for i, info := range items {
		// Clear line
		fmt.Print("\033[2K\r")

		marker := " "
		if info.HasKey {
			marker = "🔑"
		}

		if i == selected {
			// Purple highlight + > marker
			name := info.Display.Name
			tag := ""
			if info.Key == "ollama" {
				tag = " (no API key needed)"
			}
			if info.Key == customKey {
				tag = " (bring your own endpoint)"
			}
			fmt.Printf("  %s%s▸ %s%s%s\r\n", colorBold, colorPurple, name, colorReset, tag)
		} else {
			name := info.Display.Name
			tag := ""
			if info.Key == "ollama" {
				tag = " (no API key needed)"
			}
			if info.Key == customKey {
				tag = " (bring your own endpoint)"
			}
			fmt.Printf("    %s %s%s\r\n", marker, name, tag)
		}
	}

	// If we didn't reach the end, move back to selected
	moveBack := len(items) - 1 - selected
	if moveBack > 0 {
		fmt.Printf("\033[%dA", moveBack)
	}
}

func clearLines(count int) {
	for i := 0; i < count; i++ {
		fmt.Print("\033[2K\r")
		if i < count-1 {
			fmt.Print("\033[1B")
		}
	}
	// Move back to top
	if count > 1 {
		fmt.Printf("\033[%dA", count-1)
	}
	fmt.Print("\033[2K\r")
}

// fetchModels tries GET {baseURL}/v1/models and returns model IDs.
func fetchModels(baseURL, apiKey string) ([]string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	url := baseURL + "/v1/models"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	sort.Strings(models)
	return models, nil
}

func buildModelConfigsFromStrings(ids []string) []config.ModelConfig {
	models := make([]config.ModelConfig, 0, len(ids))
	for _, id := range ids {
		models = append(models, config.ModelConfig{ID: id, Name: id})
	}
	return models
}

func init() {
	rootCmd.AddCommand(setupCmd)
}
