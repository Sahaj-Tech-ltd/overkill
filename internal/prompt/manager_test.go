package prompt

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// mockChip is a test chip with configurable behavior.
type mockChip struct {
	kind          string
	title         string
	value         string
	err           error
	enabled       bool
	refreshPolicy RefreshPolicy
	callCount     *int // tracks how many times Value was called
}

func (m *mockChip) Kind() string               { return m.kind }
func (m *mockChip) Title() string              { return m.title }
func (m *mockChip) RefreshPolicy() RefreshPolicy { return m.refreshPolicy }
func (m *mockChip) Enabled() bool              { return m.enabled }

func (m *mockChip) Value(_ context.Context) (string, error) {
	if m.callCount != nil {
		*m.callCount++
	}
	return m.value, m.err
}

func newMockChip(kind, title, value string) *mockChip {
	return &mockChip{
		kind:          kind,
		title:         title,
		value:         value,
		enabled:       true,
		refreshPolicy: EveryTurn,
	}
}

func TestChipManagerRegister(t *testing.T) {
	cm := NewChipManager()

	c1 := newMockChip("dir", "Directory", "/home/user")
	c2 := newMockChip("git_branch", "Git Branch", "main")

	cm.Register(c1)
	cm.Register(c2)

	infos := cm.List()
	if len(infos) != 2 {
		t.Fatalf("expected 2 chips, got %d", len(infos))
	}
}

func TestChipManagerRegisterReplace(t *testing.T) {
	cm := NewChipManager()

	c1 := newMockChip("dir", "Directory", "/home/user")
	c2 := newMockChip("dir", "Directory V2", "/other")

	cm.Register(c1)
	cm.Register(c2)

	infos := cm.List()
	if len(infos) != 1 {
		t.Fatalf("expected 1 chip after replace, got %d", len(infos))
	}
	if infos[0].Title != "Directory V2" {
		t.Fatalf("expected title 'Directory V2', got %q", infos[0].Title)
	}
}

func TestChipManagerUnregister(t *testing.T) {
	cm := NewChipManager()

	cm.Register(newMockChip("dir", "Directory", "/home/user"))
	cm.Register(newMockChip("git_branch", "Git Branch", "main"))

	cm.Unregister("dir")

	infos := cm.List()
	if len(infos) != 1 {
		t.Fatalf("expected 1 chip after unregister, got %d", len(infos))
	}
	if infos[0].Kind != "git_branch" {
		t.Fatalf("expected git_branch chip, got %q", infos[0].Kind)
	}
}

func TestChipManagerRender(t *testing.T) {
	cm := NewChipManager()

	cm.Register(newMockChip("dir", "Directory", "/home/user"))
	cm.Register(newMockChip("git_branch", "Git Branch", "main"))

	result := cm.Render(context.Background())
	if !strings.Contains(result, "[Directory]: /home/user") {
		t.Errorf("expected directory line, got: %s", result)
	}
	if !strings.Contains(result, "[Git Branch]: main") {
		t.Errorf("expected git branch line, got: %s", result)
	}
}

func TestChipManagerRenderSkipsDisabled(t *testing.T) {
	cm := NewChipManager()

	enabled := newMockChip("dir", "Directory", "/home/user")
	disabled := &mockChip{
		kind:    "git_branch",
		title:   "Git Branch",
		value:   "main",
		enabled: false,
	}

	cm.Register(enabled)
	cm.Register(disabled)

	result := cm.Render(context.Background())
	if !strings.Contains(result, "[Directory]: /home/user") {
		t.Errorf("expected directory line, got: %s", result)
	}
	if strings.Contains(result, "Git Branch") {
		t.Errorf("expected disabled chip to be skipped, got: %s", result)
	}
}

func TestChipManagerRenderSkipsError(t *testing.T) {
	cm := NewChipManager()

	cm.Register(newMockChip("dir", "Directory", "/home/user"))
	cm.Register(&mockChip{
		kind:    "git_branch",
		title:   "Git Branch",
		value:   "",
		err:     fmt.Errorf("git not found"),
		enabled: true,
	})

	result := cm.Render(context.Background())
	if !strings.Contains(result, "[Directory]: /home/user") {
		t.Errorf("expected directory line, got: %s", result)
	}
	if strings.Contains(result, "Git Branch") {
		t.Errorf("expected error chip to be skipped, got: %s", result)
	}
}

