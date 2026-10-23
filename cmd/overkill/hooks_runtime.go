package main

import (
	"path/filepath"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
)

// Legacy channel commands use the same owner-controlled directory as the
// session runtime. Never discover executable hooks from a remote workspace.
func loadConfiguredHooks() (*hooks.Registry, error) {
	home, err := config.ConfigDir()
	if err != nil {
		return nil, err
	}
	return hooks.NewRegistryFromDir(filepath.Join(home, "hooks"))
}
