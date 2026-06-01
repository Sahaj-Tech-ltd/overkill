// Package config — v2.0 settings hierarchy loader.
//
// The layered loader merges five sources in precedence order:
//
//	layer 0: DefaultUserOverrides (the yolo profile)
//	layer 1: /etc/overkill/user.yaml          (system defaults; ops-set)
//	layer 2: ~/.config/overkill/user.yaml     (user, the normal case)
//	layer 3: $WORKSPACE/.overkill/user.yaml   (per-project tweaks)
//	layer 4: /etc/overkill/enforced.yaml      (admin lock — LAST WORD)
//
// Each later layer wins on a per-field basis. The enforced layer
// applies AFTER the user/workspace layers so an org deployment can
// guarantee specific settings (e.g. "MCP shield always on") while
// still letting users tweak the rest.
//
// Implementation: YAML merge happens at the struct level via a
// per-field overlay. We deliberately avoid reflection — each field
// merge is explicit so we can document the per-field semantic in
// one place. Maps/slices REPLACE rather than append; pointers
// preserve nil-means-unset.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LayerSources is the set of files the hierarchy loader reads.
// Empty paths are skipped. Use DiscoverLayerSources to populate
// from environment + conventional paths.
type LayerSources struct {
	SystemFile    string
	UserFile      string
	WorkspaceFile string
	EnforcedFile  string
}

// DiscoverLayerSources resolves the conventional paths. The workspace
// file is rooted at cwd; pass empty cwd to skip the workspace layer.
func DiscoverLayerSources(cwd string) (LayerSources, error) {
	user, err := UserOverridesPath()
	if err != nil {
		return LayerSources{}, err
	}
	src := LayerSources{
		SystemFile:   "/etc/overkill/user.yaml",
		UserFile:     user,
		EnforcedFile: "/etc/overkill/enforced.yaml",
	}
	if cwd != "" {
		src.WorkspaceFile = filepath.Join(cwd, ".overkill", "user.yaml")
	}
	return src, nil
}

// LoadHierarchical returns the merged UserOverrides from all layers.
// Missing files are silently skipped. A parse error in any single
// layer fails the whole load — we don't want to half-apply an
// enforced.yaml that the operator expected to lock things down.
func LoadHierarchical(src LayerSources) (*UserOverrides, error) {
	out := DefaultUserOverrides()

	layers := []struct {
		name string
		path string
	}{
		{"system", src.SystemFile},
		{"user", src.UserFile},
		{"workspace", src.WorkspaceFile},
		{"enforced", src.EnforcedFile},
	}
	for _, l := range layers {
		if l.path == "" {
			continue
		}
		layer, err := LoadUserOverrides(l.path)
		if err != nil {
			// Missing-file is fine (LoadUserOverrides returns default
			// with no error in that case). Anything else surfaces.
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("config: load %s layer (%s): %w", l.name, l.path, err)
		}
		// Skip layers whose file actually didn't exist — those got a
		// fresh DefaultUserOverrides back, and merging a default over
		// a default is a no-op anyway. We detect "file exists" by
		// stat'ing; a load that succeeded but returned defaults
		// because of an empty file is also a no-op.
		if _, statErr := os.Stat(l.path); statErr != nil {
			continue
		}
		mergeUserOverrides(out, layer)
	}
	return out, nil
}

// mergeUserOverrides overlays src onto dst. Per-field semantics:
//
//   - Strings and floats: src wins when non-zero.
//   - Booleans (plain bool): we can't distinguish "explicit false"
//     from "unset" at the YAML layer with plain bool, so the
//     convention is: plain-bool fields are FULL REPLACEMENTS — every
//     layer's value wins over the prior. The Basic-tab fields use
//     plain bools because the UI always renders an explicit toggle.
//   - Boolean POINTERS (*bool): src wins only when non-nil. Used for
//     fields where "unset" must be distinguishable from "false."
//   - Slices and maps: REPLACEMENT, not append. Layered config that
//     wanted append semantics would need a list-merge directive,
//     which we intentionally avoid for predictability.
func mergeUserOverrides(dst, src *UserOverrides) {
	if dst == nil || src == nil {
		return
	}
	if src.Profile != "" {
		dst.Profile = src.Profile
	}
	mergeBasic(&dst.Basic, src.Basic)
	mergeAdvanced(&dst.Advanced, src.Advanced)
}

func mergeBasic(dst *BasicSettings, src BasicSettings) {
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.ContextBudget != 0 {
		dst.ContextBudget = src.ContextBudget
	}
	if len(src.Tools) > 0 {
		dst.Tools = src.Tools
	}
	if src.Theme != "" {
		dst.Theme = src.Theme
	}
	// VimMode and ConfirmWrites are *bool — only override when explicitly set (non-nil).
	if src.VimMode != nil {
		dst.VimMode = src.VimMode
	}
	if src.ConfirmWrites != nil {
		dst.ConfirmWrites = src.ConfirmWrites
	}
	if src.CostCapMonthly != 0 {
		dst.CostCapMonthly = src.CostCapMonthly
	}
	if src.AutoCompactPercent != 0 {
		dst.AutoCompactPercent = src.AutoCompactPercent
	}
}

