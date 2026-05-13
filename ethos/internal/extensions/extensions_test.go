package extensions

import (
	"errors"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
)

func TestManager_NoBackendsEmpty(t *testing.T) {
	m := NewManager()
	got, err := m.List()
	if err != nil {
		t.Errorf("empty manager List should not error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty list, got %v", got)
	}
}

func TestManager_GetNotFoundForUnknownKind(t *testing.T) {
	m := NewManager()
	if _, err := m.Get(KindMCP, "anything"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestManager_AddBackendNilIsNoOp(t *testing.T) {
	m := NewManager()
	m.AddBackend(nil)
	if got, _ := m.List(); len(got) != 0 {
		t.Errorf("nil backend should not register, got %v", got)
	}
}

func TestSkillsBackend_Roundtrip(t *testing.T) {
	reg := skills.NewRegistry()
	mustRegister(t, reg, &skills.Skill{
		Name:         "house-style",
		Description:  "project conventions and styling rules",
		Instructions: "Use 4-space indents and trailing commas everywhere.",
		Tags:         []string{"style"},
		Version:      "1.0.0",
		Category:     "code",
		Enabled:      true,
		Bundled:      true,
	})

	m := NewManager()
	m.AddBackend(NewSkillsBackend(reg))

	got, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 extension, got %d", len(got))
	}
	ext := got[0]
	if ext.Kind != KindSkill {
		t.Errorf("Kind = %s, want %s", ext.Kind, KindSkill)
	}
	if ext.ID != "house-style" {
		t.Errorf("ID = %q, want house-style", ext.ID)
	}
	if !ext.Enabled {
		t.Error("expected enabled")
	}
	if ext.Source != "bundled" {
		t.Errorf("Source = %q, want bundled", ext.Source)
	}
	if ext.Metadata["version"] != "1.0.0" {
		t.Errorf("missing version metadata: %v", ext.Metadata)
	}
}

func TestSkillsBackend_EnableDisable(t *testing.T) {
	reg := skills.NewRegistry()
	mustRegister(t, reg, &skills.Skill{
		Name:         "togglable",
		Description:  "test skill",
		Instructions: "Some instructions that pass validation length requirement.",
		Enabled:      false,
	})
	m := NewManager()
	m.AddBackend(NewSkillsBackend(reg))

	if err := m.Enable(KindSkill, "togglable"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	list, _ := m.List()
	if !list[0].Enabled {
		t.Error("Enable should have flipped the flag")
	}
	if err := m.Disable(KindSkill, "togglable"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	list, _ = m.List()
	if list[0].Enabled {
		t.Error("Disable should have flipped the flag back")
	}
}

func TestStubBackends_ReturnUnsupported(t *testing.T) {
	m := NewManager()
	m.AddBackend(PluginsStubBackend{})
	m.AddBackend(HooksStubBackend{})
	m.AddBackend(MCPStubBackend{})

	for _, k := range []Kind{KindPlugin, KindHook, KindMCP} {
		if err := m.Enable(k, "x"); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s Enable should return ErrUnsupported, got %v", k, err)
		}
		if err := m.Disable(k, "x"); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s Disable should return ErrUnsupported, got %v", k, err)
		}
	}
	// List across stub backends is empty but does not error.
	got, err := m.List()
	if err != nil {
		t.Errorf("stub backends list should not error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("stub backends should list empty, got %v", got)
	}
}

func TestManager_GetUnknownIDInBackend(t *testing.T) {
	reg := skills.NewRegistry()
	m := NewManager()
	m.AddBackend(NewSkillsBackend(reg))
	if _, err := m.Get(KindSkill, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown ID should return ErrNotFound, got %v", err)
	}
}

func TestManager_ListSortedStable(t *testing.T) {
	reg := skills.NewRegistry()
	mustRegister(t, reg, &skills.Skill{Name: "zebra", Description: "z desc long enough for validation", Instructions: "long enough to pass validation."})
	mustRegister(t, reg, &skills.Skill{Name: "alpha", Description: "a desc long enough for validation", Instructions: "long enough to pass validation."})
	m := NewManager()
	m.AddBackend(NewSkillsBackend(reg))
	got, _ := m.List()
	if len(got) < 2 {
		t.Fatalf("need 2 results, got %d", len(got))
	}
	if got[0].ID != "alpha" || got[1].ID != "zebra" {
		t.Errorf("expected alpha then zebra, got %s, %s", got[0].ID, got[1].ID)
	}
}

func mustRegister(t *testing.T, r *skills.Registry, s *skills.Skill) {
	t.Helper()
	if err := r.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}
}
