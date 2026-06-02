// Package flows provides the unified flow type system stolen from OpenClaw's
// src/flows/. Every interactive surface in Overkill — provider setup, channel
// picker, model selection, health checks, search config — produces a
// FlowContribution. A single WizardPrompter handles all of them.
//
// Storage: Postgres (not SQLite — "Postgres is just the goat").
package flows

import "time"

// FlowKind categorizes a contribution by domain.
type FlowKind string

const (
	KindChannel  FlowKind = "channel"
	KindCore     FlowKind = "core"
	KindProvider FlowKind = "provider"
	KindSearch   FlowKind = "search"
)

// FlowSurface identifies which UI surface the contribution belongs to.
type FlowSurface string

const (
	SurfaceAuthChoice  FlowSurface = "auth-choice"
	SurfaceHealth      FlowSurface = "health"
	SurfaceModelPicker FlowSurface = "model-picker"
	SurfaceSetup       FlowSurface = "setup"
)

// FlowSource records where a contribution came from. Manifest entries are
// static declarations in plugin manifests. Install-catalog entries are
// packages available for download. Runtime entries come from live plugins.
// Core entries ship with Overkill itself.
type FlowSource string

const (
	SourceManifest       FlowSource = "manifest"
	SourceInstallCatalog FlowSource = "install-catalog"
	SourceRuntime        FlowSource = "runtime"
	SourceCore           FlowSource = "core"
	SourcePlugin         FlowSource = "plugin"
)

// FlowContribution is the universal shape for every interactive flow surface.
// Every setup wizard, model picker, health check, and channel config produces
// a slice of these. One WizardPrompter.Select() handles all of them.
type FlowContribution struct {
	ID       string      `json:"id"`                 // e.g. "provider:setup:deepseek"
	Kind     FlowKind    `json:"kind"`               // "channel" | "core" | "provider" | "search"
	Surface  FlowSurface `json:"surface"`            // "auth-choice" | "health" | "model-picker" | "setup"
	Option   FlowOption  `json:"option"`             // The selectable UI item
	Source   FlowSource  `json:"source,omitempty"`   // Where this came from
	PluginID string      `json:"pluginId,omitempty"` // If registered by a plugin
}

// FlowOption is the item a user selects in a flow wizard. Value is the
// stable key; Label is the display text. Group and Docs provide optional
// visual grouping and documentation links.
type FlowOption struct {
	Value string       `json:"value"`          // Stable key for selection
	Label string       `json:"label"`          // Display label
	Hint  string       `json:"hint,omitempty"` // Extra description
	Group *OptionGroup `json:"group,omitempty"`
	Docs  *OptionDocs  `json:"docs,omitempty"`
}

// OptionGroup visually groups options in the picker UI.
type OptionGroup struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
}

// OptionDocs links to relevant documentation for this option.
type OptionDocs struct {
	Path  string `json:"path"`
	Label string `json:"label,omitempty"`
}

// FlowRecord is the persisted row in the flow_contributions table. It
// mirrors FlowContribution with database-level fields.
type FlowRecord struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Surface    string    `json:"surface"`
	Value      string    `json:"value"`
	Label      string    `json:"label"`
	Hint       string    `json:"hint,omitempty"`
	GroupID    string    `json:"groupId,omitempty"`
	GroupLabel string    `json:"groupLabel,omitempty"`
	Source     string    `json:"source"`
	PluginID   string    `json:"pluginId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// ToContribution converts a database record to the in-memory type.
func (r *FlowRecord) ToContribution() FlowContribution {
	c := FlowContribution{
		ID:      r.ID,
		Kind:    FlowKind(r.Kind),
		Surface: FlowSurface(r.Surface),
		Option: FlowOption{
			Value: r.Value,
			Label: r.Label,
			Hint:  r.Hint,
		},
		Source:   FlowSource(r.Source),
		PluginID: r.PluginID,
	}
	if r.GroupID != "" {
		c.Option.Group = &OptionGroup{ID: r.GroupID, Label: r.GroupLabel}
	}
	return c
}

// FromContribution converts an in-memory contribution to a database record.
func FromContribution(c FlowContribution) FlowRecord {
	r := FlowRecord{
		ID:       c.ID,
		Kind:     string(c.Kind),
		Surface:  string(c.Surface),
		Value:    c.Option.Value,
		Label:    c.Option.Label,
		Hint:     c.Option.Hint,
		Source:   string(c.Source),
		PluginID: c.PluginID,
	}
	if c.Option.Group != nil {
		r.GroupID = c.Option.Group.ID
		r.GroupLabel = c.Option.Group.Label
	}
	return r
}
