package dialog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func newOpenDialog(t *testing.T) (*SettingsDialog, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "user.yaml")
	d := NewSettingsDialog(path, config.DefaultUserOverrides())
	d.SetSize(80, 24)
	d.ShowDialog()
	return d, path
}

func TestSettings_NavigationMovesCursor(t *testing.T) {
	d, _ := newOpenDialog(t)
	if d.cursor != 0 {
		t.Fatalf("cursor starts at %d, want 0", d.cursor)
	}
	d, _ = d.Update(keyMsg("down"))
	d, _ = d.Update(keyMsg("down"))
	if d.cursor != 2 {
		t.Errorf("after 2× down: cursor = %d, want 2", d.cursor)
	}
	d, _ = d.Update(keyMsg("up"))
	if d.cursor != 1 {
		t.Errorf("after up: cursor = %d, want 1", d.cursor)
	}
}

func TestSettings_VimModeToggle(t *testing.T) {
	d, _ := newOpenDialog(t)
	// Move to Vim mode row.
	for i := 0; i < len(d.fields); i++ {
		if d.fields[i].label == "Vim mode" {
			d.cursor = i
			break
		}
	}
	if d.overrides.Basic.VimMode {
		t.Fatal("VimMode should start false on yolo")
	}
	d, _ = d.Update(keyMsg("space"))
	if !d.overrides.Basic.VimMode {
		t.Error("space did not toggle VimMode true")
	}
	d, _ = d.Update(keyMsg("space"))
	if d.overrides.Basic.VimMode {
		t.Error("space did not toggle VimMode back to false")
	}
}

func TestSettings_SliderAdjusts(t *testing.T) {
	d, _ := newOpenDialog(t)
	// Move to context budget row.
	for i := 0; i < len(d.fields); i++ {
		if d.fields[i].label == "Context budget" {
			d.cursor = i
			break
		}
	}
	before := d.overrides.Basic.ContextBudget
	d, _ = d.Update(keyMsg("right"))
	d, _ = d.Update(keyMsg("right"))
	if d.overrides.Basic.ContextBudget <= before {
		t.Errorf("right ×2 should raise budget; before=%v after=%v", before, d.overrides.Basic.ContextBudget)
	}
}

func TestSettings_ProfileCyclesAndAppliesPreset(t *testing.T) {
	d, _ := newOpenDialog(t)
	d.cursor = 0 // Profile row
	if d.overrides.Profile != "yolo" {
		t.Fatalf("start profile = %q, want yolo", d.overrides.Profile)
	}
	d, _ = d.Update(keyMsg("right"))
	if d.overrides.Profile != "default" {
		t.Errorf("after one cycle: profile = %q, want default", d.overrides.Profile)
	}
	// Default profile turns command scanner on.
	if !d.overrides.Advanced.Scanners.Command.Enabled {
		t.Error("default profile should enable command scanner")
	}
	d, _ = d.Update(keyMsg("right"))
	if d.overrides.Profile != "paranoid" {
		t.Errorf("after two cycles: profile = %q, want paranoid", d.overrides.Profile)
	}
}

func TestSettings_SaveRoundTrip(t *testing.T) {
	d, path := newOpenDialog(t)
	// Make a real change so dirty triggers.
	d.cursor = 0
	d, _ = d.Update(keyMsg("right"))           // profile → default
	d, cmd := d.Update(keyMsg("ctrl+s"))       // save
	if cmd == nil {
		t.Fatal("ctrl+s should emit a SaveResultMsg cmd")
	}
	got := cmd()
	res, ok := got.(SaveResultMsg)
	if !ok {
		t.Fatalf("expected SaveResultMsg, got %T", got)
	}
	if res.Err != nil {
		t.Fatalf("save error: %v", res.Err)
	}
	if !res.Saved {
		t.Fatal("Saved=false")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("user.yaml not written: %v", err)
	}
	if d.Show {
		t.Error("dialog should close after successful save")
	}

	// Loading the file back should yield the same Profile.
	loaded, err := config.LoadUserOverrides(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Profile != "default" {
		t.Errorf("loaded profile = %q, want default", loaded.Profile)
	}
}

func TestSettings_DirtyPromptOnEsc(t *testing.T) {
	d, _ := newOpenDialog(t)
	d.cursor = 0
	d, _ = d.Update(keyMsg("right")) // make dirty
	if !d.IsDirty() {
		t.Fatal("expected dirty after profile change")
	}
	d, _ = d.Update(keyMsg("esc"))
	if !d.Show {
		t.Error("first esc on dirty dialog should NOT close")
	}
	if !strings.Contains(d.status, "unsaved") {
		t.Errorf("first esc should warn; status=%q", d.status)
	}
	d, _ = d.Update(keyMsg("esc"))
	if d.Show {
		t.Error("second esc should close")
	}
}

func TestSettings_DriftIndicator(t *testing.T) {
	d, _ := newOpenDialog(t)
	// Start clean — no drift suffix.
	row := d.fields[0].get(d.overrides)
	if strings.Contains(row, "modified") {
		t.Errorf("pristine yolo should not show drift; got %q", row)
	}
	// Toggle vim mode (yolo doesn't set vim mode → flipping it to
	// true diverges from the pristine yolo).
	d.overrides.Basic.VimMode = true
	row = d.fields[0].get(d.overrides)
	if !strings.Contains(row, "modified") {
		t.Errorf("after editing field: drift suffix missing; got %q", row)
	}
}

func TestSettings_ViewRenders(t *testing.T) {
	d, _ := newOpenDialog(t)
	out := d.View()
	if !strings.Contains(out, "Profile") || !strings.Contains(out, "Model") || !strings.Contains(out, "Vim mode") {
		t.Errorf("View missing expected labels:\n%s", out)
	}
	if !strings.Contains(out, "↑↓") {
		t.Error("View should include nav legend")
	}
}

func TestSettings_HiddenIgnoresKeys(t *testing.T) {
	d, _ := newOpenDialog(t)
	d.HideDialog()
	before := d.cursor
	d, _ = d.Update(keyMsg("down"))
	if d.cursor != before {
		t.Error("hidden dialog should not consume keys")
	}
}

func TestSettings_TabSwitchesBasicAdvanced(t *testing.T) {
	d, _ := newOpenDialog(t)
	if d.tab != tabBasic {
		t.Fatalf("default tab = %v, want tabBasic", d.tab)
	}
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab})
	if d.tab != tabAdvanced {
		t.Errorf("after Tab: tab = %v, want tabAdvanced", d.tab)
	}
	if !strings.Contains(d.Title, "Advanced") {
		t.Errorf("title should reflect tab: %q", d.Title)
	}
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab})
	if d.tab != tabBasic {
		t.Errorf("after second Tab: tab = %v, want tabBasic", d.tab)
	}
}

