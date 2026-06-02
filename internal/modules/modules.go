// Package modules — dependency and skill update management.
//
// Overkill bundles third-party skills (superpowers, caveman, etc.) and
// system dependencies (Postgres driver, unicode, TTS engines). This
// package provides the manifest, fetcher, and updater so users can:
//
//   overkill modules list             # show all tracked modules
//   overkill modules update superpowers  # pull latest release from GitHub
//   overkill modules update --all     # update everything
//   overkill modules update --check   # show what's outdated
//   overkill modules update --dry-run # preview without applying
//
// Modules are tracked in ~/.overkill/modules.toml with source, version,
// and update strategy metadata.

package modules

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
	"github.com/pelletier/go-toml/v2"
)

// Source indicates where a module comes from.
type Source string

const (
	SourceGitHub   Source = "github"
	SourceGoModule Source = "go-module"
	SourceGit      Source = "git"
)

// ModuleType classifies what kind of module this is.
type ModuleType string

const (
	TypeSkill      ModuleType = "skill"
	TypeDependency ModuleType = "dependency"
	TypePlugin     ModuleType = "plugin"
)

// Module represents one tracked module in the manifest.
type Module struct {
	Name        string     `toml:"-"`           // key in the map
	Source      Source     `toml:"source"`      // github, go-module, git
	Repo        string     `toml:"repo"`        // GitHub: "owner/repo"
	Path        string     `toml:"path"`        // filesystem path under ~/.overkill/
	Version     string     `toml:"version"`     // current installed version
	Type        ModuleType `toml:"type"`        // skill, dependency, plugin
	Description string     `toml:"description"` // human-readable
	AutoUpdate  bool       `toml:"auto_update"` // update with --all
	LastCheck   time.Time  `toml:"last_check"`  // when we last checked for updates
}

// Manifest is the modules.toml file.
type Manifest struct {
	Modules map[string]*Module `toml:"modules"`
}

// UpdateResult describes what happened during an update.
type UpdateResult struct {
	Module      string `json:"module"`
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
	Updated     bool   `json:"updated"`
	Error       string `json:"error,omitempty"`
	Skipped     bool   `json:"skipped"`
	Reason      string `json:"reason,omitempty"`
}

// Manager handles module discovery, checking, and updating.
type Manager struct {
	manifestPath string
	manifest     *Manifest
	overkillHome string
}

// NewManager loads or creates the modules manifest.
func NewManager(overkillHome string) (*Manager, error) {
	m := &Manager{
		manifestPath: filepath.Join(overkillHome, "modules.toml"),
		overkillHome: overkillHome,
	}

	if err := m.load(); err != nil {
		return nil, err
	}

	return m, nil
}

func (m *Manager) load() error {
	m.manifest = &Manifest{Modules: make(map[string]*Module)}

	data, err := os.ReadFile(m.manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			// First run — seed with built-in defaults.
			m.seedDefaults()
			return m.save()
		}
		return fmt.Errorf("modules: read manifest: %w", err)
	}

	if err := toml.Unmarshal(data, m.manifest); err != nil {
		return fmt.Errorf("modules: parse manifest: %w", err)
	}

	// Populate name keys from the map.
	for name, mod := range m.manifest.Modules {
		mod.Name = name
	}

	return nil
}

func (m *Manager) save() error {
	if err := os.MkdirAll(filepath.Dir(m.manifestPath), 0750); err != nil {
		return err
	}

	data, err := toml.Marshal(m.manifest)
	if err != nil {
		return fmt.Errorf("modules: marshal manifest: %w", err)
	}
	return atomicfile.WriteFile(m.manifestPath, data, 0600)
}

