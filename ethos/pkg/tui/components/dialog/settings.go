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

// tabKind picks which top-level tab the dialog is on. Tab cycles
// between them.
type tabKind int

const (
	tabBasic tabKind = iota
	tabAdvanced
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

	// tab is the active top-level tab.
	tab tabKind

	// fields drives the Basic tab. Built once at New time; the
	// cursor moves over it.
	fields []settingsField
	cursor int

	// sections / sectionCursor / sectionFieldCursor drive Advanced.
	// One section is "active" at a time; ←/→ pages between sections,
	// ↑/↓ navigates fields within the active section.
	sections           []advancedSection
	sectionCursor      int // which section is visible
	sectionFieldCursor int // which row within the visible section

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

// advancedSection is one card in the Advanced tab — a named group of
// settingsField rows sharing a theme (Scanners, Persona, Telemetry,
// etc.). Each card defines its own field table; the dialog reuses
// the existing settingsField shape so the editor code is shared.
type advancedSection struct {
	id     string
	title  string
	help   string
	fields []settingsField
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
	d.sections = buildAdvancedSections()
	return d
}

// buildAdvancedSections wires the Advanced-tab cards. Initial cut
// surfaces Scanners, Compaction, Persona, and Telemetry — the
// settings most users want after the Basic tab. The other 7
// sub-sections from settings-design.md land in follow-up PRs.
func buildAdvancedSections() []advancedSection {
	return []advancedSection{
		{
			id:    "scanners",
			title: "Scanners",
			help:  "defense-in-depth checks on every tool call",
			fields: []settingsField{
				{
					label: "Command scanner",
					help:  "blocks dangerous shell verbs (rm -rf /, mkfs, etc.)",
					kind:  kindBool,
					get:   func(u *config.UserOverrides) string { return boolLabel(u.Advanced.Scanners.Command.Enabled) },
					edit:  makeBoolEdit(func(u *config.UserOverrides) *bool { return &u.Advanced.Scanners.Command.Enabled }),
				},
				{
					label: "Injection scanner",
					help:  "catches 'ignore previous instructions' patterns in tool inputs",
					kind:  kindBool,
					get:   func(u *config.UserOverrides) string { return boolLabel(u.Advanced.Scanners.Injection.Enabled) },
					edit:  makeBoolEdit(func(u *config.UserOverrides) *bool { return &u.Advanced.Scanners.Injection.Enabled }),
				},
				{
					label: "Prompt-injection browser",
					help:  "annotates web fetches that try to override system prompt",
					kind:  kindBool,
					get:   func(u *config.UserOverrides) string { return boolLabel(u.Advanced.Scanners.PromptInjectBrowser.Enabled) },
					edit:  makeBoolEdit(func(u *config.UserOverrides) *bool { return &u.Advanced.Scanners.PromptInjectBrowser.Enabled }),
				},
			},
		},
		{
			id:    "compaction",
			title: "Compaction",
			help:  "automatic context summarisation when the budget is tight",
			fields: []settingsField{
				{
					label: "Compaction model",
					help:  "model used for the summary LLM call (empty = gpt-4o-mini)",
					kind:  kindModel,
					get: func(u *config.UserOverrides) string {
						if u.Advanced.Compaction.Model == "" {
							return "(default)"
						}
						return u.Advanced.Compaction.Model
					},
					edit: editCompactionModel,
				},
				{
					label: "Soft threshold",
					help:  "trigger compaction at this fraction of the context window",
					kind:  kindSlider,
					get: func(u *config.UserOverrides) string {
						v := u.Advanced.Compaction.SoftThreshold
						if v == 0 {
							v = 0.5
						}
						return renderSlider(v, 0, 1)
					},
					edit: makeFloatEdit(0, 1, 0.05, func(u *config.UserOverrides) *float64 { return &u.Advanced.Compaction.SoftThreshold }),
				},
				{
					label: "Hard threshold",
					help:  "force compaction at this fraction; 0 = use baked default",
					kind:  kindSlider,
					get: func(u *config.UserOverrides) string {
						v := u.Advanced.Compaction.HardThreshold
						if v == 0 {
							v = 0.95
						}
						return renderSlider(v, 0, 1)
					},
					edit: makeFloatEdit(0, 1, 0.05, func(u *config.UserOverrides) *float64 { return &u.Advanced.Compaction.HardThreshold }),
				},
				{
					label: "Preserve last N turns",
					help:  "messages near the tail kept verbatim regardless of compaction",
					kind:  kindFloat,
					get: func(u *config.UserOverrides) string {
						return fmt.Sprintf("%d", u.Advanced.Compaction.PreserveLast)
					},
					edit: makeIntEdit(0, 100, 1, func(u *config.UserOverrides) *int { return &u.Advanced.Compaction.PreserveLast }),
				},
			},
		},
		{
			id:    "persona",
			title: "Persona",
			help:  "long-lived tone + style appended to every system prompt",
			fields: []settingsField{
				{
					label: "Tone",
					help:  "terse / normal / verbose (← / → cycles)",
					kind:  kindEnumTheme,
					get: func(u *config.UserOverrides) string {
						if u.Advanced.Persona.Tone == "" {
							return "(default)"
						}
						return u.Advanced.Persona.Tone
					},
					edit: makeEnumEdit(
						[]string{"", "terse", "normal", "verbose"},
						func(u *config.UserOverrides) *string { return &u.Advanced.Persona.Tone },
					),
				},
				{
					label: "Style",
					help:  "senior / pair / tutor / brutal",
					kind:  kindEnumTheme,
					get: func(u *config.UserOverrides) string {
						if u.Advanced.Persona.Style == "" {
							return "(default)"
						}
						return u.Advanced.Persona.Style
					},
					edit: makeEnumEdit(
						[]string{"", "senior", "pair", "tutor", "brutal"},
						func(u *config.UserOverrides) *string { return &u.Advanced.Persona.Style },
					),
				},
			},
		},
		{
			id:    "telemetry",
			title: "Telemetry",
			help:  "what the agent records about its own work",
			fields: []settingsField{
				{
					label: "Event log",
					help:  "every tool call + agent message",
					kind:  kindBool,
					get:   func(u *config.UserOverrides) string { return ptrBoolLabel(u.Advanced.Telemetry.EventLog) },
					edit:  makeBoolPtrEdit(func(u *config.UserOverrides) **bool { return &u.Advanced.Telemetry.EventLog }),
				},
				{
					label: "Flight recorder",
					help:  "audit-trail of last resort; crash-safe",
					kind:  kindBool,
					get:   func(u *config.UserOverrides) string { return ptrBoolLabel(u.Advanced.Telemetry.FlightRecorder) },
					edit:  makeBoolPtrEdit(func(u *config.UserOverrides) **bool { return &u.Advanced.Telemetry.FlightRecorder }),
				},
				{
					label: "Verify receipt chain on boot",
					help:  "checks the cryptographic audit chain hasn't been tampered with",
					kind:  kindBool,
					get:   func(u *config.UserOverrides) string { return ptrBoolLabel(u.Advanced.Telemetry.VerifyOnBoot) },
					edit:  makeBoolPtrEdit(func(u *config.UserOverrides) **bool { return &u.Advanced.Telemetry.VerifyOnBoot }),
				},
				{
					label: "Retention (days)",
					help:  "0 = keep forever; otherwise prune older entries",
					kind:  kindFloat,
					get: func(u *config.UserOverrides) string {
						if u.Advanced.Telemetry.RetentionDays == 0 {
							return "forever"
						}
						return fmt.Sprintf("%d days", u.Advanced.Telemetry.RetentionDays)
					},
					edit: makeIntEdit(0, 3650, 30, func(u *config.UserOverrides) *int { return &u.Advanced.Telemetry.RetentionDays }),
				},
			},
		},
	}
}

// editCompactionModel is identical to editModel but targets the
// compaction sub-config instead of basic.Model. Inlined for clarity
// because the cycling target differs.
func editCompactionModel(d *SettingsDialog, key tea.KeyMsg) bool {
	if len(d.availableModels) == 0 {
		d.status = "model picker not populated"
		return false
	}
	current := d.overrides.Advanced.Compaction.Model
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
	d.overrides.Advanced.Compaction.Model = d.availableModels[idx]
	return true
}

// makeIntEdit mirrors makeFloatEdit for int-typed sliders.
func makeIntEdit(min, max, step int, target func(*config.UserOverrides) *int) func(*SettingsDialog, tea.KeyMsg) bool {
	return func(d *SettingsDialog, key tea.KeyMsg) bool {
		p := target(d.overrides)
		switch keyName(key) {
		case "left":
			n := *p - step
			if n < min {
				n = min
			}
			*p = n
		case "right":
			n := *p + step
			if n > max {
				n = max
			}
			*p = n
		case "enter", "space":
			if *p == 0 {
				*p = (max + min) / 2
			} else {
				*p = 0
			}
		default:
			return false
		}
		return true
	}
}

// makeEnumEdit returns a handler that cycles a string field through
// a fixed list of options. Empty string at index 0 represents
// "(default)" — the user can return to the no-override state.
func makeEnumEdit(options []string, target func(*config.UserOverrides) *string) func(*SettingsDialog, tea.KeyMsg) bool {
	return func(d *SettingsDialog, key tea.KeyMsg) bool {
		p := target(d.overrides)
		idx := 0
		for i, o := range options {
			if o == *p {
				idx = i
				break
			}
		}
		switch keyName(key) {
		case "left":
			idx = (idx - 1 + len(options)) % len(options)
		case "right", "enter", "space":
			idx = (idx + 1) % len(options)
		default:
			return false
		}
		*p = options[idx]
		return true
	}
}

// makeBoolPtrEdit handles *bool fields (Telemetry, etc.) where nil
// means "use parent layer's value" and the user cycles
// "(default) → on → off → (default)" so they can reset to inherited.
func makeBoolPtrEdit(target func(*config.UserOverrides) **bool) func(*SettingsDialog, tea.KeyMsg) bool {
	return func(d *SettingsDialog, key tea.KeyMsg) bool {
		if keyName(key) != "space" && keyName(key) != "enter" && keyName(key) != "left" && keyName(key) != "right" {
			return false
		}
		pp := target(d.overrides)
		switch {
		case *pp == nil:
			t := true
			*pp = &t
		case **pp:
			f := false
			*pp = &f
		default:
			*pp = nil
		}
		return true
	}
}

func ptrBoolLabel(p *bool) string {
	if p == nil {
		return "(default)"
	}
	if *p {
		return "on"
	}
	return "off"
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
	case "tab":
		// Cycle Basic → Advanced → Basic.
		if d.tab == tabBasic {
			d.tab = tabAdvanced
			d.Title = "Settings — Advanced"
		} else {
			d.tab = tabBasic
			d.Title = "Settings — Basic"
		}
		return d, nil
	}
	// Per-tab dispatch.
	switch d.tab {
	case tabBasic:
		return d.updateBasic(key)
	case tabAdvanced:
		return d.updateAdvanced(key)
	}
	return d, nil
}

