// Package tui — /walls command handler.
//
// Surfaces the architecture wall (Wall 2 of the three-wall adversarial
// review system) against the current working tree. Best-effort: when
// no findings exist we toast a success and exit; non-fatal scan
// failures emit an error toast rather than panicking.
package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"
)

// runWalls executes the architecture wall against the current working
// tree and emits a one-line summary. Test-quality and ouroboros walls
// require a code/test pair so they're not run here without explicit
// input.
func (m *appModel) runWalls() tea.Cmd {
	cwd, err := os.Getwd()
	if err != nil {
		return m.toastCmd("walls: getcwd: "+err.Error(), "error")
	}
	files := map[string]string{}
	_ = filepath.WalkDir(cwd, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".ts" && ext != ".tsx" && ext != ".py" {
			return nil
		}
		rel, _ := filepath.Rel(cwd, path)
		if strings.HasPrefix(rel, "vendor/") || strings.HasPrefix(rel, "node_modules/") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if len(b) > 64*1024 {
			return nil
		}
		files[rel] = string(b)
		return nil
	})
	wall := walls.NewArchitectureWall(walls.ArchitectureConfig{})
	res, err := wall.Check(context.Background(), files)
	if err != nil {
		return m.toastCmd("walls: "+err.Error(), "error")
	}
	if res == nil {
		return m.toastCmd("walls: no findings", "success")
	}
	return m.toastCmd(fmt.Sprintf("architecture wall: severity=%s passed=%v details=%d", res.Severity, res.Passed, len(res.Details)), wallToastKind(res.Severity))
}

func wallToastKind(s walls.Severity) string {
	switch s {
	case walls.SeverityBlock:
		return "error"
	case walls.SeverityWarning:
		return "warning"
	default:
		return "info"
	}
}