func TestSettings_AdvancedSectionNavigation(t *testing.T) {
	d, _ := newOpenDialog(t)
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab}) // → Advanced
	if d.sectionCursor != 0 {
		t.Fatalf("section cursor starts at %d, want 0", d.sectionCursor)
	}
	startSection := d.sections[d.sectionCursor].id
	d, _ = d.Update(keyMsg("]"))
	if d.sectionCursor != 1 {
		t.Errorf("] should advance section; got cursor=%d", d.sectionCursor)
	}
	if d.sections[d.sectionCursor].id == startSection {
		t.Errorf("section id didn't change after ]")
	}
	d, _ = d.Update(keyMsg("["))
	if d.sectionCursor != 0 {
		t.Errorf("[ should retreat section; got cursor=%d", d.sectionCursor)
	}
}

func TestSettings_AdvancedScannerToggle(t *testing.T) {
	d, _ := newOpenDialog(t)
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab}) // → Advanced
	if d.sections[d.sectionCursor].id != "scanners" {
		t.Fatalf("expected scanners section first; got %q", d.sections[d.sectionCursor].id)
	}
	if d.overrides.Advanced.Scanners.Command.Enabled {
		t.Fatal("yolo default: command scanner should start disabled")
	}
	d, _ = d.Update(keyMsg("space"))
	if !d.overrides.Advanced.Scanners.Command.Enabled {
		t.Error("space should toggle command scanner on")
	}
}

func TestSettings_AdvancedFieldCursorResetsOnSectionChange(t *testing.T) {
	d, _ := newOpenDialog(t)
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab})
	d, _ = d.Update(keyMsg("down"))
	if d.sectionFieldCursor != 1 {
		t.Errorf("expected field cursor=1; got %d", d.sectionFieldCursor)
	}
	d, _ = d.Update(keyMsg("]"))
	if d.sectionFieldCursor != 0 {
		t.Errorf("field cursor should reset on section change; got %d", d.sectionFieldCursor)
	}
}

func TestSettings_AdvancedRenderIncludesTabStrip(t *testing.T) {
	d, _ := newOpenDialog(t)
	out := d.View()
	if !strings.Contains(out, "Basic") || !strings.Contains(out, "Advanced") {
		t.Errorf("tab strip missing: %q", out)
	}
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab})
	out = d.View()
	if !strings.Contains(out, "Scanners") {
		t.Errorf("advanced view should show section name; got: %s", out)
	}
}

func TestSettings_PersonaEnumCycle(t *testing.T) {
	d, _ := newOpenDialog(t)
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab})
	for d.sections[d.sectionCursor].id != "persona" && d.sectionCursor < len(d.sections)-1 {
		d, _ = d.Update(keyMsg("]"))
	}
	if d.sections[d.sectionCursor].id != "persona" {
		t.Fatal("persona section not found")
	}
	if d.overrides.Advanced.Persona.Tone != "" {
		t.Fatal("Tone should start unset")
	}
	d, _ = d.Update(keyMsg("right"))
	if d.overrides.Advanced.Persona.Tone == "" {
		t.Error("right should advance the Tone enum")
	}
}

func TestSettings_TelemetryBoolPtrCycle(t *testing.T) {
	d, _ := newOpenDialog(t)
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab})
	for d.sections[d.sectionCursor].id != "telemetry" && d.sectionCursor < len(d.sections)-1 {
		d, _ = d.Update(keyMsg("]"))
	}
	if d.sections[d.sectionCursor].id != "telemetry" {
		t.Fatal("telemetry section not found")
	}
	// EventLog starts at yolo default (false explicitly). Cycle:
	// (default) → on → off → (default). Tests that the editor flips
	// through all three states.
	startPtr := d.overrides.Advanced.Telemetry.EventLog
	d, _ = d.Update(keyMsg("space"))
	if d.overrides.Advanced.Telemetry.EventLog == startPtr {
		t.Error("first space should change EventLog state")
	}
}
