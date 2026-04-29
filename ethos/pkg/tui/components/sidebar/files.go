package sidebar

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type fileEntry struct {
	Path    string
	Added   int
	Deleted int
	Status  string
}

type FilesPanel struct {
	files  []fileEntry
	width  int
	height int
}

func NewFilesPanel() FilesPanel {
	return FilesPanel{}
}

func (f *FilesPanel) Name() string {
	return "Files"
}

func (f *FilesPanel) UpdateFiles(files []fileEntry) {
	f.files = make([]fileEntry, len(files))
	copy(f.files, files)
	sort.SliceStable(f.files, func(i, j int) bool {
		return abs(f.files[i].Added)+abs(f.files[i].Deleted) > abs(f.files[j].Added)+abs(f.files[j].Deleted)
	})
}

func (f *FilesPanel) View(width, height int) string {
	f.width = width
	f.height = height

	if len(f.files) == 0 {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6c7086")).
			Render("No file changes")
	}

	var b strings.Builder

	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#89b4fa")).
		Render("File Changes")
	b.WriteString(header)
	b.WriteByte('\n')

	maxFiles := 20
	overflow := 0
	files := f.files
	if len(files) > maxFiles {
		overflow = len(files) - maxFiles
		files = files[:maxFiles]
	}

	maxPathLen := width - 12
	if maxPathLen < 4 {
		maxPathLen = 4
	}

	for _, fe := range files {
		displayPath := fe.Path
		if len(displayPath) > maxPathLen {
			displayPath = "…" + displayPath[len(displayPath)-maxPathLen+1:]
		}

		if fe.Status == "binary" {
			b.WriteString(displayPath)
			b.WriteByte(' ')
			b.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("#6c7086")).
				Render("(binary)"))
			b.WriteByte('\n')
			continue
		}

		addedColor := lipgloss.Color("#a6e3a1")
		deletedColor := lipgloss.Color("#f38ba8")
		modifiedColor := lipgloss.Color("#f9e2af")

		b.WriteString(displayPath)
		b.WriteByte(' ')

		if fe.Added > 0 {
			pathColor := addedColor
			if fe.Status == "modified" {
				pathColor = modifiedColor
			}
			b.WriteString(lipgloss.NewStyle().Foreground(pathColor).Render(
				"+" + itoa(fe.Added)))
			b.WriteByte(' ')
		}

		if fe.Deleted > 0 {
			b.WriteString(lipgloss.NewStyle().Foreground(deletedColor).Render(
				"-" + itoa(fe.Deleted)))
			b.WriteByte(' ')
		}

		if fe.Added == 0 && fe.Deleted == 0 {
			b.WriteString(lipgloss.NewStyle().Foreground(modifiedColor).Render(
				"~0"))
		}

		b.WriteByte('\n')
	}

	if overflow > 0 {
		b.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6c7086")).
			Render("+" + itoa(overflow) + " more"))
		b.WriteByte('\n')
	}

	return strings.TrimRight(b.String(), "\n")
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
