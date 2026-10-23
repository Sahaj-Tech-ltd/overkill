package automemory

import (
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"path/filepath"
	"time"
)

func loadMemorySettings(home string) (*bool, error) {
	u, err := config.LoadUserOverrides(filepath.Join(home, "user.yaml"))
	if err != nil {
		return nil, err
	}
	return u.Advanced.Memory.Enabled, nil
}

func (e *Extractor) entryLimit() int {
	limit := maxFacts
	if e.configHome != "" {
		u, err := config.LoadUserOverrides(filepath.Join(e.configHome, "user.yaml"))
		if err == nil && u.Advanced.Memory.MaxEntries > 0 && u.Advanced.Memory.MaxEntries < limit {
			limit = u.Advanced.Memory.MaxEntries
		}
	}
	return limit
}
func (e *Extractor) retained(facts []Fact) []Fact {
	days := 0
	if e.configHome != "" {
		u, err := config.LoadUserOverrides(filepath.Join(e.configHome, "user.yaml"))
		if err == nil {
			days = u.Advanced.Memory.TTLDays
		}
	}
	var out []Fact
	for _, f := range facts {
		if days > 0 && (f.SavedAt == 0 || time.Since(time.Unix(f.SavedAt, 0)) > time.Duration(min(days, 36500))*24*time.Hour) {
			continue
		}
		out = append(out, f)
	}
	limit := e.entryLimit()
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}
