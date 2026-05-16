// Package dialog — settings.go renders the v2.0 Settings dialog.
//
// First cut: Basic tab only. Eight controls — model, context budget,
// tool toggles, theme, vim mode, cost cap, auto-compact threshold,
// confirm-before-write — plus the profile picker at the top.
//
// Navigation:
//
//	↑ / k         — move cursor up
//	↓ / j         — move cursor down
//	enter / space — edit field (or toggle a bool)
//	←  / →        — adjust numeric slider / cycle enum / cycle profile
//	tab           — (placeholder) switch to Advanced tab (not yet rendered)
//	ctrl+s        — save user.yaml + close
//	esc           — close without saving (prompts when dirty)
//
// Saves run through atomicfile via internal/config.SaveUserOverrides.
// The hot-reload bus picks the file change up and re-applies live;
// the agent's WireAgent subscriber handles model swap + persona /
// system_prompt reflow.
//
// Profile picker behaviour: changing the profile applies its preset
// values into the in-memory UserOverrides. The picker shows
// "(modified)" suffix when individual fields have drifted from the
// named profile — same indicator the design doc spec'd.
package dialog

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/theme"
)

// SettingsDialog renders the Settings page. Construct via
// NewSettingsDialog; mutate via Update; render via View.
type SettingsDialog struct {
	Dialog

	// overrides is the in-flight edit buffer. NOT applied to disk
	// until Ctrl+S; closing without saving discards changes.
	overrides *config.UserOverrides
	// savedSnapshot is the copy on disk at open time so we can
	// detect dirty state for the close-confirm prompt.
	savedSnapshot config.UserOverrides
	// path is the user.yaml location (typically ~/.config/overkill/user.yaml).
	path string

	// fields drives the visible rows. Built once at New time; the
	// cursor moves over it.
	fields []settingsField
	cursor int

	// availableThemes / availableModels are populated at open time
	// from the live catalog / theme registry. Cached on the dialog
	// so the per-frame View() is cheap.
	availableThemes []string
	availableModels []string

	// status holds the most recent toast ("saved", "dirty", error).
	// Renders below the field list; cleared on the next keystroke.
	status string
	// dirtyConfirm: when the user presses esc with unsaved edits we
	// flip this to true and the next esc actually closes.
	dirtyConfirm bool
}

// settingsField is one row in the Basic tab. Kind determines how the
// renderer + the editor key handler treat it.
type settingsField struct {
	label string
	help  string
	kind  fieldKind
	// getter/setter operate on the in-memory overrides struct so
	// editing is type-safe without reflection.
	get func(u *config.UserOverrides) string // human-readable current value
	// edit handles a key press for this field. Returns true when the
	// key was consumed.
	edit func(d *SettingsDialog, key tea.KeyMsg) bool
}

type fieldKind int

const (
	kindProfile fieldKind = iota
	kindModel
	kindSlider
	kindToolToggles
	kindEnumTheme
	kindBool
	kindFloat
)

