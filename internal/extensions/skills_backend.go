package extensions

import (
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
)

// SkillsBackend adapts the existing skills.Registry to the Backend
// interface. The registry already supports Enable/Disable/List so this
// is a thin translation layer.
type SkillsBackend struct {
	reg *skills.Registry
}

// NewSkillsBackend wires a registry. Nil registry returns nil — the
// caller is expected to skip AddBackend when skills aren't configured.
func NewSkillsBackend(r *skills.Registry) *SkillsBackend {
	if r == nil {
		return nil
	}
	return &SkillsBackend{reg: r}
}

func (b *SkillsBackend) Kind() Kind { return KindSkill }

func (b *SkillsBackend) List() ([]Extension, error) {
	if b == nil || b.reg == nil {
		return nil, nil
	}
	skills := b.reg.List()
	out := make([]Extension, 0, len(skills))
	for _, s := range skills {
		if s == nil {
			continue
		}
		source := "user"
		if s.Bundled {
			source = "bundled"
		}
		md := map[string]string{}
		if s.Version != "" {
			md["version"] = s.Version
		}
		if s.Category != "" {
			md["category"] = s.Category
		}
		if len(s.Tags) > 0 {
			md["tags"] = strings.Join(s.Tags, ",")
		}
		out = append(out, Extension{
			Kind:        KindSkill,
			ID:          strings.ToLower(s.Name),
			Name:        s.Name,
			Description: s.Description,
			Source:      source,
			Enabled:     s.Enabled,
			Metadata:    md,
		})
	}
	return out, nil
}

func (b *SkillsBackend) Enable(id string) error {
	if b == nil || b.reg == nil {
		return ErrNotFound
	}
	return b.reg.Enable(id)
}

func (b *SkillsBackend) Disable(id string) error {
	if b == nil || b.reg == nil {
		return ErrNotFound
	}
	return b.reg.Disable(id)
}

func (b *SkillsBackend) Get(id string) (*Extension, error) {
	if b == nil || b.reg == nil {
		return nil, ErrNotFound
	}
	s, ok := b.reg.Get(strings.ToLower(id))
	if !ok || s == nil {
		return nil, ErrNotFound
	}
	source := "user"
	if s.Bundled {
		source = "bundled"
	}
	md := map[string]string{}
	if s.Version != "" {
		md["version"] = s.Version
	}
	if s.Category != "" {
		md["category"] = s.Category
	}
	if len(s.Tags) > 0 {
		md["tags"] = strings.Join(s.Tags, ",")
	}
	return &Extension{
		Kind:        KindSkill,
		ID:          strings.ToLower(s.Name),
		Name:        s.Name,
		Description: s.Description,
		Source:      source,
		Enabled:     s.Enabled,
		Metadata:    md,
	}, nil
}
