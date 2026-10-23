package api

import (
	"context"
	"sort"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
)

// SkillInfoDTO is the wire format for the skills panel.
type SkillInfoDTO struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	Enabled     bool     `json:"enabled"`
	Bundled     bool     `json:"bundled"`
}

// skillsStore exposes the same registry used by executing agents.
type skillsStore struct{ registry *skills.Registry }

func newSkillsStore(reg *skills.Registry) *skillsStore { return &skillsStore{registry: reg} }
func (s *skillsStore) list() []SkillInfoDTO {
	out := []SkillInfoDTO{}
	if s.registry == nil {
		return out
	}
	for _, sk := range s.registry.List() {
		out = append(out, SkillInfoDTO{Name: sk.Name, Description: sk.Description, Category: sk.Category, Tags: sk.Tags, Enabled: sk.Enabled, Bundled: sk.Bundled})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// handleSkillsList returns all loaded skills with their enabled state.
func (s *Server) handleSkillsList(_ context.Context, _ []byte) (interface{}, *RPCError) {
	if s.skillsStore == nil {
		return []SkillInfoDTO{}, nil
	}
	return s.skillsStore.list(), nil
}

// handleSkillsToggle enables or disables a skill by name.
func (s *Server) handleSkillsToggle(_ context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		Name    string `json:"name"`
		Enabled *bool  `json:"enabled,omitempty"`
	}
	if len(params) > 0 {
		if err := unmarshalParams(params, &p); err != nil {
			return nil, err
		}
	}
	if s.skillsStore == nil {
		return nil, &RPCError{Code: InternalError, Message: "skills store not configured"}
	}
	if s.skillsStore.registry == nil {
		return nil, &RPCError{Code: InternalError, Message: "skills registry not configured"}
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, &RPCError{Code: InvalidParams, Message: "skill name is required"}
	}
	if _, ok := s.skillsStore.registry.Get(p.Name); !ok {
		return nil, &RPCError{Code: InvalidParams, Message: "skill not found"}
	}
	enabled, err := s.skillsStore.registry.UpdateEnabled(p.Name, p.Enabled)
	if err != nil {
		return nil, &RPCError{Code: InternalError, Message: err.Error()}
	}
	return map[string]interface{}{"name": p.Name, "enabled": enabled}, nil
}