// NewSettingsDialog wires the field table. Pass the path so Save
// goes to the right place; pass the current overrides so the editor
// starts in the user's actual state.
func NewSettingsDialog(path string, current *config.UserOverrides) *SettingsDialog {
	if current == nil {
		current = config.DefaultUserOverrides()
	}
	snap := *current
	d := &SettingsDialog{
		Dialog:        Dialog{Title: "Settings — Basic"},
		overrides:     current,
		savedSnapshot: snap,
		path:          path,
	}
	d.fields = []settingsField{
		{
			label: "Profile",
			help:  "yolo / default / paranoid / enterprise",
			kind:  kindProfile,
			get: func(u *config.UserOverrides) string {
				p := u.Profile
				if p == "" {
					p = "yolo"
				}
				if d.driftedFromProfile() {
					return p + "  (modified)"
				}
				return p
			},
			edit: editProfile,
		},
		{
			label: "Model",
			help:  "active model — full picker via /model command",
			kind:  kindModel,
			get: func(u *config.UserOverrides) string {
				if u.Basic.Model == "" {
					return "(default)"
				}
				return u.Basic.Model
			},
			edit: editModel,
		},
		{
			label: "Context budget",
			help:  "fraction of the model's window used per turn",
			kind:  kindSlider,
			get: func(u *config.UserOverrides) string {
				v := u.Basic.ContextBudget
				if v == 0 {
					v = 0.8 // baked default
				}
				return renderSlider(v, 0, 1)
			},
			edit: makeFloatEdit(0, 1, 0.05, func(u *config.UserOverrides) *float64 { return &u.Basic.ContextBudget }),
		},
		{
			label: "Tools",
			help:  "press enter to open the tool toggle list",
			kind:  kindToolToggles,
			get: func(u *config.UserOverrides) string {
				enabled := 0
				for _, on := range u.Basic.Tools {
					if on {
						enabled++
					}
				}
				return fmt.Sprintf("%d enabled", enabled)
			},
			edit: editToolToggles,
		},
		{
			label: "Theme",
			help:  "press ← / → to cycle themes",
			kind:  kindEnumTheme,
			get: func(u *config.UserOverrides) string {
				if u.Basic.Theme == "" {
					return "(default)"
				}
				return u.Basic.Theme
			},
			edit: editTheme,
		},
		{
			label: "Vim mode",
			help:  "modal input bindings (toggle with space)",
			kind:  kindBool,
			get:   func(u *config.UserOverrides) string { return boolLabel(u.Basic.VimMode) },
			edit:  makeBoolEdit(func(u *config.UserOverrides) *bool { return &u.Basic.VimMode }),
		},
		{
			label: "Cost cap / month",
			help:  "USD; 0 = no cap (just track)",
			kind:  kindFloat,
			get: func(u *config.UserOverrides) string {
				if u.Basic.CostCapMonthly <= 0 {
					return "no cap"
				}
				return fmt.Sprintf("$%.0f", u.Basic.CostCapMonthly)
			},
			edit: makeFloatEdit(0, 10000, 5, func(u *config.UserOverrides) *float64 { return &u.Basic.CostCapMonthly }),
		},
		{
			label: "Auto-compact threshold",
			help:  "trigger compaction at this fraction of the context window",
			kind:  kindSlider,
			get: func(u *config.UserOverrides) string {
				v := u.Basic.AutoCompactPercent
				if v == 0 {
					v = 0.5
				}
				return renderSlider(v, 0, 1)
			},
			edit: makeFloatEdit(0, 1, 0.05, func(u *config.UserOverrides) *float64 { return &u.Basic.AutoCompactPercent }),
		},
		{
			label: "Confirm before write",
			help:  "ask before destructive shell / fs / patch operations",
			kind:  kindBool,
			get:   func(u *config.UserOverrides) string { return boolLabel(u.Basic.ConfirmWrites) },
			edit:  makeBoolEdit(func(u *config.UserOverrides) *bool { return &u.Basic.ConfirmWrites }),
		},
	}
	return d
}

// SetThemes / SetModels populate the cycling enums. Call before Show
// so the picker has something to scroll through. Optional — without
// them ← / → on Theme / Model become no-ops (the field still renders).
func (d *SettingsDialog) SetThemes(names []string)  { d.availableThemes = names }
func (d *SettingsDialog) SetModels(ids []string)    { d.availableModels = ids }

// Current returns the in-flight overrides so a caller can inspect or
// re-bind after Save. Test helper.
func (d *SettingsDialog) Current() *config.UserOverrides { return d.overrides }

// IsDirty reports whether the editor has unsaved changes.
func (d *SettingsDialog) IsDirty() bool {
	return !overridesEqual(d.overrides, &d.savedSnapshot)
}

// SaveResultMsg is emitted on save success/failure so the parent
// model can show a toast.
type SaveResultMsg struct {
	Err   error
	Saved bool
}

// keyName normalises a bubbletea key into the dialog's vocabulary.
// `tea.KeyMsg.String()` returns " " for space and the literal char
// for runes; the dialog cares about logical names like "space".
func keyName(k tea.KeyMsg) string {
	if k.Type == tea.KeySpace {
		return "space"
	}
	return k.String()
}

// Update handles a key. Returns the dialog (state may have mutated)
// and an optional Cmd (e.g. close + save toast).
func (d *SettingsDialog) Update(msg tea.Msg) (*SettingsDialog, tea.Cmd) {
	if !d.Show {
		return d, nil
	}
	d.status = ""
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, nil
	}
	switch keyName(key) {
	case "esc":
		if d.IsDirty() && !d.dirtyConfirm {
			d.status = "unsaved changes — esc again to discard, ctrl+s to save"
			d.dirtyConfirm = true
			return d, nil
		}
		d.HideDialog()
		d.dirtyConfirm = false
		return d, nil
	case "ctrl+s":
		err := config.SaveUserOverrides(d.path, d.overrides)
		if err == nil {
			d.savedSnapshot = *d.overrides
			d.status = "saved → " + d.path
			d.HideDialog()
		} else {
			d.status = "save failed: " + err.Error()
		}
		return d, func() tea.Msg { return SaveResultMsg{Err: err, Saved: err == nil} }
	case "up", "k":
		if d.cursor > 0 {
			d.cursor--
		}
		return d, nil
	case "down", "j":
		if d.cursor < len(d.fields)-1 {
			d.cursor++
		}
		return d, nil
	}
	if d.cursor >= 0 && d.cursor < len(d.fields) {
		d.fields[d.cursor].edit(d, key)
	}
	return d, nil
}

