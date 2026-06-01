package api

import (
	"context"
	"sync"

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

// skillsStore holds the loaded registry and disabled-skill tracking.
type skillsStore struct {
	mu       sync.RWMutex
	registry *skills.Registry
	disabled map[string]bool
}

func newSkillsStore(reg *skills.Registry) *skillsStore {
	return &skillsStore{registry: reg, disabled: make(map[string]bool)}
}

func (s *skillsStore) list() []SkillInfoDTO {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.registry == nil {
		return nil
	}
	all := s.registry.List()
	out := make([]SkillInfoDTO, 0, len(all))
	for _, sk := range all {
		out = append(out, SkillInfoDTO{
			Name:        sk.Name,
			Description: sk.Description,
			Category:    sk.Category,
			Tags:        sk.Tags,
			Enabled:     !s.disabled[sk.Name],
			Bundled:     sk.Bundled,
		})
	}
	return out
}

func (s *skillsStore) toggle(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disabled[name] = !s.disabled[name]
	return !s.disabled[name]
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
	enabled := s.skillsStore.toggle(p.Name)
	return map[string]interface{}{"name": p.Name, "enabled": enabled}, nil
}
