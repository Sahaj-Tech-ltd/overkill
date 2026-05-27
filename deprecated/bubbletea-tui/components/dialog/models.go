// Package dialog — models.go renders the live model picker. The list is
// sourced from internal/providers (models.dev) at open time; nothing is
// hardcoded here. Layout is editorial: dim provider headers, accent model
// names, muted metadata (cost / context / capability tags).
package dialog

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/theme"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// ModelEntry is a flat list row carrying everything the dialog needs to
// render and emit a selection.
type ModelEntry struct {
	ID            string
	Name          string
	Provider      string // provider id (e.g. "openai")
	ProviderName  string // display name
	CostIn        float64
	CostOut       float64
	ContextWindow int
	Caps          []string // tags: "tools" | "vision" | "reasoning"
}

type ShowModelDialogMsg struct{}
type CloseModelDialogMsg struct{}
type ModelSelectedMsg struct {
	ModelID  string
	Provider string
}

// ModelDialog is a two-step picker:
//
//	step 0 — list of providers (selecting drills into step 1)
//	step 1 — list of models for the chosen provider (selecting emits ModelSelectedMsg)
//
// Backspace from step 1 returns to step 0. Esc closes the dialog from either step.
// Filter-as-you-type works at each step (filters the visible list).
type ModelDialog struct {
	Dialog
	Models    []ModelEntry
	Filtered  []ModelEntry
	Cursor    int
	Query     string
	Providers []string

	// step: 0 = provider list, 1 = model list for selectedProvider
	step             int
	selectedProvider string

	Loading   bool
	spinFrame int
	Source    string // "live" | "cache" | "baked"
	LoadErr   error
}

func NewModelDialog() ModelDialog {
	return ModelDialog{Dialog: Dialog{Title: "Models"}}
}

// SetLoading toggles the spinner banner.
func (m *ModelDialog) SetLoading(on bool) { m.Loading = on }

// SetCatalog replaces the dialog's underlying model list from a live catalog.
// source is one of "live", "cache", "baked".
func (m *ModelDialog) SetCatalog(cat *providers.Catalog, source string) {
	if cat == nil {
		return
	}
	var entries []ModelEntry
	for _, p := range cat.Providers() {
		for _, mdl := range cat.Models(p.ID) {
			entries = append(entries, ModelEntry{
				ID:            mdl.ID,
				Name:          mdl.Name,
				Provider:      p.ID,
				ProviderName:  p.Name,
				CostIn:        mdl.Cost.Input,
				CostOut:       mdl.Cost.Output,
				ContextWindow: mdl.Limit.Context,
				Caps:          providers.CapsTags(mdl),
			})
		}
	}
	m.Source = source
	m.Loading = false
	m.SetModels(entries)
}

// SetModels is kept for tests and direct seeding. Resets to step 0
// (provider list) so callers don't have to manage step state.
func (m *ModelDialog) SetModels(models []ModelEntry) {
	m.Models = models
	m.Cursor = 0
	m.Query = ""
	m.step = 0
	m.selectedProvider = ""
	m.Providers = nil
	seen := map[string]bool{}
	for _, mdl := range models {
		key := mdl.Provider
		if !seen[key] {
			m.Providers = append(m.Providers, key)
			seen[key] = true
		}
	}
	m.refilter()
}

// providerLabel returns the human-readable provider name for an id, falling
// back to the id itself if no model row carries a non-empty ProviderName.
func (m ModelDialog) providerLabel(id string) string {
	for _, mdl := range m.Models {
		if mdl.Provider == id && mdl.ProviderName != "" {
			return mdl.ProviderName
		}
	}
	return id
}

// providerModelCount counts the models the catalog has for a given provider.
func (m ModelDialog) providerModelCount(id string) int {
	n := 0
	for _, mdl := range m.Models {
		if mdl.Provider == id {
			n++
		}
	}
	return n
}

