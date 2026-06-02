package main

import (
	"bufio"
	"context"
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
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive provider setup wizard",
	Long: `Walk through selecting and configuring an LLM provider.

Supports 18 built-in providers plus custom OpenAI-compatible endpoints.
Arrow keys to navigate, Enter to select.
Press Esc or Ctrl+C twice to cancel (prevents accidental exit).`,
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

	// Fetch live model catalog from models.dev (uses disk cache, fast).
	// We use this to show current models instead of the baked-in list.
	fmt.Printf("  %sFetching latest model catalog from models.dev...%s", colorDim, colorReset)
	catalog, catalogErr := providers.FetchCatalog(context.Background())
	if catalogErr != nil {
		fmt.Printf("\r  %s⚠ models.dev unreachable — using cached model list%s\n", colorYellow, colorReset)
	} else {
		fmt.Printf("\r  %s✓ model catalog loaded (%d providers)%s\n", colorGreen, len(catalog.Providers()), colorReset)
	}

	// Welcome
	fmt.Println()
	fmt.Printf("%s%s╔══════════════════════════════════════════╗%s\n", colorBold, colorGreen, colorReset)
	fmt.Printf("%s%s║       Overkill · Provider Setup           ║%s\n", colorBold, colorGreen, colorReset)
	fmt.Printf("%s%s╚══════════════════════════════════════════╝%s\n", colorBold, colorGreen, colorReset)
	fmt.Println()
	fmt.Println("  I'll help you pick an LLM provider and configure it.")
	fmt.Println("  You only need ONE to get started — you can add more later.")
	fmt.Println()

	// Build provider list with key detection and live model catalog
	infos := make([]providerInfo, 0, len(all)+1)
	for key, p := range all {
		info := providerInfo{Key: key, Display: p}

		// Use live catalog models if available, fall back to hardcoded.
		if catalog != nil {
			if catID, ok := config.ProviderToCatalogID(key); ok {
				if catModels := catalog.Models(catID); len(catModels) > 0 {
					// Limit to top 15 models to keep the list manageable
					top := catModels
					if len(top) > 15 {
						top = top[:15]
					}
					ids := make([]string, len(top))
					for i, m := range top {
						ids[i] = m.ID
					}
					info.Display.Models = ids
				}
			}
		}

		if p.APIKeyEnv != "" {
			if val := os.Getenv(p.APIKeyEnv); val != "" {
				info.HasKey = true
				info.Display.APIKeyEnv = fmt.Sprintf("%s=%s (detected from env)", p.APIKeyEnv, config.MaskKey(val))
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
		// Use catalog-enriched models from the selection list, not stale hardcoded.
		for _, info := range infos {
			if info.Key == providerKey && len(info.Display.Models) > 0 {
				ps.Models = info.Display.Models
				break
			}
		}
		wizard.ApplyStep("provider", providerKey)
		fmt.Printf("\n\n  %s✓ %s%s\n", colorGreen, ps.Name, colorReset)

		var apiKey string

		// Step 2: API key
		if ps.APIKeyEnv != "" {
			envVal := os.Getenv(ps.APIKeyEnv)
			if envVal != "" {
				fmt.Printf("\n%s▸ API Key — using %s%s\n", colorBold, ps.APIKeyEnv, colorReset)
				fmt.Printf("  %s✓ %s=%s (from environment)%s\n", colorGreen, ps.APIKeyEnv, config.MaskKey(envVal), colorReset)
				apiKey = envVal
				wizard.ApplyStep("api_key", envVal)
			} else {
				fmt.Printf("\n%s▸ API Key%s\n", colorBold, colorReset)
				fmt.Printf("  Required: %s\n\n", ps.APIKeyEnv)
				fmt.Printf("  API key (or Enter to use env var): ")
				raw, err := bufio.NewReader(os.Stdin).ReadString('\n')
				if err != nil {
					return nil
				}
				apiKey = strings.TrimSpace(raw)
				if apiKey == "" {
					fmt.Printf("  %s⚠ no key — set %s before running%s\n", colorYellow, ps.APIKeyEnv, colorReset)
				}
				wizard.ApplyStep("api_key", apiKey)
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

		// Step 4: Model — try live API first, fall back to catalog
		fmt.Printf("\n%s▸ Models%s\n", colorBold, colorReset)
		var models []string
		if apiKey != "" && baseURL != "" {
			fmt.Printf("  %sFetching live from %s/v1/models...%s\n", colorDim, baseURL, colorReset)
			models, _ = fetchModels(baseURL, apiKey)
		}
		if len(models) > 0 {
			fmt.Printf("  %s✓ found %d models from API%s\n", colorGreen, len(models), colorReset)
		} else {
			if len(ps.Models) > 0 {
				models = ps.Models
				fmt.Printf("  %susing %d models from catalog%s\n", colorDim, len(models), colorReset)
			}
		}

		if len(models) > 1 {
			fmt.Println()
			modelInfos := make([]providerInfo, len(models))
			for i, m := range models {
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
		} else if len(models) == 1 {
			wizard.ApplyStep("model", models[0])
			fmt.Printf("\n  %s✓ model: %s%s\n", colorGreen, models[0], colorReset)
		} else {
			fmt.Printf("  %s⚠ no models available%s\n", colorYellow, colorReset)
			fmt.Printf("  Enter model ID manually: ")
			raw, err := bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil {
				return nil
			}
			model := strings.TrimSpace(raw)
			if model == "" {
				return nil
			}
			wizard.ApplyStep("model", model)
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
	fmt.Printf("  %s%s✓ Setup complete! Starting Overkill...%s\n", colorBold, colorGreen, colorReset)
	fmt.Println()

	return runAgent(cmd, args)
}

// arrowSelect renders a scrollable list with arrow-key navigation.
// Selected item is purple with ▸ marker. Esc/Ctrl+C need two presses within
// 2 seconds to cancel (matching OpenCode/Hermes behaviour).
func arrowSelect(title string, items []providerInfo) (string, error) {
	if len(items) == 0 {
		return "", fmt.Errorf("no items")
	}

	fmt.Printf("\n%s▸ %s%s  (↑↓ to move, Enter to select, Esc×2 to cancel)\n\n", colorBold, title, colorReset)

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(fd, oldState)

	// OS signal handler
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
	pendingCancel := false
	var cancelTimer *time.Timer
	cancelTimerCh := make(chan struct{})

	clearCancel := func() {
		if cancelTimer != nil {
			cancelTimer.Stop()
		}
		select {
		case <-cancelTimerCh:
		default:
		}
		pendingCancel = false
		// Clear popup below list
		fmt.Printf("\033[%dB", len(items))
		fmt.Print("\033[2K\r")
		fmt.Printf("\033[%dA", len(items))
	}

	renderList(items, selected)

	oneByte := make([]byte, 1)
	for {
		_, err := os.Stdin.Read(oneByte)
		if err != nil {
			return "", err
		}
		b := oneByte[0]

		switch {
		case b == 13: // Enter
			if pendingCancel {
				clearCancel()
				renderList(items, selected)
				continue
			}
			clearLines(len(items))
			return items[selected].Key, nil

		case b == 27: // Escape — could be standalone (1 byte) or arrow key prefix (3 bytes)
			// In raw mode, arrow keys arrive as ESC [ letter. Standalone Esc
			// is just ESC. To distinguish: do a non-blocking read for the next
			// 2 bytes. If nothing arrives within a few ms, it's standalone Esc.
			rest := make([]byte, 2)
			syscall.SetNonblock(fd, true)
			n, _ := syscall.Read(fd, rest)
			syscall.SetNonblock(fd, false)
			if n <= 0 {
				// Standalone Esc — no more bytes in buffer
				if pendingCancel {
					clearLines(len(items))
					return "", fmt.Errorf("cancelled")
				}
				pendingCancel = true
				cancelTimer = time.AfterFunc(2*time.Second, func() {
					cancelTimerCh <- struct{}{}
					clearCancel()
					renderList(items, selected)
				})
				fmt.Printf("\033[%dB", len(items))
				fmt.Printf("\033[2K\r  %sEsc — press again to cancel (or wait 2s)%s", colorDim, colorReset)
				fmt.Printf("\033[%dA", len(items))
				continue
			}

			// Arrow key — got at least 1 more byte
			if pendingCancel {
				clearCancel()
				renderList(items, selected)
			}
			if rest[0] == 91 { // '[' — confirms ESC[ sequence
				if n >= 2 {
					switch rest[1] {
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

		case b == 3: // Ctrl+C
			if pendingCancel {
				clearLines(len(items))
				return "", fmt.Errorf("cancelled")
			}
			pendingCancel = true
			cancelTimer = time.AfterFunc(2*time.Second, func() {
				cancelTimerCh <- struct{}{}
				clearCancel()
				renderList(items, selected)
			})
			fmt.Printf("\033[%dB", len(items))
			fmt.Printf("\033[2K\r  %sCtrl+C — press again to cancel (or wait 2s)%s", colorDim, colorReset)
			fmt.Printf("\033[%dA", len(items))

		case b == 'q': // q to quit
			if pendingCancel {
				clearCancel()
				renderList(items, selected)
				continue
			}
			clearLines(len(items))
			return "", fmt.Errorf("cancelled")

		default:
			if pendingCancel {
				clearCancel()
				renderList(items, selected)
			}
		}
	}
}

func renderList(items []providerInfo, selected int) {
	// Clear the full list area by moving up and clearing each line.
	// First call has nothing above it, but the escape is harmless.
	for range items {
		fmt.Print("\033[2K") // clear line
		fmt.Print("\033[1A") // move up
	}
	fmt.Print("\033[2K") // clear the title line too

	// Reprint title
	fmt.Printf("\r%s▸ %s%s  (↑↓ to move, Enter to select, Esc×2 to cancel)\n\n", colorBold, "", colorReset)

	for i, info := range items {
		if i == selected {
			name := info.Display.Name
			tag := ""
			if info.Key == "ollama" {
				tag = " (no API key needed)"
			}
			if info.Key == customKey {
				tag = " (bring your own endpoint)"
			}
			fmt.Printf("  %s%s▸ %s%s%s\n", colorBold, colorPurple, name, colorReset, tag)
		} else {
			name := info.Display.Name
			marker := " "
			if info.HasKey {
				marker = "🔑"
			}
			tag := ""
			if info.Key == "ollama" {
				tag = " (no API key needed)"
			}
			if info.Key == customKey {
				tag = " (bring your own endpoint)"
			}
			fmt.Printf("    %s %s%s\n", marker, name, tag)
		}
	}
	// Cursor is now after the last item — no terminal gymnastics needed.
}

func clearLines(count int) {
	for i := 0; i < count; i++ {
		fmt.Print("\033[2K\r")
		if i < count-1 {
			fmt.Print("\033[1B")
		}
	}
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
