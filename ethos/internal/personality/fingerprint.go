package personality

import (
	"regexp"
	"strings"
	"sync"
	"time"
)

type ModelFingerprint struct {
	Family        string    `json:"family"`
	Version       string    `json:"version"`
	ContextWindow int       `json:"context_window"`
	DetectedAt    time.Time `json:"detected_at"`
}

// FingerprintTracker holds the previous + current model fingerprint
// so boot-time swap detection survives across BootCheck / Update /
// Current calls. All mutators + readers take the mutex; concurrent
// access from the boot path + an async config-reload is now race-
// safe.
type FingerprintTracker struct {
	mu       sync.Mutex
	current  *ModelFingerprint
	previous *ModelFingerprint
	changed  bool
}

var contextWindowMap = map[string]int{
	"claude-opus":   200000,
	"claude-sonnet": 200000,
	"claude-haiku":  200000,
	"gpt-4o":        128000,
	"gpt-4":         128000,
	"gpt-3.5":       16385,
	"gemini-pro":    1000000,
	"gemini-flash":  1000000,
}

const defaultContextWindow = 8192

var dateSuffixRe = regexp.MustCompile(`-\d{8}$`)

func NewFingerprintTracker() *FingerprintTracker {
	return &FingerprintTracker{}
}

func (ft *FingerprintTracker) Detect(modelID string) *ModelFingerprint {
	family, version := extractFamilyVersion(modelID)
	ctxWindow := defaultContextWindow
	if w, ok := contextWindowMap[family]; ok {
		ctxWindow = w
	}
	return &ModelFingerprint{
		Family:        family,
		Version:       version,
		ContextWindow: ctxWindow,
		DetectedAt:    time.Now(),
	}
}

func (ft *FingerprintTracker) HasChanged(newModelID string) bool {
	ft.mu.Lock()
	cur := ft.current
	ft.mu.Unlock()
	if cur == nil {
		return false
	}
	detected := ft.Detect(newModelID)
	return detected.Family != cur.Family
}

func (ft *FingerprintTracker) CalibratePrompt() string {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	if !ft.changed || ft.previous == nil || ft.current == nil {
		return ""
	}
	return "Model changed from " + ft.previous.Family + " to " + ft.current.Family + ". Running quick calibration to adjust capabilities."
}

func (ft *FingerprintTracker) Update(fp *ModelFingerprint) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.previous = ft.current
	ft.current = fp
	ft.changed = ft.previous != nil && ft.previous.Family != fp.Family
}

func (ft *FingerprintTracker) Current() *ModelFingerprint {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return ft.current
}

func (ft *FingerprintTracker) Previous() *ModelFingerprint {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return ft.previous
}

func extractFamilyVersion(modelID string) (string, string) {
	normalized := strings.ToLower(strings.TrimSpace(modelID))
	normalized = dateSuffixRe.ReplaceAllString(normalized, "")
	parts := strings.Split(normalized, "-")

	switch {
	case strings.HasPrefix(normalized, "claude"):
		return extractClaude(normalized, parts)
	case strings.HasPrefix(normalized, "gpt"):
		return extractGPT(normalized, parts)
	case strings.HasPrefix(normalized, "gemini"):
		return extractGemini(normalized, parts)
	case strings.HasPrefix(normalized, "llama"):
		return extractLlama(normalized, parts)
	case strings.HasPrefix(normalized, "mistral"):
		return extractMistral(normalized, parts)
	case strings.HasPrefix(normalized, "deepseek"):
		return extractDeepseek(normalized, parts)
	default:
		return modelID, modelID
	}
}

func extractClaude(normalized string, parts []string) (string, string) {
	var tier string
	for _, p := range parts {
		switch p {
		case "opus", "sonnet", "haiku":
			tier = p
		}
	}
	if tier == "" {
		return "claude", normalized
	}
	family := "claude-" + tier
	return family, normalized
}

func extractGPT(normalized string, parts []string) (string, string) {
	if strings.HasPrefix(normalized, "gpt-4o") {
		return "gpt-4o", "4o"
	}
	if strings.HasPrefix(normalized, "gpt-3.5") {
		return "gpt-3.5", "3.5-turbo"
	}
	return "gpt-4", normalized
}

func extractGemini(normalized string, parts []string) (string, string) {
	var variant string
	for _, p := range parts {
		switch p {
		case "pro", "flash":
			variant = p
		}
	}
	if variant == "" {
		return "gemini", normalized
	}
	family := "gemini-" + variant
	return family, normalized
}

func extractLlama(normalized string, parts []string) (string, string) {
	return "llama", normalized
}

func extractMistral(normalized string, parts []string) (string, string) {
	if len(parts) >= 2 {
		return "mistral-" + parts[1], parts[1]
	}
	return "mistral", normalized
}

func extractDeepseek(normalized string, parts []string) (string, string) {
	if len(parts) >= 2 {
		return "deepseek-" + parts[1], parts[1]
	}
	return "deepseek", normalized
}