func mergeAdvanced(dst *AdvancedSettings, src AdvancedSettings) {
	if src.SystemPrompt.Mode != "" {
		dst.SystemPrompt = src.SystemPrompt
	}
	if len(src.Tools) > 0 {
		dst.Tools = src.Tools
	}
	mergeScanners(&dst.Scanners, src.Scanners)
	mergeCompaction(&dst.Compaction, src.Compaction)
	if len(src.MCPServers) > 0 {
		dst.MCPServers = src.MCPServers
	}
	mergeSkills(&dst.Skills, src.Skills)
	if len(src.Hooks.PreToolUse)+len(src.Hooks.PostToolUse)+len(src.Hooks.OnError)+len(src.Hooks.OnStop) > 0 {
		dst.Hooks = src.Hooks
	}
	mergeMemory(&dst.Memory, src.Memory)
	if len(src.Providers) > 0 {
		dst.Providers = src.Providers
	}
	mergePersona(&dst.Persona, src.Persona)
	mergeTelemetry(&dst.Telemetry, src.Telemetry)
	mergePermissions(&dst.Permissions, src.Permissions)
	mergeBoard(&dst.Board, src.Board)
}

func mergeScanners(dst *ScannerToggles, src ScannerToggles) {
	// Per-field merge — only override when explicitly set (non-nil).
	// This prevents a layer that doesn't set scanners from zeroing out
	// a lower-priority layer's scanner configuration.
	if src.Command.Enabled != nil {
		dst.Command.Enabled = src.Command.Enabled
	}
	if src.Injection.Enabled != nil {
		dst.Injection.Enabled = src.Injection.Enabled
	}
	if src.PromptInjectBrowser.Enabled != nil {
		dst.PromptInjectBrowser.Enabled = src.PromptInjectBrowser.Enabled
	}
}

func mergeCompaction(dst *CompactionUserConfig, src CompactionUserConfig) {
	if src.Enabled != nil {
		dst.Enabled = src.Enabled
	}
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.SoftThreshold != 0 {
		dst.SoftThreshold = src.SoftThreshold
	}
	if src.HardThreshold != 0 {
		dst.HardThreshold = src.HardThreshold
	}
	if src.PreserveLast != 0 {
		dst.PreserveLast = src.PreserveLast
	}
	if src.Strategy != "" {
		dst.Strategy = src.Strategy
	}
}

func mergeSkills(dst *SkillsUserConfig, src SkillsUserConfig) {
	if src.CustomPath != "" {
		dst.CustomPath = src.CustomPath
	}
	if src.AutoLoad != nil {
		dst.AutoLoad = src.AutoLoad
	}
	if len(src.Active) > 0 {
		dst.Active = src.Active
	}
}

func mergeMemory(dst *MemoryUserConfig, src MemoryUserConfig) {
	if src.Enabled != nil {
		dst.Enabled = src.Enabled
	}
	if src.MaxEntries != 0 {
		dst.MaxEntries = src.MaxEntries
	}
	if src.TTLDays != 0 {
		dst.TTLDays = src.TTLDays
	}
	if src.EmbeddingModel != "" {
		dst.EmbeddingModel = src.EmbeddingModel
	}
}

func mergePersona(dst *PersonaUserConfig, src PersonaUserConfig) {
	if src.Tone != "" {
		dst.Tone = src.Tone
	}
	if src.Style != "" {
		dst.Style = src.Style
	}
	if src.Directive != "" {
		dst.Directive = src.Directive
	}
}

func mergeTelemetry(dst *TelemetryUserConfig, src TelemetryUserConfig) {
	if src.EventLog != nil {
		dst.EventLog = src.EventLog
	}
	if src.FlightRecorder != nil {
		dst.FlightRecorder = src.FlightRecorder
	}
	if src.RetentionDays != 0 {
		dst.RetentionDays = src.RetentionDays
	}
	if src.VerifyOnBoot != nil {
		dst.VerifyOnBoot = src.VerifyOnBoot
	}
}

func mergePermissions(dst *PermissionsUserConfig, src PermissionsUserConfig) {
	if src.AutoApproveAll != nil {
		dst.AutoApproveAll = src.AutoApproveAll
	}
	if src.SkipDestructiveConfirm != nil {
		dst.SkipDestructiveConfirm = src.SkipDestructiveConfirm
	}
}

func mergeBoard(dst *BoardUserConfig, src BoardUserConfig) {
	if src.Enabled != nil {
		dst.Enabled = src.Enabled
	}
	if src.AutoDispatch != nil {
		dst.AutoDispatch = src.AutoDispatch
	}
	if src.MaxConcurrent != 0 {
		dst.MaxConcurrent = src.MaxConcurrent
	}
	if src.DefaultEffort != "" {
		dst.DefaultEffort = src.DefaultEffort
	}
	if src.DefaultPriority != "" {
		dst.DefaultPriority = src.DefaultPriority
	}
	if len(src.ReviewPipeline) > 0 {
		dst.ReviewPipeline = src.ReviewPipeline
	}
	if len(src.OvenActions) > 0 {
		dst.OvenActions = src.OvenActions
	}
}
