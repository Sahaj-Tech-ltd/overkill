package subagent

import (
	"encoding/json"
	"regexp"
	"strings"
)

// ExportContext holds the structured context passed between agents during delegation.
type ExportContext struct {
	FilesModified       []string `json:"files_modified,omitempty"`
	RecentChanges       string   `json:"recent_changes,omitempty"`
	ProjectStructure    string   `json:"project_structure,omitempty"`
	Constraints         string   `json:"constraints,omitempty"`
	Language            string   `json:"language,omitempty"`
	RelatedConversation string   `json:"related_conversation,omitempty"`
}

// ContextExport is the JSON envelope for cross-agent delegation.
// It carries session metadata plus the filtered context payload.
type ContextExport struct {
	SessionID       string        `json:"session_id"`
	Goal            string        `json:"goal"`
	Context         ExportContext `json:"context"`
	OverkillVersion string        `json:"overkill_version"`
}

// Pre-compiled secret detection patterns — evaluated once at package init.
var (
	// key=value or key:value patterns for common secret names (case-insensitive).
	reKeyValue = regexp.MustCompile(
		`(?i)(api[_-]?key|secret|token|password|credential|auth[_-]?token)\s*[=:]\s*\S+`,
	)
	// OpenAI-style secret tokens.
	reOpenAIKey = regexp.MustCompile(`sk-[a-zA-Z0-9]{20,}`)
	// Bearer tokens.
	reBearer = regexp.MustCompile(`(?i)bearer\s+[a-zA-Z0-9\-._~+/]+=*`)
)

// ToJSON marshals the ContextExport to a JSON string.
func (e ContextExport) ToJSON() (string, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// FilterSecrets returns a copy of the ContextExport with secrets redacted
// from all string fields, including top-level Goal and SessionID.
func (e ContextExport) FilterSecrets() ContextExport {
	cp := e // value copy

	cp.SessionID = redactSecrets(cp.SessionID)
	cp.Goal = redactSecrets(cp.Goal)
	cp.Context.RecentChanges = redactSecrets(cp.Context.RecentChanges)
	cp.Context.ProjectStructure = redactSecrets(cp.Context.ProjectStructure)
	cp.Context.Constraints = redactSecrets(cp.Context.Constraints)
	cp.Context.Language = redactSecrets(cp.Context.Language)
	cp.Context.RelatedConversation = redactSecrets(cp.Context.RelatedConversation)

	return cp
}

// ContextExportFromJSON unmarshals a JSON string into a ContextExport.
func ContextExportFromJSON(data string) (ContextExport, error) {
	var e ContextExport
	if err := json.Unmarshal([]byte(data), &e); err != nil {
		return ContextExport{}, err
	}
	return e, nil
}

// redactSecrets applies all secret-filtering rules to a single string.
func redactSecrets(s string) string {
	// First: key=value / key:value patterns — preserve the key.
	s = reKeyValue.ReplaceAllStringFunc(s, func(match string) string {
		// Try "=" separator first.
		if parts := strings.SplitN(match, "=", 2); len(parts) == 2 {
			return parts[0] + "=[REDACTED]"
		}
		// Fall back to ":" separator.
		if parts := strings.SplitN(match, ":", 2); len(parts) == 2 {
			return parts[0] + ":[REDACTED]"
		}
		return "[REDACTED]"
	})

	// Second: standalone OpenAI-style keys.
	s = reOpenAIKey.ReplaceAllString(s, "[REDACTED]")

	// Third: Bearer tokens.
	s = reBearer.ReplaceAllString(s, "[REDACTED]")

	return s
}
