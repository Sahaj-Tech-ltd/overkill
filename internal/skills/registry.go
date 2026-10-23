package skills

import (
	"encoding/json"
	"fmt"
	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

type Registry struct {
	mu               sync.RWMutex
	skills           map[string]*Skill
	index            map[string][]string
	stateDir         string
	enabledOverrides map[string]bool
}

func NewRegistry() *Registry {
	return &Registry{
		skills:           make(map[string]*Skill),
		index:            make(map[string][]string),
		enabledOverrides: make(map[string]bool),
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
	if enabled, ok := r.enabledOverrides[strings.ToLower(skill.Name)]; ok {
		skillCopy.Enabled = enabled
	}
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

// Match returns trigger-matched skills using an empty MatchContext —
// equivalent to the pre-Phase-1.5 behaviour. Skills declaring
// Conditions that REQUIRE context (cwd, language, time...) fail those
// checks here and are excluded. Use MatchWithContext for
// context-aware activation.
func (r *Registry) Match(input string) []*Skill {
	return r.MatchWithContext(input, MatchContext{})
}

// MatchWithContext returns skills whose Triggers fire AND whose
// Conditions all hold against ctx. Phase 1.5 #7.
//
// Semantics:
//   - Trigger match: case-insensitive substring of input (unchanged).
//   - Condition match: every populated field in skill.Conditions must
//     hold against ctx; missing fields are wildcards.
//   - Both gates apply (AND). A skill with triggers but no conditions
//     behaves like before. A skill with conditions but no triggers
//     is not returned here — the agent's renderSkillSection handles
//     always-on skills via a separate List walk and also applies
//     conditions there.
func (r *Registry) MatchWithContext(input string, ctx MatchContext) []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lower := strings.ToLower(input)
	seen := make(map[string]bool)
	var result []*Skill

	for trigger, names := range r.index {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(trigger) + `\b`)
		if !re.MatchString(lower) {
			continue
		}
		for _, name := range names {
			key := strings.ToLower(name)
			if seen[key] {
				continue
			}
			skill, ok := r.skills[key]
			if !ok {
				continue
			}
			if !skill.Enabled {
				continue
			}
			if !skill.Conditions.matches(ctx) {
				continue
			}
			seen[key] = true
			cp := *skill
			result = append(result, &cp)
		}
	}

	return result
}

// NewPersistentRegistry loads user enablement overrides. Each skill has its
// own atomic file so concurrent CLI processes changing different skills cannot
// overwrite each other's settings. Bundled skill files remain untouched.
func NewPersistentRegistry(dir string) (*Registry, error) {
	r := NewRegistry()
	r.stateDir = dir
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("skills: read enablement settings: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		if !nameRegex.MatchString(name) {
			return nil, fmt.Errorf("skills: invalid enablement setting filename %q", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var enabled *bool
		if err = json.Unmarshal(data, &enabled); err != nil {
			return nil, fmt.Errorf("skills: parse enablement for %s: %w", name, err)
		}
		if enabled == nil {
			return nil, fmt.Errorf("skills: enablement for %s must be true or false", name)
		}
		r.enabledOverrides[name] = *enabled
	}
	return r, nil
}

// UpdateEnabled commits the requested state before changing the executing
// registry. A nil state toggles atomically; an explicit state is idempotent.
func (r *Registry) UpdateEnabled(name string, enabled *bool) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.ToLower(name)
	skill, ok := r.skills[key]
	if !ok {
		return false, fmt.Errorf("skills: skill %q not found", name)
	}
	next := !skill.Enabled
	if enabled != nil {
		next = *enabled
	}
	if r.stateDir != "" {
		if err := os.MkdirAll(r.stateDir, 0750); err != nil {
			return skill.Enabled, fmt.Errorf("skills: save enablement: %w", err)
		}
		data, _ := json.Marshal(next)
		if err := atomicfile.WriteFile(filepath.Join(r.stateDir, key+".json"), data, 0600); err != nil {
			return skill.Enabled, fmt.Errorf("skills: save enablement: %w", err)
		}
	}
	skill.Enabled = next
	r.enabledOverrides[key] = next
	return next, nil
}

func (r *Registry) Enable(name string) error {
	enabled := true
	_, err := r.UpdateEnabled(name, &enabled)
	return err
}
func (r *Registry) Disable(name string) error {
	enabled := false
	_, err := r.UpdateEnabled(name, &enabled)
	return err
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
