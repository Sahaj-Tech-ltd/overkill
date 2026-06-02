// Package tools — dev-browser surface (Batch J).
//
// Four tools, deliberately narrow:
//
//   - devbrowser_open(name, url)       navigate to URL, return snapshot
//   - browser_snapshot(name)         re-snapshot without navigating
//   - devbrowser_click(name, selector)  click the matching element
//   - browser_type(name, selector, text)  type into the matching field
//
// "snapshotForAI" returns a structured page (title, headings, links,
// forms, bounded text) — not raw HTML. The model reads a scannable
// summary instead of choking on 100KB of markup per turn.
//
// No browser_evaluate. No file_upload. No download. The narrow
// surface IS the safety story — the model can drive a browser but
// can't smuggle arbitrary JS in.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/browser/devbrowser"
)

// DevBrowserOpenTool implements browser_open.
type DevBrowserOpenTool struct {
	Manager *devbrowser.Manager
}

// NewDevBrowserOpenTool wires the manager.
func NewDevBrowserOpenTool(m *devbrowser.Manager) *DevBrowserOpenTool {
	return &DevBrowserOpenTool{Manager: m}
}

func (t *DevBrowserOpenTool) Name() string { return "devbrowser_open" }

type browserOpenInput struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func (t *DevBrowserOpenTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.Manager == nil {
		return nil, fmt.Errorf("devbrowser_open: dev-browser not wired")
	}
	var req browserOpenInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("devbrowser_open: parse: %w", err)
	}
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	if req.Name == "" {
		return nil, fmt.Errorf("devbrowser_open: 'name' is required (label this page for future tool calls)")
	}
	if req.URL == "" {
		return nil, fmt.Errorf("devbrowser_open: 'url' is required")
	}
	snap, err := t.Manager.Open(req.Name, req.URL)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(snap)
}

// DevBrowserSnapshotTool implements browser_snapshot.
type DevBrowserSnapshotTool struct {
	Manager *devbrowser.Manager
}

// NewDevBrowserSnapshotTool wires the manager.
func NewDevBrowserSnapshotTool(m *devbrowser.Manager) *DevBrowserSnapshotTool {
	return &DevBrowserSnapshotTool{Manager: m}
}

func (t *DevBrowserSnapshotTool) Name() string { return "browser_snapshot" }

type browserSnapshotInput struct {
	Name string `json:"name"`
}

func (t *DevBrowserSnapshotTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.Manager == nil {
		return nil, fmt.Errorf("browser_snapshot: dev-browser not wired")
	}
	var req browserSnapshotInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("browser_snapshot: parse: %w", err)
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("browser_snapshot: 'name' is required")
	}
	snap, err := t.Manager.Snapshot(req.Name)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(snap)
}

// DevBrowserClickTool implements devbrowser_click.
type DevBrowserClickTool struct {
	Manager *devbrowser.Manager
}

// NewDevBrowserClickTool wires the manager.
func NewDevBrowserClickTool(m *devbrowser.Manager) *DevBrowserClickTool {
	return &DevBrowserClickTool{Manager: m}
}

func (t *DevBrowserClickTool) Name() string { return "devbrowser_click" }

type browserClickInput struct {
	Name     string `json:"name"`
	Selector string `json:"selector"`
}

func (t *DevBrowserClickTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.Manager == nil {
		return nil, fmt.Errorf("devbrowser_click: dev-browser not wired")
	}
	var req browserClickInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("devbrowser_click: parse: %w", err)
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("devbrowser_click: 'name' is required")
	}
	if strings.TrimSpace(req.Selector) == "" {
		return nil, fmt.Errorf("devbrowser_click: 'selector' is required")
	}
	snap, err := t.Manager.Click(req.Name, req.Selector)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(snap)
}

// DevBrowserTypeTool implements browser_type.
type DevBrowserTypeTool struct {
	Manager *devbrowser.Manager
}

// NewDevBrowserTypeTool wires the manager.
func NewDevBrowserTypeTool(m *devbrowser.Manager) *DevBrowserTypeTool {
	return &DevBrowserTypeTool{Manager: m}
}

func (t *DevBrowserTypeTool) Name() string { return "browser_type" }

type browserTypeInput struct {
	Name     string `json:"name"`
	Selector string `json:"selector"`
	Text     string `json:"text"`
}

func (t *DevBrowserTypeTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.Manager == nil {
		return nil, fmt.Errorf("browser_type: dev-browser not wired")
	}
	var req browserTypeInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("browser_type: parse: %w", err)
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("browser_type: 'name' is required")
	}
	if strings.TrimSpace(req.Selector) == "" {
		return nil, fmt.Errorf("browser_type: 'selector' is required")
	}
	snap, err := t.Manager.Type(req.Name, req.Selector, req.Text)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(snap)
}
