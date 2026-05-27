package dialog

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

const sampleDiff = `--- a/x
+++ b/x
@@ -1,3 +1,3 @@
 a
-b
+B
 c
`

func TestDiffDialog_ToggleSplit(t *testing.T) {
	d := NewDiffDialog()
	d.SetDiff("x", sampleDiff)
	d.Show = true
	if d.SplitMode {
		t.Fatal("default should be unified")
	}
	updated, _ := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if !updated.SplitMode {
		t.Error("`s` should toggle split mode on")
	}
	updated2, _ := updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if updated2.SplitMode {
		t.Error("second `s` should toggle split mode off")
	}
}

func TestDiffDialog_SplitViewRenders(t *testing.T) {
	d := NewDiffDialog()
	d.SetDiff("x", sampleDiff)
	d.Show = true
	d.SetSplitMode(true)
	out := d.View(120, 40)
	if !strings.Contains(out, "@@") {
		t.Errorf("expected hunk header in output: %q", out)
	}
	if !strings.Contains(out, "│") {
		t.Errorf("expected gutter character in side-by-side output")
	}
}

func TestRenderDiffSplit_FitsBudget(t *testing.T) {
	out := RenderDiffSplit("x", sampleDiff, 60)
	for _, line := range strings.Split(stripANSIDiff(out), "\n") {
		if len([]rune(line)) > 64 { // small slack for gutter
			t.Errorf("line exceeds budget: %q (%d)", line, len([]rune(line)))
		}
	}
}

func TestDiffDialog_EscClosesAndEmitsMsg(t *testing.T) {
	d := NewDiffDialog()
	d.Show = true
	updated, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.Show {
		t.Error("should hide on esc")
	}
	if cmd == nil {
		t.Fatal("should emit a close cmd")
	}
	if _, ok := cmd().(CloseDiffDialogMsg); !ok {
		t.Error("expected CloseDiffDialogMsg")
	}
}

func stripANSIDiff(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if r == 0x1b {
			in = true
			continue
		}
		if in {
			if r == 'm' {
				in = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
