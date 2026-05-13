package plugin

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// Discovered is one plugin found by walking the plugins directory.
type Discovered struct {
	Name           string
	EntryPath      string
	EntryArgs      []string
	Env            map[string]string
	StaticManifest *Manifest
}

// pluginToml is the on-disk shape of plugin.toml.
type pluginToml struct {
	Name        string            `toml:"name"`
	Version     string            `toml:"version"`
	Description string            `toml:"description"`
	Entry       string            `toml:"entry"`
	Args        []string          `toml:"args"`
	Env         map[string]string `toml:"env"`
	Permissions Permissions       `toml:"permissions"`
}

// DefaultPluginsDir returns ~/.overkill/plugins.
func DefaultPluginsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".overkill", "plugins")
}

// Discover walks the plugins directory and returns one Discovered entry per
// plugin. Two layouts are supported:
//   - <dir>/<name>/plugin.toml + entry script
//   - <dir>/<name>/<name>      executable file
//   - <dir>/<name>             single-file executable
func Discover(root string) ([]Discovered, error) {
	if root == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Discovered
	for _, e := range entries {
		full := filepath.Join(root, e.Name())
		if e.IsDir() {
			d, err := discoverDir(full, e.Name())
			if err != nil {
				continue
			}
			if d != nil {
				out = append(out, *d)
			}
			continue
		}
		if isExecutable(full) {
			out = append(out, Discovered{Name: e.Name(), EntryPath: full})
		}
	}
	return out, nil
}

func discoverDir(dir, name string) (*Discovered, error) {
	tomlPath := filepath.Join(dir, "plugin.toml")
	if data, err := os.ReadFile(tomlPath); err == nil {
		var pt pluginToml
		if err := toml.Unmarshal(data, &pt); err != nil {
			return nil, fmt.Errorf("plugin: parse %s: %w", tomlPath, err)
		}
		if pt.Entry == "" {
			return nil, fmt.Errorf("plugin: %s missing entry", tomlPath)
		}
		entry := pt.Entry
		if !filepath.IsAbs(entry) {
			entry = filepath.Join(dir, entry)
		}
		nm := pt.Name
		if nm == "" {
			nm = name
		}
		return &Discovered{
			Name:      nm,
			EntryPath: entry,
			EntryArgs: pt.Args,
			Env:       pt.Env,
			StaticManifest: &Manifest{
				Name:        nm,
				Version:     pt.Version,
				Description: pt.Description,
				Permissions: pt.Permissions,
			},
		}, nil
	}
	// Bare-executable layout: directory containing a binary with the same name.
	candidate := filepath.Join(dir, name)
	if isExecutable(candidate) {
		return &Discovered{Name: name, EntryPath: candidate}, nil
	}
	return nil, nil
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}