// updateBasic handles key navigation + editing on the Basic tab.
func (d *SettingsDialog) updateBasic(key tea.KeyMsg) (*SettingsDialog, tea.Cmd) {
	switch keyName(key) {
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

// updateAdvanced handles key navigation + editing on the Advanced
// tab. ←/→ pages between sub-sections; ↑/↓ moves within the active
// section's field list; any other key is passed to the section's
// field editor.
func (d *SettingsDialog) updateAdvanced(key tea.KeyMsg) (*SettingsDialog, tea.Cmd) {
	if len(d.sections) == 0 {
		return d, nil
	}
	section := &d.sections[d.sectionCursor]
	switch keyName(key) {
	case "up", "k":
		if d.sectionFieldCursor > 0 {
			d.sectionFieldCursor--
		}
		return d, nil
	case "down", "j":
		if d.sectionFieldCursor < len(section.fields)-1 {
			d.sectionFieldCursor++
		}
		return d, nil
	case "pgup", "[":
		if d.sectionCursor > 0 {
			d.sectionCursor--
			d.sectionFieldCursor = 0
		}
		return d, nil
	case "pgdown", "]":
		if d.sectionCursor < len(d.sections)-1 {
			d.sectionCursor++
			d.sectionFieldCursor = 0
		}
		return d, nil
	}
	if d.sectionFieldCursor >= 0 && d.sectionFieldCursor < len(section.fields) {
		section.fields[d.sectionFieldCursor].edit(d, key)
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
	helpStyle := lipgloss.NewStyle().Foreground(t.TextMuted()).Italic(true)
	statusStyle := lipgloss.NewStyle().Foreground(t.DialogAccent())

	var b strings.Builder
	// Tab strip: highlight active.
	b.WriteString(d.renderTabStrip())
	b.WriteString("\n\n")

	switch d.tab {
	case tabBasic:
		b.WriteString(d.renderBasic())
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("↑↓ navigate · ←→ adjust · space/enter toggle · tab → Advanced · ctrl+s save · esc close"))
	case tabAdvanced:
		b.WriteString(d.renderAdvanced())
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("↑↓ field · [ ] section · ←→ adjust · space/enter toggle · tab → Basic · ctrl+s save · esc close"))
	}

	if d.status != "" {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(d.status))
	}
	return d.BaseView(b.String(), d.Width, d.Height)
}