// View renders the dialog body. Caller composites into the dialog
// frame via BaseView.
func (d *SettingsDialog) View() string {
	if !d.Show {
		return ""
	}
	t := theme.CurrentTheme()
	labelW := 0
	for _, f := range d.fields {
		if len(f.label) > labelW {
			labelW = len(f.label)
		}
	}
	labelStyle := lipgloss.NewStyle().Foreground(t.DialogText()).Bold(true)
	valStyle := lipgloss.NewStyle().Foreground(t.DialogAccent())
	helpStyle := lipgloss.NewStyle().Foreground(t.TextMuted()).Italic(true)
	cursorStyle := lipgloss.NewStyle().Foreground(t.DialogAccent()).Bold(true)

	var b strings.Builder
	for i, f := range d.fields {
		marker := "  "
		if i == d.cursor {
			marker = cursorStyle.Render("▸ ")
		}
		label := labelStyle.Render(padRight(f.label, labelW))
		val := valStyle.Render(f.get(d.overrides))
		b.WriteString(marker)
		b.WriteString(label)
		b.WriteString("  ")
		b.WriteString(val)
		b.WriteString("\n")
		if i == d.cursor && f.help != "" {
			b.WriteString("    ")
			b.WriteString(helpStyle.Render(f.help))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑↓ navigate · ←→ adjust · space/enter toggle · ctrl+s save · esc close"))
	if d.status != "" {
		b.WriteString("\n")
		statusStyle := lipgloss.NewStyle().Foreground(t.DialogAccent())
		b.WriteString(statusStyle.Render(d.status))
	}
	return d.BaseView(b.String(), d.Width, d.Height)
}

// ---- field editors ------------------------------------------------

func editProfile(d *SettingsDialog, key tea.KeyMsg) bool {
	if keyName(key) != "left" && keyName(key) != "right" && keyName(key) != "enter" && keyName(key) != "space" {
		return false
	}
	current := d.overrides.Profile
	if current == "" {
		current = "yolo"
	}
	idx := 0
	for i, p := range config.AvailableProfiles {
		if p == current {
			idx = i
			break
		}
	}
	switch keyName(key) {
	case "left":
		idx = (idx - 1 + len(config.AvailableProfiles)) % len(config.AvailableProfiles)
	default:
		idx = (idx + 1) % len(config.AvailableProfiles)
	}
	_ = config.ApplyProfile(d.overrides, config.AvailableProfiles[idx])
	return true
}

func editModel(d *SettingsDialog, key tea.KeyMsg) bool {
	if len(d.availableModels) == 0 {
		d.status = "model picker not populated — use /model to choose"
		return false
	}
	current := d.overrides.Basic.Model
	idx := 0
	for i, m := range d.availableModels {
		if m == current {
			idx = i
			break
		}
	}
	switch keyName(key) {
	case "left":
		idx = (idx - 1 + len(d.availableModels)) % len(d.availableModels)
	case "right", "enter", "space":
		idx = (idx + 1) % len(d.availableModels)
	default:
		return false
	}
	d.overrides.Basic.Model = d.availableModels[idx]
	return true
}

func editTheme(d *SettingsDialog, key tea.KeyMsg) bool {
	if len(d.availableThemes) == 0 {
		return false
	}
	current := d.overrides.Basic.Theme
	idx := 0
	for i, t := range d.availableThemes {
		if t == current {
			idx = i
			break
		}
	}
	switch keyName(key) {
	case "left":
		idx = (idx - 1 + len(d.availableThemes)) % len(d.availableThemes)
	case "right", "enter", "space":
		idx = (idx + 1) % len(d.availableThemes)
	default:
		return false
	}
	d.overrides.Basic.Theme = d.availableThemes[idx]
	return true
}

// makeFloatEdit returns a handler that nudges the field by step on
// ← / →, snapped to [min, max]. enter / space round-trips the value
// to the boundary (handy for "reset slider to 0").
func makeFloatEdit(min, max, step float64, target func(*config.UserOverrides) *float64) func(*SettingsDialog, tea.KeyMsg) bool {
	return func(d *SettingsDialog, key tea.KeyMsg) bool {
		p := target(d.overrides)
		switch keyName(key) {
		case "left":
			*p = clamp(*p-step, min, max)
		case "right":
			*p = clamp(*p+step, min, max)
		case "enter", "space":
			if *p == 0 {
				*p = max / 2
			} else {
				*p = 0
			}
		default:
			return false
		}
		return true
	}
}

func makeBoolEdit(target func(*config.UserOverrides) *bool) func(*SettingsDialog, tea.KeyMsg) bool {
	return func(d *SettingsDialog, key tea.KeyMsg) bool {
		if keyName(key) != "space" && keyName(key) != "enter" && keyName(key) != "left" && keyName(key) != "right" {
			return false
		}
		p := target(d.overrides)
		*p = !*p
		return true
	}
}

// editToolToggles is a placeholder — full sub-list rendering arrives
// with v2.0-8 (Advanced framework). For now, pressing enter on the
// Tools row clears all toggles or enables a curated default set so
// the user can see the field actually does something. Left/right
// cycle through "all off → defaults on → all on → all off".
func editToolToggles(d *SettingsDialog, key tea.KeyMsg) bool {
	if keyName(key) != "enter" && keyName(key) != "space" && keyName(key) != "left" && keyName(key) != "right" {
		return false
	}
	if d.overrides.Basic.Tools == nil {
		d.overrides.Basic.Tools = map[string]bool{}
	}
	enabled := 0
	for _, on := range d.overrides.Basic.Tools {
		if on {
			enabled++
		}
	}
	defaults := []string{"fs", "shell", "grep", "web", "patch", "browser_open"}
	switch {
	case enabled == 0:
		for _, t := range defaults {
			d.overrides.Basic.Tools[t] = true
		}
	case enabled > 0 && enabled < len(defaults)+1:
		// Cycle to "all on" by adding a sentinel for any registered
		// tool the dialog knows about. Without a tool registry plumb,
		// we just set every known default to true plus a wildcard
		// the future Advanced view will resolve.
		for k := range d.overrides.Basic.Tools {
			d.overrides.Basic.Tools[k] = true
		}
		d.overrides.Basic.Tools["*"] = true
	default:
		for k := range d.overrides.Basic.Tools {
			d.overrides.Basic.Tools[k] = false
		}
	}
	return true
}

// ---- helpers ------------------------------------------------------

func renderSlider(v, lo, hi float64) string {
	const width = 10
	if hi <= lo {
		return fmt.Sprintf("%.2f", v)
	}
	pos := int(((v - lo) / (hi - lo)) * float64(width))
	if pos < 0 {
		pos = 0
	}
	if pos > width {
		pos = width
	}
	bar := strings.Repeat("█", pos) + strings.Repeat("░", width-pos)
	pct := int((v - lo) / (hi - lo) * 100)
	return fmt.Sprintf("%s  %d%%", bar, pct)
}

func boolLabel(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// driftedFromProfile reports whether any field has been edited away
// from what its named profile would set. Used by the profile picker
// to render "(modified)" so the user knows the active state isn't
// pristine.
func (d *SettingsDialog) driftedFromProfile() bool {
	if d.overrides.Profile == "" {
		return false
	}
	pristine := &config.UserOverrides{}
	if err := config.ApplyProfile(pristine, d.overrides.Profile); err != nil {
		return false
	}
	return !overridesEqual(d.overrides, pristine)
}

// overridesEqual is a shallow value compare. Basic struct contains a
// map so we compare the scalar fields explicitly + the Tools map
// separately. Spot-check the most-likely-drift Advanced sections too.
func overridesEqual(a, b *config.UserOverrides) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Basic.Model != b.Basic.Model ||
		a.Basic.ContextBudget != b.Basic.ContextBudget ||
		a.Basic.Theme != b.Basic.Theme ||
		a.Basic.VimMode != b.Basic.VimMode ||
		a.Basic.CostCapMonthly != b.Basic.CostCapMonthly ||
		a.Basic.AutoCompactPercent != b.Basic.AutoCompactPercent ||
		a.Basic.ConfirmWrites != b.Basic.ConfirmWrites {
		return false
	}
	if !toolsEqual(a.Basic.Tools, b.Basic.Tools) {
		return false
	}
	if !scannerTogglesEqual(a.Advanced.Scanners, b.Advanced.Scanners) {
		return false
	}
	if a.Advanced.Persona != b.Advanced.Persona {
		return false
	}
	return true
}

func toolsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if a[k] != b[k] {
			return false
		}
	}
	return true
}

func scannerTogglesEqual(a, b config.ScannerToggles) bool {
	return a == b
}