// seedDefaults populates the manifest with built-in tracked modules.
func (m *Manager) seedDefaults() {
	defaults := []Module{
		{
			Name: "superpowers", Source: SourceGitHub, Repo: "anthropics/superpowers",
			Path: "skills/superpowers", Version: "v0.0.0", Type: TypeSkill,
			Description: "Claude Code superpowers skill suite — planning, TDD, debugging, code review",
			AutoUpdate:  true,
		},
		{
			Name: "caveman", Source: SourceGitHub, Repo: "harsh-caveman/caveman",
			Path: "skills/caveman", Version: "v0.0.0", Type: TypeSkill,
			Description: "Ultra-compressed communication mode — 75% token savings",
			AutoUpdate:  true,
		},
		{
			Name: "postgres", Source: SourceGoModule, Repo: "github.com/lib/pq",
			Path: "", Version: "v1.10.9", Type: TypeDependency,
			Description: "PostgreSQL driver for Go — database/sql compatible",
			AutoUpdate:  false,
		},
		{
			Name: "unicode", Source: SourceGoModule, Repo: "golang.org/x/text",
			Path: "", Version: "v0.14.0", Type: TypeDependency,
			Description: "Unicode text handling — normalization, collation, encoding",
			AutoUpdate:  true,
		},
		{
			Name: "edge-tts", Source: SourceGit, Repo: "https://github.com/rany2/edge-tts",
			Path: "", Version: "latest", Type: TypeDependency,
			Description: "Microsoft Edge TTS — free, natural voices, no API key",
			AutoUpdate:  false,
		},
	}

	for i := range defaults {
		mod := &defaults[i]
		m.manifest.Modules[mod.Name] = mod
	}
}

// List returns all tracked modules sorted by name.
func (m *Manager) List() []*Module {
	modules := make([]*Module, 0, len(m.manifest.Modules))
	for _, mod := range m.manifest.Modules {
		modules = append(modules, mod)
	}
	sort.Slice(modules, func(i, j int) bool {
		return modules[i].Name < modules[j].Name
	})
	return modules
}

// Get returns a module by name.
func (m *Manager) Get(name string) *Module {
	return m.manifest.Modules[name]
}

// CheckForUpdates checks a single module for available updates.
// Returns (latestVersion, needsUpdate, error).
func (m *Manager) CheckForUpdates(name string) (string, bool, error) {
	mod := m.Get(name)
	if mod == nil {
		return "", false, fmt.Errorf("modules: unknown module %q", name)
	}

	switch mod.Source {
	case SourceGitHub:
		return m.checkGitHubRelease(mod)
	case SourceGoModule:
		return mod.Version, false, nil // go modules managed by go.mod
	case SourceGit:
		return mod.Version, false, nil // git repos managed manually
	default:
		return mod.Version, false, nil
	}
}

// checkGitHubRelease queries the GitHub API for the latest release tag.
func (m *Manager) checkGitHubRelease(mod *Module) (string, bool, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", mod.Repo)

	resp, err := httpGet(url)
	if err != nil {
		return "", false, fmt.Errorf("modules: GitHub release check for %s: %w", mod.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return "", false, fmt.Errorf("modules: no releases found for %s (%s)", mod.Name, mod.Repo)
	}
	if resp.StatusCode != 200 {
		return "", false, fmt.Errorf("modules: GitHub API returned %d for %s", resp.StatusCode, mod.Name)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", false, fmt.Errorf("modules: read release body: %w", err)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", false, fmt.Errorf("modules: parse GitHub release: %w", err)
	}
	if release.TagName == "" {
		return "", false, fmt.Errorf("modules: no tag_name in release for %s", mod.Name)
	}

	needsUpdate := release.TagName != mod.Version
	return release.TagName, needsUpdate, nil
}

// Update updates a single module to its latest version.
func (m *Manager) Update(name string) (*UpdateResult, error) {
	mod := m.Get(name)
	if mod == nil {
		return nil, fmt.Errorf("modules: unknown module %q", name)
	}

	result := &UpdateResult{
		Module:      name,
		FromVersion: mod.Version,
	}

	latest, needsUpdate, err := m.CheckForUpdates(name)
	if err != nil {
		result.Error = err.Error()
		return result, err
	}

	if !needsUpdate {
		result.Skipped = true
		result.Reason = fmt.Sprintf("already at %s", mod.Version)
		result.ToVersion = mod.Version
		return result, nil
	}

	// Attempt the update.
	oldVersion := mod.Version
	if err := m.applyUpdate(mod, latest); err != nil {
		result.Error = err.Error()
		mod.Version = oldVersion // rollback
		return result, err
	}

	mod.Version = latest
	mod.LastCheck = time.Now()

	if err := m.save(); err != nil {
		result.Error = fmt.Sprintf("saved but failed to persist manifest: %v", err)
	}

	result.Updated = true
	result.ToVersion = latest
	return result, nil
}

// UpdateAll updates all modules with AutoUpdate enabled.
func (m *Manager) UpdateAll() ([]*UpdateResult, error) {
	var results []*UpdateResult
	var errs []string

	for _, mod := range m.List() {
		if !mod.AutoUpdate {
			results = append(results, &UpdateResult{
				Module:  mod.Name,
				Skipped: true,
				Reason:  "auto-update disabled",
			})
			continue
		}

		result, err := m.Update(mod.Name)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", mod.Name, err))
		}
		results = append(results, result)
	}

	var combinedErr error
	if len(errs) > 0 {
		combinedErr = fmt.Errorf("modules: update errors:\n%s", strings.Join(errs, "\n"))
	}

	return results, combinedErr
}

