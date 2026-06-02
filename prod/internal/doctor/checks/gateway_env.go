package checks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Sahaj-Tech-ltd/overkill/internal/doctor"
)

// RegisterGatewayEnv adds checks for gateway bot tokens and supporting
// environment that the original doctor sweep never covered.
func RegisterGatewayEnv(r *doctor.Runner, d Deps) {
	// ANTHROPIC_API_KEY for vision (separate from provider config).
	r.Register(doctor.SubsystemCheck{
		ID:       "env.anthropic_key",
		Name:     "Anthropic vision API key",
		Category: doctor.CatProvider,
		Fn: func(ctx context.Context) doctor.Result {
			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				return skip("ANTHROPIC_API_KEY not set — vision features use provider config")
			}
			return okf("ANTHROPIC_API_KEY set")
		},
	})

	// Browser tool dependencies.
	r.Register(doctor.SubsystemCheck{
		ID:       "tools.browser_deps",
		Name:     "Browser tool dependencies",
		Category: doctor.CatBackend,
		Fn: func(ctx context.Context) doctor.Result {
			missing := []string{}
			for _, dep := range []string{"chromium", "chromium-browser", "google-chrome"} {
				if _, err := exec.LookPath(dep); err == nil {
					return okf("browser found: %s", dep)
				}
				missing = append(missing, dep)
			}
			return skip("no browser found on PATH (chromium / google-chrome); browser tools disabled")
		},
	})

	// Overkill data directories.
	configDir := d.ConfigDir
	if configDir == "" {
		configDir = os.Getenv("HOME") + "/.overkill"
	}
	dirs := []struct {
		id, name, path string
	}{
		{"fs.checkpoints", "Checkpoints directory", filepath.Join(configDir, "checkpoints")},
		{"fs.tags", "Tags directory", filepath.Join(configDir, "tags")},
		{"fs.plans", "Plans directory", filepath.Join(configDir, "plans")},
	}
	for _, dir := range dirs {
		dir := dir
		r.Register(doctor.SubsystemCheck{
			ID:       dir.id,
			Name:     dir.name,
			Category: doctor.CatStorage,
			Fn: func(ctx context.Context) doctor.Result {
				info, err := os.Stat(dir.path)
				if os.IsNotExist(err) {
					// Create the directory if it doesn't exist.
					if mkErr := os.MkdirAll(dir.path, 0o750); mkErr != nil {
						return warnf("create it manually or check permissions",
							"%s is missing and could not be created: %v", dir.path, mkErr)
					}
					return okf("%s created", dir.path)
				}
				if err != nil {
					return warnf("check filesystem permissions",
						"cannot stat %s: %v", dir.path, err)
				}
				if !info.IsDir() {
					return warnf("remove the file and let overkill recreate the directory",
						"%s is a file, not a directory", dir.path)
				}
				return okf("%s exists", dir.path)
			},
		})
	}

	// Telegram bot token.
	r.Register(doctor.SubsystemCheck{
		ID:       "gateway.telegram_token",
		Name:     "Telegram bot token",
		Category: doctor.CatBackend,
		Fn: func(ctx context.Context) doctor.Result {
			if d.Cfg != nil && d.Cfg.Gateways.Telegram.BotToken != "" {
				return okf("Telegram bot token configured")
			}
			return skip("Telegram gateway not configured")
		},
	})

	// Discord bot token.
	r.Register(doctor.SubsystemCheck{
		ID:       "gateway.discord_token",
		Name:     "Discord bot token",
		Category: doctor.CatBackend,
		Fn: func(ctx context.Context) doctor.Result {
			if d.Cfg != nil && d.Cfg.Gateways.Discord.BotToken != "" {
				return okf("Discord bot token configured")
			}
			return skip("Discord gateway not configured")
		},
	})
}
