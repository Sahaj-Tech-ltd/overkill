package hotreload

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// NewLogReporter writes one structured JSON outcome through a Printf-compatible
// sink, such as log.Printf. sessionID must be an opaque session identifier, not
// a prompt, credential or configuration value. A nil sink explicitly discards
// reports. Error messages are deliberately classified, never interpolated:
// YAML parse errors may themselves contain configuration values or secrets.
func NewLogReporter(printf func(string, ...any), sessionID string) Reporter {
	if printf == nil {
		return DiscardReporter()
	}
	return &logReporter{printf: printf, sessionID: sessionID}
}

type logReporter struct {
	printf    func(string, ...any)
	sessionID string
}

func (r *logReporter) OnReload(changed []string, err error) {
	result := ReloadResult{Changed: changed, Err: err}
	var restart *RestartRequiredError
	if errors.As(err, &restart) {
		result.RestartRequired = restart.Fields
		result.Applied = restart.Applied
	}
	// Legacy success callbacks do not establish actual applied effects. Only
	// the detailed callback is authoritative for non-partial applied fields.
	r.OnReloadResult(result)
}

var reloadLogFields = func() map[string]bool {
	fields := make(map[string]bool)
	var walk func(reflect.Type, string)
	walk = func(t reflect.Type, prefix string) {
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			name := strings.Split(field.Tag.Get("yaml"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			key := prefix + name
			if key == "basic" || key == "advanced" {
				walk(field.Type, key+".")
			} else {
				fields[key] = true
			}
		}
	}
	walk(reflect.TypeOf(config.UserOverrides{}), "")
	return fields
}()

func logFieldNames(fields []string) []string {
	out := make([]string, 0, len(reloadLogFields))
	seen := make(map[string]bool)
	for _, field := range fields {
		if reloadLogFields[field] && !seen[field] {
			out = append(out, field)
			seen[field] = true
		}
	}
	return out
}

func (r *logReporter) OnReloadResult(result ReloadResult) {
	entry := struct {
		Event           string   `json:"event"`
		Session         string   `json:"session"`
		Status          string   `json:"status"`
		Changed         []string `json:"changed"`
		Applied         []string `json:"applied"`
		RestartRequired []string `json:"restart_required"`
		ErrorKind       string   `json:"error_kind,omitempty"`
	}{Event: "settings_reload", Session: r.sessionID, Status: "applied", Changed: logFieldNames(result.Changed), Applied: logFieldNames(result.Applied), RestartRequired: logFieldNames(result.RestartRequired)}
	var restart *RestartRequiredError
	switch {
	case result.Err != nil && !errors.As(result.Err, &restart):
		entry.Status = "error"
		entry.ErrorKind = "load_failed"
	case len(entry.RestartRequired) > 0 || restart != nil:
		entry.Status = "restart_required"
		entry.ErrorKind = "restart_required"
	case len(entry.Applied) == 0:
		entry.Status = "observed"
	}
	// All fields are strings/slices of strings, so Marshal cannot fail.
	encoded, _ := json.Marshal(entry)
	r.printf("%s", encoded)
}
