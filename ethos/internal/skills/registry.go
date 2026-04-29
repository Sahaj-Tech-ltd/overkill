package skills

import (
	"fmt"
	"strings"
	"sync"
)

type Registry struct {
	mu     sync.RWMutex
	skills map[string]*Skill
	index  map[string][]string
}

func NewRegistry() *Registry {
	return &Registry{
		skills: make(map[string]*Skill),
		index:  make(map[string][]string),
	}
}

func (r *Registry) Register(skill *Skill) error {
	if skill == nil {
		return fmt.Errorf("skills: skill is nil")
	}

	errs := Validate(skill)
	if len(errs) > 0 {
		return fmt.Errorf("skills: validation failed: %v", errs)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.skills[strings.ToLower(skill.Name)]; exists {
		return fmt.Errorf("skills: skill %q already registered", skill.Name)
	}

	skillCopy := *skill
	r.skills[strings.ToLower(skill.Name)] = &skillCopy

	for _, trigger := range skill.Triggers {
		key := strings.ToLower(trigger)
		r.index[key] = append(r.index[key], skill.Name)
	}

	return nil
}

func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := strings.ToLower(name)
	skill, exists := r.skills[key]
	if !exists {
		return false
	}

	for trigger, names := range r.index {
		filtered := names[:0]
		for _, n := range names {
			if strings.ToLower(n) != key {
				filtered = append(filtered, n)
			}
		}
		if len(filtered) == 0 {
			delete(r.index, trigger)
		} else {
			r.index[trigger] = filtered
		}
	}

	delete(r.skills, key)
	_ = skill
	return true
}

func (r *Registry) Get(name string) (*Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	skill, ok := r.skills[strings.ToLower(name)]
	if !ok {
		return nil, false
	}
	cp := *skill
	return &cp, true
}

func (r *Registry) List() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Skill, 0, len(r.skills))
	for _, skill := range r.skills {
		cp := *skill
		result = append(result, &cp)
	}
	return result
}

func (r *Registry) ListByCategory(category string) []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*Skill
	lower := strings.ToLower(category)
	for _, skill := range r.skills {
		if strings.ToLower(skill.Category) == lower {
			cp := *skill
			result = append(result, &cp)
		}
	}
	return result
}

func (r *Registry) ListByTag(tag string) []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*Skill
	lower := strings.ToLower(tag)
	for _, skill := range r.skills {
		for _, t := range skill.Tags {
			if strings.ToLower(t) == lower {
				cp := *skill
				result = append(result, &cp)
				break
			}
		}
	}
	return result
}

func (r *Registry) Search(query string) []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lower := strings.ToLower(query)
	var result []*Skill

	for _, skill := range r.skills {
		if strings.Contains(strings.ToLower(skill.Name), lower) ||
			strings.Contains(strings.ToLower(skill.Description), lower) {
			cp := *skill
			result = append(result, &cp)
			continue
		}
		for _, t := range skill.Tags {
			if strings.Contains(strings.ToLower(t), lower) {
				cp := *skill
				result = append(result, &cp)
				break
			}
		}
	}

	return result
}

func (r *Registry) Match(input string) []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lower := strings.ToLower(input)
	seen := make(map[string]bool)
	var result []*Skill

	for trigger, names := range r.index {
		if strings.Contains(lower, trigger) {
			for _, name := range names {
				key := strings.ToLower(name)
				if !seen[key] {
					seen[key] = true
					if skill, ok := r.skills[key]; ok {
						cp := *skill
						result = append(result, &cp)
					}
				}
			}
		}
	}

	return result
}

func (r *Registry) Enable(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := strings.ToLower(name)
	skill, ok := r.skills[key]
	if !ok {
		return fmt.Errorf("skills: skill %q not found", name)
	}

	skill.Enabled = true
	return nil
}

func (r *Registry) Disable(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := strings.ToLower(name)
	skill, ok := r.skills[key]
	if !ok {
		return fmt.Errorf("skills: skill %q not found", name)
	}

	skill.Enabled = false
	return nil
}

func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.skills)
}

func (r *Registry) CountByCategory() map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]int)
	for _, skill := range r.skills {
		result[skill.Category]++
	}
	return result
}