// renderTabStrip draws the [Basic] [Advanced] header.
func (d *SettingsDialog) renderTabStrip() string {
	t := theme.CurrentTheme()
	activeStyle := lipgloss.NewStyle().Foreground(t.DialogAccent()).Bold(true).
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(t.DialogAccent())
	inactiveStyle := lipgloss.NewStyle().Foreground(t.TextMuted())

	label := func(name string, active bool) string {
		if active {
			return activeStyle.Render(" " + name + " ")
		}
		return inactiveStyle.Render(" " + name + " ")
	}
	return label("Basic", d.tab == tabBasic) + "  " + label("Advanced", d.tab == tabAdvanced)
}

// renderBasic draws the Basic-tab field list (the v2.0-7 surface).
func (d *SettingsDialog) renderBasic() string {
	return d.renderFieldList(d.fields, d.cursor)
}

// renderAdvanced draws the section breadcrumb + the active section's
// field list. Uses the same row renderer as Basic so the visual
// language stays consistent.
func (d *SettingsDialog) renderAdvanced() string {
	t := theme.CurrentTheme()
	helpStyle := lipgloss.NewStyle().Foreground(t.TextMuted()).Italic(true)
	titleStyle := lipgloss.NewStyle().Foreground(t.DialogAccent()).Bold(true)
	if len(d.sections) == 0 {
		return helpStyle.Render("(no advanced sections wired yet)")
	}
	section := d.sections[d.sectionCursor]
	var b strings.Builder
	// Breadcrumb: « Scanners (1/4) »
	crumbStyle := lipgloss.NewStyle().Foreground(t.TextMuted())
	prev := "[ prev"
	next := "next ]"
	if d.sectionCursor == 0 {
		prev = lipgloss.NewStyle().Foreground(t.TextMuted()).Render("[ prev")
	}
	if d.sectionCursor == len(d.sections)-1 {
		next = lipgloss.NewStyle().Foreground(t.TextMuted()).Render("next ]")
	}
	b.WriteString(crumbStyle.Render(prev))
	b.WriteString("   ")
	b.WriteString(titleStyle.Render(fmt.Sprintf("%s  (%d/%d)", section.title, d.sectionCursor+1, len(d.sections))))
	b.WriteString("   ")
	b.WriteString(crumbStyle.Render(next))
	b.WriteString("\n")
	if section.help != "" {
		b.WriteString(helpStyle.Render(section.help))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(d.renderFieldList(section.fields, d.sectionFieldCursor))
	return b.String()
}

// renderFieldList is the shared row renderer used by both tabs. Pulls
// the longest label for column alignment and highlights the cursor
// row with a ▸ marker + inline help.
func (d *SettingsDialog) renderFieldList(fields []settingsField, cursor int) string {
	t := theme.CurrentTheme()
	labelW := 0
	for _, f := range fields {
		if len(f.label) > labelW {
			labelW = len(f.label)
		}
	}
	labelStyle := lipgloss.NewStyle().Foreground(t.DialogText()).Bold(true)
	valStyle := lipgloss.NewStyle().Foreground(t.DialogAccent())
	helpStyle := lipgloss.NewStyle().Foreground(t.TextMuted()).Italic(true)
	cursorStyle := lipgloss.NewStyle().Foreground(t.DialogAccent()).Bold(true)

	var b strings.Builder
	for i, f := range fields {
		marker := "  "
		if i == cursor {
			marker = cursorStyle.Render("▸ ")
		}
		label := labelStyle.Render(padRight(f.label, labelW))
		val := valStyle.Render(f.get(d.overrides))
		b.WriteString(marker)
		b.WriteString(label)
		b.WriteString("  ")
		b.WriteString(val)
		b.WriteString("\n")
		if i == cursor && f.help != "" {
			b.WriteString("    ")
			b.WriteString(helpStyle.Render(f.help))
			b.WriteString("\n")
		}
	}
	return b.String()
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
