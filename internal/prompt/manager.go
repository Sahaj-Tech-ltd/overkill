package prompt

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ChipManager aggregates multiple Chip implementations and renders them
// into a composite context string suitable for injection into the agent's
// system prompt.
type ChipManager struct {
	mu    sync.RWMutex
	chips []Chip

	// lastValues caches the most recent rendered value for each chip
	// keyed by Kind(). Used to implement the OnChange refresh policy.
	lastValues map[string]string
}

// NewChipManager returns an initialized ChipManager ready for registration.
func NewChipManager() *ChipManager {
	return &ChipManager{
		lastValues: make(map[string]string),
	}
}

// Register adds a chip to the manager. If a chip with the same Kind
// already exists, it is replaced. Safe for concurrent use.
func (cm *ChipManager) Register(chip Chip) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Replace existing chip with the same kind.
	for i, existing := range cm.chips {
		if existing.Kind() == chip.Kind() {
			cm.chips[i] = chip
			return
		}
	}
	cm.chips = append(cm.chips, chip)
}

// Unregister removes the chip with the given kind. No-op if not found.
func (cm *ChipManager) Unregister(kind string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for i, chip := range cm.chips {
		if chip.Kind() == kind {
			cm.chips = append(cm.chips[:i], cm.chips[i+1:]...)
			delete(cm.lastValues, kind)
			return
		}
	}
}

// Render calls Value on every enabled chip, formats the output as
// "[TITLE]: value" lines, and joins them with newlines. Chips that
// return errors or empty values are silently omitted (never fail the
// prompt). Respects RefreshPolicy — OnChange chips skip the Value
// call when the last rendered value hasn't changed.
//
// Render is safe for concurrent use.
func (cm *ChipManager) Render(ctx context.Context) string {
	cm.mu.RLock()
	// Snapshot under read lock so we don't hold it during Value calls.
	chips := make([]Chip, len(cm.chips))
	copy(chips, cm.chips)
	cm.mu.RUnlock()

	var lines []string
	for _, chip := range chips {
		if !chip.Enabled() {
			continue
		}

		// Respect context cancellation.
		select {
		case <-ctx.Done():
			// Add what we have so far and bail out.
			return strings.Join(lines, "\n")
		default:
		}

		val, err := chip.Value(ctx)
		if err != nil {
			// Errors are logged but never break the prompt; the chip
			// is simply omitted for this turn.
			continue
		}
		if val == "" {
			continue
		}

		// Apply OnChange caching.
		if chip.RefreshPolicy() == OnChange {
			cm.mu.Lock()
			prev := cm.lastValues[chip.Kind()]
			if prev == val {
				// Value hasn't changed; use cached output.
				cm.mu.Unlock()
				lines = append(lines, formatChip(chip.Title(), prev))
				continue
			}
			cm.lastValues[chip.Kind()] = val
			cm.mu.Unlock()
		}

		lines = append(lines, formatChip(chip.Title(), val))
	}

	return strings.Join(lines, "\n")
}

// List returns metadata for every registered chip, sorted by Kind.
func (cm *ChipManager) List() []ChipInfo {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	infos := make([]ChipInfo, 0, len(cm.chips))
	for _, chip := range cm.chips {
		infos = append(infos, ChipInfo{
			Kind:          chip.Kind(),
			Title:         chip.Title(),
			Enabled:       chip.Enabled(),
			RefreshPolicy: chip.RefreshPolicy(),
		})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Kind < infos[j].Kind })
	return infos
}

// ContextProvider returns a function compatible with the Agent's
// SetContextProvider callback signature. The returned function calls
// Render and returns the result. When cm is nil, the returned function
// returns an empty string (safe no-op).
func (cm *ChipManager) ContextProvider() func(ctx context.Context, sessionID string) string {
	if cm == nil {
		return func(ctx context.Context, sessionID string) string { return "" }
	}
	return func(ctx context.Context, sessionID string) string {
		return cm.Render(ctx)
	}
}

// formatChip produces the standard "[TITLE]: value" line for a chip.
func formatChip(title, value string) string {
	return fmt.Sprintf("[%s]: %s", title, value)
}
