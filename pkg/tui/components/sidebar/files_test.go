package sidebar

import (
	"fmt"
	"strings"
	"testing"
)

func TestFilePanel_Render(t *testing.T) {
	p := NewFilesPanel()
	p.UpdateFiles([]fileEntry{
		{Path: "main.go", Added: 5, Deleted: 2, Status: "modified"},
	})
	v := p.View(30, 15)
	if !containsStr(v, "main.go") || !containsStr(v, "+5") || !containsStr(v, "-2") {
		t.Error("should show file and stats")
	}
}

func TestFilePanel_NoChanges(t *testing.T) {
	p := NewFilesPanel()
	v := p.View(30, 15)
	if !containsStr(v, "No file changes") {
		t.Error("should show empty state")
	}
}

func TestFilePanel_LongPath(t *testing.T) {
	p := NewFilesPanel()
	longPath := "this/is/a/very/long/path/to/a/file/that/should/be/truncated.go"
	p.UpdateFiles([]fileEntry{{Path: longPath, Added: 1, Deleted: 0, Status: "added"}})
	v := p.View(20, 10)
	if containsStr(v, longPath) {
		t.Error("path should be truncated")
	}
}

func TestFilePanel_ColorCoding(t *testing.T) {
	p := NewFilesPanel()
	p.UpdateFiles([]fileEntry{
		{Path: "a.go", Added: 10, Status: "added"},
		{Path: "b.go", Deleted: 5, Status: "deleted"},
		{Path: "c.go", Added: 1, Deleted: 1, Status: "modified"},
	})
	v := p.View(30, 10)
	if !containsStr(v, "a.go") {
		t.Error("missing a.go")
	}
	if !containsStr(v, "b.go") {
		t.Error("missing b.go")
	}
	if !containsStr(v, "c.go") {
		t.Error("missing c.go")
	}
}

func TestFilePanel_SortByChange(t *testing.T) {
	p := NewFilesPanel()
	p.UpdateFiles([]fileEntry{
		{Path: "small.go", Added: 1, Deleted: 1},
		{Path: "big.go", Added: 20, Deleted: 0},
	})
	v := p.View(30, 10)
	idxBig := indexOf(v, "big.go")
	idxSmall := indexOf(v, "small.go")
	if idxBig > idxSmall {
		t.Error("big changes should come first")
	}
}

func TestFilePanel_BinaryFiles(t *testing.T) {
	p := NewFilesPanel()
	p.UpdateFiles([]fileEntry{{Path: "image.png", Status: "binary"}})
	v := p.View(30, 10)
	if !containsStr(v, "(binary)") {
		t.Error("should show binary label")
	}
}

func TestFilePanel_MaxFiles(t *testing.T) {
	p := NewFilesPanel()
	files := make([]fileEntry, 25)
	for i := range files {
		files[i] = fileEntry{Path: fmt.Sprintf("file%d.go", i), Added: 1}
	}
	p.UpdateFiles(files)
	v := p.View(30, 30)
	if !containsStr(v, "+5 more") {
		t.Error("should show overflow")
	}
}

func TestFilePanel_Refresh(t *testing.T) {
	p := NewFilesPanel()
	p.UpdateFiles([]fileEntry{{Path: "a.go", Added: 1}})
	v1 := p.View(30, 10)
	_ = v1
	p.UpdateFiles([]fileEntry{{Path: "b.go", Added: 2}})
	v2 := p.View(30, 10)
	if containsStr(v2, "a.go") {
		t.Error("old file should be replaced")
	}
	if !containsStr(v2, "b.go") {
		t.Error("new file should appear")
	}
}

func indexOf(s, sub string) int {
	return strings.Index(s, sub)
}
