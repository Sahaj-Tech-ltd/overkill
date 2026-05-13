// Package dialog — file mention picker (`@filename` overlay).
//
// FileMentionDialog is a fuzzy picker of files in the current cwd. It's opened
// from the chat page when the user types `@` in the editor. Up/Down navigate,
// Tab/Enter inserts the selection at the trigger position, Esc closes.
package dialog

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/theme"
)

// fileListCap caps the number of files we list to keep the UI snappy on large
// trees. opencode does the same trick.
const fileListCap = 1000

// FileMentionSelectedMsg is emitted when the user picks a path. The receiver
// is responsible for splicing it into the editor.
type FileMentionSelectedMsg struct {
	Path string
}

// CloseFileMentionMsg is emitted on Esc.
type CloseFileMentionMsg struct{}

// FileMentionDialog is the model.
type FileMentionDialog struct {
	Dialog
	all      []string
	filtered []string
	query    string
	cursor   int
	loaded   bool
	cwd      string
}

// NewFileMentionDialog returns a fresh, hidden dialog. Callers should call
// LoadFromCwd before showing.
func NewFileMentionDialog() FileMentionDialog {
	return FileMentionDialog{Dialog: Dialog{Title: "mention file"}}
}

// SetQuery sets the filter text (without the leading @).
func (d *FileMentionDialog) SetQuery(q string) {
	d.query = q
	d.filter()
	if d.cursor >= len(d.filtered) {
		d.cursor = max0(len(d.filtered) - 1)
	}
}

// Query returns the current filter text.
func (d *FileMentionDialog) Query() string { return d.query }

// LoadFromCwd populates the file list from `git ls-files`, falling back to
// recursive find. Subsequent calls are no-ops unless force=true.
func (d *FileMentionDialog) LoadFromCwd(cwd string, force bool) {
	if d.loaded && !force {
		return
	}
	d.cwd = cwd
	files, err := listGitFiles(cwd)
	if err != nil || len(files) == 0 {
		files = listFallback(cwd)
	}
	if len(files) > fileListCap {
		files = files[:fileListCap]
	}
	d.all = files
	d.loaded = true
	d.filter()
}

// IsLoaded reports whether the file cache has been populated.
func (d *FileMentionDialog) IsLoaded() bool { return d.loaded }

// All returns the full file list (used by tests).
func (d *FileMentionDialog) All() []string { return d.all }

// Filtered returns the current filtered slice.
func (d *FileMentionDialog) Filtered() []string { return d.filtered }

// Cursor returns the highlight index.
func (d *FileMentionDialog) Cursor() int { return d.cursor }

func (d *FileMentionDialog) filter() {
	if d.query == "" {
		d.filtered = append([]string(nil), d.all...)
		return
	}
	q := strings.ToLower(d.query)
	out := make([]string, 0, 32)
	for _, f := range d.all {
		if strings.Contains(strings.ToLower(f), q) {
			out = append(out, f)
		}
	}
	d.filtered = out
}

// Update handles dialog keys: up/down/tab/enter/esc and rune typing for query.
func (d FileMentionDialog) Update(msg tea.Msg) (FileMentionDialog, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, nil
	}
	switch k.String() {
	case "up", "ctrl+p":
		if d.cursor > 0 {
			d.cursor--
		}
	case "down", "ctrl+n":
		if d.cursor < len(d.filtered)-1 {
			d.cursor++
		}
	case "tab", "enter":
		if len(d.filtered) > 0 && d.cursor < len(d.filtered) {
			selected := d.filtered[d.cursor]
			d.Show = false
			return d, func() tea.Msg { return FileMentionSelectedMsg{Path: selected} }
		}
	case "esc":
		d.Show = false
		return d, func() tea.Msg { return CloseFileMentionMsg{} }
	case "backspace":
		if len(d.query) > 0 {
			d.query = d.query[:len(d.query)-1]
			d.filter()
			if d.cursor >= len(d.filtered) {
				d.cursor = max0(len(d.filtered) - 1)
			}
		}
	default:
		if len(k.Runes) > 0 {
			d.query += string(k.Runes)
			d.filter()
			d.cursor = 0
		}
	}
	return d, nil
}

// View renders the dialog box.
func (d FileMentionDialog) View(totalWidth, totalHeight int) string {
	if !d.Show {
		return ""
	}
	t := theme.CurrentTheme()
	hi := lipgloss.NewStyle().Foreground(t.Background()).Background(t.Accent()).Bold(true)
	row := lipgloss.NewStyle().Foreground(t.Text())

	var b strings.Builder
	b.WriteString("query: @" + d.query + "\n\n")
	if len(d.filtered) == 0 {
		b.WriteString("(no matches)")
		return d.BaseView(b.String(), totalWidth, totalHeight)
	}
	max := len(d.filtered)
	if max > 12 {
		max = 12
	}
	for i := 0; i < max; i++ {
		line := d.filtered[i]
		if i == d.cursor {
			b.WriteString(hi.Render("> " + line))
		} else {
			b.WriteString(row.Render("  " + line))
		}
		b.WriteString("\n")
	}
	if len(d.filtered) > max {
		b.WriteString(lipgloss.NewStyle().Foreground(t.TextMuted()).
			Render("  …" + itoaQuick(len(d.filtered)-max) + " more"))
	}
	return d.BaseView(strings.TrimRight(b.String(), "\n"), totalWidth, totalHeight)
}

// listGitFiles runs `git ls-files` to enumerate tracked files relative to cwd.
func listGitFiles(cwd string) ([]string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-files")
	cmd.Dir = cwd
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	files := make([]string, 0, 256)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			files = append(files, line)
		}
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// listFallback walks the tree, skipping .git, capping at fileListCap entries.
func listFallback(cwd string) []string {
	files := make([]string, 0, 256)
	_ = filepath.WalkDir(cwd, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(cwd, path)
		if err != nil {
			return nil
		}
		files = append(files, rel)
		if len(files) >= fileListCap {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(files)
	return files
}

func itoaQuick(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
