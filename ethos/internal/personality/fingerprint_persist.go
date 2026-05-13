package personality

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// LoadFromFile reads a previously-persisted fingerprint and seeds it
// into the tracker's CURRENT slot. The follow-up Update call shifts it
// to previous and installs the live session's fingerprint in current
// (matching the runtime-model-swap shape). Missing file is fine (cold
// start) and returns nil.
//
// Sister function to SaveToFile. Together they make boot-time model
// change detection durable across sessions (master plan §4.16).
func (ft *FingerprintTracker) LoadFromFile(path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("personality: fingerprint load: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var fp ModelFingerprint
	if err := json.Unmarshal(data, &fp); err != nil {
		return fmt.Errorf("personality: fingerprint parse: %w", err)
	}
	ft.current = &fp
	return nil
}

// SaveToFile persists the CURRENT fingerprint atomically. Call after
// Update with the live session's model so the next boot can compare.
// No-op when no current fingerprint is tracked.
func (ft *FingerprintTracker) SaveToFile(path string) error {
	if path == "" || ft == nil || ft.current == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("personality: fingerprint mkdir: %w", err)
	}
	data, err := json.MarshalIndent(ft.current, "", "  ")
	if err != nil {
		return fmt.Errorf("personality: fingerprint marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("personality: fingerprint write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("personality: fingerprint rename: %w", err)
	}
	return nil
}

// BootCheck loads the prior fingerprint, detects the current model,
// updates the tracker, and returns the calibration notice (empty when
// no change or cold start). Callers then call SaveToFile after
// session-end to persist the new state for next boot.
//
// Composite helper so the wire-up is a single call from the CLI boot
// path rather than three steps spread across files.
func (ft *FingerprintTracker) BootCheck(path, currentModel string) (string, error) {
	if err := ft.LoadFromFile(path); err != nil {
		return "", err
	}
	fp := ft.Detect(currentModel)
	ft.Update(fp)
	return ft.CalibratePrompt(), nil
}