func TestChipManagerRenderSkipsEmptyValue(t *testing.T) {
	cm := NewChipManager()

	cm.Register(newMockChip("dir", "Directory", "/home/user"))
	cm.Register(&mockChip{
		kind:    "git_diff",
		title:   "Git Diff",
		value:   "",
		err:     nil,
		enabled: true,
	})

	result := cm.Render(context.Background())
	if !strings.Contains(result, "[Directory]: /home/user") {
		t.Errorf("expected directory line, got: %s", result)
	}
	if strings.Contains(result, "Git Diff") {
		t.Errorf("expected empty-value chip to be skipped, got: %s", result)
	}
}

func TestChipManagerOnChangeCache(t *testing.T) {
	cm := NewChipManager()

	callCount := 0
	onChangeChip := &mockChip{
		kind:          "git_branch",
		title:         "Git Branch",
		value:         "main",
		enabled:       true,
		refreshPolicy: OnChange,
		callCount:     &callCount,
	}

	cm.Register(onChangeChip)

	// First call: Value should be called.
	result := cm.Render(context.Background())
	if !strings.Contains(result, "[Git Branch]: main") {
		t.Errorf("expected branch line, got: %s", result)
	}
	if callCount != 1 {
		t.Errorf("expected 1 Value call, got %d", callCount)
	}

	// Second call: value hasn't changed, Value is still called (we need
	// to check), but the output line should still be present (cached).
	result = cm.Render(context.Background())
	if !strings.Contains(result, "[Git Branch]: main") {
		t.Errorf("expected branch line (cached), got: %s", result)
	}
	// Value is called each time for OnChange chips — the optimization
	// is in skipping the output, not the call itself.
	if callCount != 2 {
		t.Errorf("expected 2 Value calls, got %d", callCount)
	}

	// Change the value, call again: Value SHOULD be called and output new value.
	onChangeChip.value = "feature/xyz"
	result = cm.Render(context.Background())
	if !strings.Contains(result, "[Git Branch]: feature/xyz") {
		t.Errorf("expected new branch line, got: %s", result)
	}
	if callCount != 3 {
		t.Errorf("expected 3 Value calls after change, got %d", callCount)
	}
}

func TestChipManagerContextProvider(t *testing.T) {
	cm := NewChipManager()
	cm.Register(newMockChip("dir", "Directory", "/home/user"))

	provider := cm.ContextProvider()
	result := provider(context.Background(), "session-123")
	if !strings.Contains(result, "[Directory]: /home/user") {
		t.Errorf("expected directory line, got: %s", result)
	}
}

func TestChipManagerContextProviderNil(t *testing.T) {
	var cm *ChipManager
	provider := cm.ContextProvider()
	result := provider(context.Background(), "session-123")
	if result != "" {
		t.Errorf("expected empty string from nil manager, got: %s", result)
	}
}

func TestChipManagerListSorted(t *testing.T) {
	cm := NewChipManager()

	cm.Register(newMockChip("git_diff", "Git Diff", "1 file changed"))
	cm.Register(newMockChip("dir", "Directory", "/home/user"))
	cm.Register(newMockChip("git_branch", "Git Branch", "main"))

	infos := cm.List()
	if len(infos) != 3 {
		t.Fatalf("expected 3 chips, got %d", len(infos))
	}
	if infos[0].Kind != "dir" {
		t.Errorf("expected 'dir' first, got %q", infos[0].Kind)
	}
	if infos[1].Kind != "git_branch" {
		t.Errorf("expected 'git_branch' second, got %q", infos[1].Kind)
	}
	if infos[2].Kind != "git_diff" {
		t.Errorf("expected 'git_diff' third, got %q", infos[2].Kind)
	}
}

func TestChipManagerRenderContextCancellation(t *testing.T) {
	cm := NewChipManager()
	cm.Register(newMockChip("dir", "Directory", "/home/user"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before render

	result := cm.Render(ctx)
	if result != "" {
		t.Errorf("expected empty on cancelled context, got: %s", result)
	}
}