func (m ModelDialog) Update(msg tea.Msg) (ModelDialog, tea.Cmd) {
	switch k := msg.(type) {
	case tea.KeyMsg:
		// Provider list (step 0) tracks providersFiltered length; model list
		// (step 1) tracks Filtered length. listLen abstracts that.
		listLen := len(m.Filtered)
		if m.step == 0 {
			listLen = len(m.providersFiltered())
		}
		switch k.String() {
		case "up", "k":
			if m.Cursor > 0 {
				m.Cursor--
			}
		case "down", "j":
			if m.Cursor < listLen-1 {
				m.Cursor++
			}
		case "home", "g":
			m.Cursor = 0
		case "end", "G":
			if listLen > 0 {
				m.Cursor = listLen - 1
			}
		case "backspace":
			// Three meanings, in priority: edit filter, then go up a step,
			// then no-op.
			if len(m.Query) > 0 {
				m.Query = m.Query[:len(m.Query)-1]
				m.refilter()
			} else if m.step == 1 {
				m.step = 0
				m.selectedProvider = ""
				m.Cursor = 0
				m.Query = ""
				m.refilter()
			}
		case "enter":
			if m.step == 0 {
				provs := m.providersFiltered()
				if m.Cursor < len(provs) {
					m.selectedProvider = provs[m.Cursor]
					m.step = 1
					m.Cursor = 0
					m.Query = ""
					m.refilter()
				}
			} else {
				if m.Cursor < len(m.Filtered) {
					sel := m.Filtered[m.Cursor]
					return m, func() tea.Msg {
						return ModelSelectedMsg{ModelID: sel.ID, Provider: sel.Provider}
					}
				}
			}
		case "esc":
			return m, func() tea.Msg { return CloseModelDialogMsg{} }
		default:
			if len(k.Runes) > 0 && k.Type == tea.KeyRunes {
				m.Query += string(k.Runes)
				m.Cursor = 0
				m.refilter()
			}
		}
	case ShowModelDialogMsg:
		m.Show = true
		m.step = 0
		m.Cursor = 0
		m.Query = ""
	case CloseModelDialogMsg:
		m.Show = false
		m.step = 0
		m.selectedProvider = ""
		m.Query = ""
		m.Cursor = 0
	}
	return m, nil
}

// refilter rebuilds the visible rows based on step + query.
//   - step 0: filters Providers by substring on id+display name
//   - step 1: filters Models for selectedProvider by substring on id+name
func (m *ModelDialog) refilter() {
	q := strings.ToLower(m.Query)
	if m.step == 0 {
		// Filtered isn't used at step 0; providersFiltered() reads Query
		// directly. Just clamp cursor.
		n := len(m.providersFiltered())
		if m.Cursor >= n {
			m.Cursor = max(0, n-1)
		}
		return
	}
	m.Filtered = nil
	for _, mdl := range m.Models {
		if mdl.Provider != m.selectedProvider {
			continue
		}
		hay := strings.ToLower(mdl.ID + " " + mdl.Name)
		if q == "" || strings.Contains(hay, q) {
			m.Filtered = append(m.Filtered, mdl)
		}
	}
	if m.Cursor >= len(m.Filtered) {
		m.Cursor = max(0, len(m.Filtered)-1)
	}
}

// providersFiltered returns the provider IDs that match the current query.
// Used at step 0 (provider list).
func (m ModelDialog) providersFiltered() []string {
	q := strings.ToLower(m.Query)
	if q == "" {
		return m.Providers
	}
	out := make([]string, 0, len(m.Providers))
	for _, p := range m.Providers {
		hay := strings.ToLower(p + " " + m.providerLabel(p))
		if strings.Contains(hay, q) {
			out = append(out, p)
		}
	}
	return out
}

// prevProviderStart returns the index of the first row of the provider that
// precedes the row currently under the cursor. Used by PgUp/Ctrl+U to jump
// between providers without scrolling row-by-row.
func (m ModelDialog) prevProviderStart() int {
	if len(m.Filtered) == 0 {
		return 0
	}
	cur := m.Cursor
	if cur < 0 || cur >= len(m.Filtered) {
		return 0
	}
	curProv := m.Filtered[cur].Provider
	// Walk back to the start of the current group.
	i := cur
	for i > 0 && m.Filtered[i-1].Provider == curProv {
		i--
	}
	// If we were already at the top of the current group, jump one more
	// step into the previous group's first row.
	if i == cur && i > 0 {
		prevProv := m.Filtered[i-1].Provider
		j := i - 1
		for j > 0 && m.Filtered[j-1].Provider == prevProv {
			j--
		}
		return j
	}
	return i
}

// nextProviderStart returns the index of the first row of the provider that
// follows the row currently under the cursor.
func (m ModelDialog) nextProviderStart() int {
	if len(m.Filtered) == 0 {
		return 0
	}
	cur := m.Cursor
	if cur < 0 || cur >= len(m.Filtered) {
		return 0
	}
	curProv := m.Filtered[cur].Provider
	for i := cur + 1; i < len(m.Filtered); i++ {
		if m.Filtered[i].Provider != curProv {
			return i
		}
	}
	return len(m.Filtered) - 1
}