// CheckAll checks all modules for available updates.
// GitHub-sourced modules that return [NOT IMPLEMENTED] are noted in
// the result map with a "(not yet implemented)" suffix so the CLI can
// surface them without treating the check as a fatal error.
func (m *Manager) CheckAll() (map[string]string, []string) {
	updates := make(map[string]string)
	var skipped []string
	for _, mod := range m.List() {
		latest, needsUpdate, err := m.CheckForUpdates(mod.Name)
		if err != nil {
			// GitHub release checking isn't wired yet — surface as info.
			skipped = append(skipped, fmt.Sprintf("%s: %v", mod.Name, err))
			continue
		}
		if !needsUpdate {
			continue
		}
		updates[mod.Name] = fmt.Sprintf("%s → %s", mod.Version, latest)
	}
	return updates, skipped
}

// applyUpdate performs the actual update for a module.
func (m *Manager) applyUpdate(mod *Module, version string) error {
	switch mod.Source {
	case SourceGitHub:
		return m.fetchGitHubRelease(mod, version)
	case SourceGoModule:
		return m.updateGoModule(mod, version)
	case SourceGit:
		return nil // manual
	default:
		return fmt.Errorf("modules: unknown source %q", mod.Source)
	}
}

// fetchGitHubRelease downloads and extracts a GitHub release tarball.
// Does NOT update the manifest version or call save() — the caller (Update)
// handles the version bump and persistence.
func (m *Manager) fetchGitHubRelease(mod *Module, version string) error {
	// Download the tarball for the tag.
	tarballURL := fmt.Sprintf("https://api.github.com/repos/%s/tarball/%s", mod.Repo, version)

	resp, err := httpGet(tarballURL)
	if err != nil {
		return fmt.Errorf("modules: download %s@%s: %w", mod.Name, version, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("modules: GitHub returned %d downloading %s@%s", resp.StatusCode, mod.Name, version)
	}

	// Extract to the module directory under ~/.overkill/modules/<name>/.
	dest := filepath.Join(m.overkillHome, "modules", mod.Name)
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("modules: clean destination %s: %w", dest, err)
	}
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return fmt.Errorf("modules: create destination %s: %w", dest, err)
	}

	if err := extractTarGz(resp.Body, dest); err != nil {
		return fmt.Errorf("modules: extract %s@%s: %w", mod.Name, version, err)
	}

	return nil
}

// updateGoModule updates a Go module dependency.
func (m *Manager) updateGoModule(mod *Module, version string) error {
	_ = mod
	_ = version
	// Go modules are managed by go.mod in the build process.
	return nil
}

// FormatUpdateReport produces a human-readable update summary.
func FormatUpdateReport(results []*UpdateResult) string {
	if len(results) == 0 {
		return "No modules to update."
	}

	var b strings.Builder
	b.WriteString("## Module Updates\n\n")

	updated := 0
	skipped := 0
	failed := 0

	for _, r := range results {
		if r.Updated {
			b.WriteString(fmt.Sprintf("✅ **%s**: %s → %s\n", r.Module, r.FromVersion, r.ToVersion))
			updated++
		} else if r.Skipped {
			b.WriteString(fmt.Sprintf("⏭️ **%s**: %s\n", r.Module, r.Reason))
			skipped++
		} else if r.Error != "" {
			b.WriteString(fmt.Sprintf("❌ **%s**: %s\n", r.Module, r.Error))
			failed++
		}
	}

	b.WriteString(fmt.Sprintf("\n%d updated, %d skipped, %d failed\n", updated, skipped, failed))
	return b.String()
}
