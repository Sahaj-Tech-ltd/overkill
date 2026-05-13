package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/tags"
)

func newTestTagMgr(t *testing.T) *tags.Manager {
	t.Helper()
	m, err := tags.NewManager(filepath.Join(t.TempDir(), "t.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTagAddAndList(t *testing.T) {
	mgr := newTestTagMgr(t)
	add := NewTagAddTool(mgr)
	in, _ := json.Marshal(map[string]string{"path": "x", "tag": "review"})
	if _, err := add.Execute(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	list := NewTagListTool(mgr)
	out, err := list.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Count int `json:"count"`
	}
	json.Unmarshal(out, &resp)
	if resp.Count != 1 {
		t.Errorf("count=%d", resp.Count)
	}
}

func TestTagRemove(t *testing.T) {
	mgr := newTestTagMgr(t)
	mgr.Tag("a", "todo", "")
	rm := NewTagRemoveTool(mgr)
	in, _ := json.Marshal(map[string]string{"path": "a", "tag": "todo"})
	if _, err := rm.Execute(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if got := mgr.List(); len(got) != 0 {
		t.Errorf("not removed: %+v", got)
	}
}