// spinnerFrames is a tiny braille spinner used while the live fetch runs.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m ModelDialog) View(totalWidth, totalHeight int) string {
	if !m.Show {
		return ""
	}
	t := theme.CurrentTheme()
	dim := lipgloss.NewStyle().Foreground(t.TextMuted())
	accent := lipgloss.NewStyle().Foreground(t.DialogAccent()).Bold(true)
	muted := lipgloss.NewStyle().Foreground(t.TextMuted()).Faint(true)

	var lines []string

	// Loading banner.
	if m.Loading {
		spin := spinnerFrames[m.spinFrame%len(spinnerFrames)]
		lines = append(lines, dim.Render(spin+" loading models from models.dev..."))
		lines = append(lines, "")
	} else if m.Source != "" && m.Source != "live" {
		hint := "(cached, offline)"
		if m.Source == "baked" {
			hint = "(baked fallback — no network)"
		}
		lines = append(lines, muted.Render(hint))
		lines = append(lines, "")
	}

	// Breadcrumb shows where we are in the two-step flow.
	switch m.step {
	case 0:
		lines = append(lines, dim.Render("providers"))
	case 1:
		lines = append(lines, dim.Render("providers › "+m.providerLabel(m.selectedProvider)))
	}
	lines = append(lines, "")

	if m.Query != "" {
		lines = append(lines, muted.Render("filter: ")+m.Query)
		lines = append(lines, "")
	}

	// Build the visible rows for the current step.
	var rendered []string
	switch m.step {
	case 0:
		provs := m.providersFiltered()
		if len(provs) == 0 {
			lines = append(lines, muted.Render("no providers match"))
		} else {
			rendered = make([]string, len(provs))
			for i, p := range provs {
				cursor := "  "
				if i == m.Cursor {
					cursor = accent.Render("> ")
				}
				label := m.providerLabel(p)
				count := m.providerModelCount(p)
				countStr := muted.Render(fmt.Sprintf("  %d models", count))
				row := cursor + accent.Render(label)
				if label != p {
					row += "  " + muted.Render(p)
				}
				row += countStr
				rendered[i] = row
			}
		}
	case 1:
		if len(m.Filtered) == 0 {
			lines = append(lines, muted.Render("no models match"))
		} else {
			rendered = make([]string, len(m.Filtered))
			for i, mdl := range m.Filtered {
				cursor := "  "
				if i == m.Cursor {
					cursor = accent.Render("> ")
				}
				name := mdl.Name
				if name == "" {
					name = mdl.ID
				}
				if len(name) > 40 {
					name = name[:37] + "..."
				}
				meta := []string{}
				if c := providers.FormatCost(mdl.CostIn, mdl.CostOut); c != "" {
					meta = append(meta, c)
				}
				if c := providers.FormatContext(mdl.ContextWindow); c != "" {
					meta = append(meta, c+" ctx")
				}
				if len(mdl.Caps) > 0 {
					meta = append(meta, strings.Join(mdl.Caps, "·"))
				}
				metaStr := ""
				if len(meta) > 0 {
					metaStr = "  " + muted.Render(strings.Join(meta, "  "))
				}
				row := cursor + accent.Render(name) + metaStr
				if !strings.EqualFold(mdl.ID, name) {
					row += "  " + muted.Render(mdl.ID)
				}
				rendered[i] = row
			}
		}
	}

	if len(rendered) > 0 {
		maxRows := totalHeight - 12
		if maxRows > 18 {
			maxRows = 18
		}
		if maxRows < 5 {
			maxRows = 5
		}
		visible, before, after := Window(rendered, m.Cursor, maxRows)
		if before > 0 {
			lines = append(lines, muted.Render(fmt.Sprintf("  ↑ %d more", before)))
		}
		lines = append(lines, visible...)
		if after > 0 {
			lines = append(lines, muted.Render(fmt.Sprintf("  ↓ %d more", after)))
		}
	}

	lines = append(lines, "")
	hint := "↑↓ move · type to filter · enter pick · esc close"
	if m.step == 1 {
		hint = "↑↓ move · type to filter · enter pick · backspace ← providers · esc close"
	}
	lines = append(lines, muted.Render(hint))

	content := strings.Join(lines, "\n")
	return m.BaseView(content, totalWidth, totalHeight)
}

// tabFiltered reports whether the current Filtered set looks like a single-
// provider slice (i.e. the user just hit tab). Used to highlight the active
// tab instead of always showing them all dimmed.
func (m ModelDialog) tabFiltered() bool {
	if len(m.Filtered) == 0 {
		return false
	}
	first := m.Filtered[0].Provider
	for _, e := range m.Filtered {
		if e.Provider != first {
			return false
		}
	}
	return true
}

// AdvanceSpinner is called from the TUI tick to animate the spinner while
// loading. Cheap and idempotent.
func (m *ModelDialog) AdvanceSpinner() { m.spinFrame++ }

// String helpers for tests.
func (m ModelDialog) summary() string {
	return fmt.Sprintf("%d models, %d providers, source=%s", len(m.Models), len(m.Providers), m.Source)
}
