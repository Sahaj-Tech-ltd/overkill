package hotreload

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type loggedReload struct {
	Event           string   `json:"event"`
	Session         string   `json:"session"`
	Status          string   `json:"status"`
	Changed         []string `json:"changed"`
	Applied         []string `json:"applied"`
	RestartRequired []string `json:"restart_required"`
	ErrorKind       string   `json:"error_kind"`
}

func readLog(t *testing.T, b *bytes.Buffer) loggedReload {
	t.Helper()
	var entry loggedReload
	if err := json.Unmarshal(b.Bytes(), &entry); err != nil {
		t.Fatalf("structured log: %v; %q", err, b.String())
	}
	b.Reset()
	return entry
}

func TestLogReporterActualAppliedPendingAndLoadError(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	rep := NewLogReporter(logger.Printf, "session-fixture")
	baseline := readOverrides(t, "basic:\n  model: model-secret-before\n")
	a := &fakeAgent{model: "model-secret-before"}
	w := &agentWiring{agent: a, prev: baseline, baseline: baseline, initialModel: a.Model(), reporter: rep}
	w.apply(readOverrides(t, "basic:\n  model: model-secret-after\n"))
	if strings.Contains(buf.String(), "model-secret") {
		t.Fatal("model value leaked")
	}
	entry := readLog(t, &buf)
	if entry.Event != "settings_reload" || entry.Session != "session-fixture" || entry.Status != "applied" || !reflect.DeepEqual(entry.Changed, []string{"basic.model"}) || !reflect.DeepEqual(entry.Applied, []string{"basic.model"}) || len(entry.RestartRequired) != 0 || entry.ErrorKind != "" {
		t.Fatalf("applied %+v", entry)
	}
	w.apply(readOverrides(t, "basic:\n  model: model-secret-after\nadvanced:\n  providers:\n    sensitive-provider-name:\n      api_key: credential-secret-value\n  persona:\n    directive: prompt-secret-value\n"))
	for _, value := range []string{"credential-secret-value", "sensitive-provider-name", "prompt-secret-value", "model-secret"} {
		if strings.Contains(buf.String(), value) {
			t.Fatalf("config value leaked: %q", value)
		}
	}
	entry = readLog(t, &buf)
	if entry.Status != "restart_required" || entry.ErrorKind != "restart_required" || len(entry.Applied) != 0 || !reflect.DeepEqual(entry.RestartRequired, []string{"advanced.providers", "advanced.persona"}) {
		t.Fatalf("restart %+v", entry)
	}
	// A real YAML type error contains the rejected scalar. It must never
	// reach the log sink, including failures during initial WireAgent setup.
	file := filepath.Join(t.TempDir(), "user.yaml")
	if err := os.WriteFile(file, []byte("advanced:\n  scanners:\n    command:\n      enabled: credential-secret-invalid-bool\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := WireAgent(t.Context(), New(Paths{}), a, file, rep)
	if err == nil {
		t.Fatal("invalid bool unexpectedly parsed")
	}
	rep.OnReload(nil, err)
	if strings.Contains(buf.String(), "credential-secret") || strings.Contains(buf.String(), file) {
		t.Fatal("raw load error leaked")
	}
	entry = readLog(t, &buf)
	if entry.Status != "error" || entry.ErrorKind != "load_failed" || len(entry.Applied) != 0 || len(entry.Changed) != 0 {
		t.Fatalf("error %+v", entry)
	}
}

func TestLogReporterWhitelistsFieldsAndEscapesIdentity(t *testing.T) {
	var buf bytes.Buffer
	rep := NewLogReporter(log.New(&buf, "", 0).Printf, "session\nquoted\"fixture")
	detailed := rep.(ResultReporter)
	detailed.OnReloadResult(ReloadResult{Changed: []string{"basic.model", "secret-value-in-invalid-field", "basic.model"}, Applied: []string{"basic.model"}, RestartRequired: []string{"advanced.persona", "bad-secret-field"}, Err: errors.New("raw-credential-error")})
	output := buf.String()
	if strings.Count(output, "\n") != 1 || strings.Contains(output, "secret-value") || strings.Contains(output, "bad-secret") || strings.Contains(output, "raw-credential") {
		t.Fatalf("unsafe output %q", output)
	}
	entry := readLog(t, &buf)
	if entry.Session != "session\nquoted\"fixture" || !reflect.DeepEqual(entry.Changed, []string{"basic.model"}) || !reflect.DeepEqual(entry.RestartRequired, []string{"advanced.persona"}) {
		t.Fatalf("fields %+v", entry)
	}
}

func TestLogReporterLegacyPartialAndObserved(t *testing.T) {
	var buf bytes.Buffer
	rep := NewLogReporter(log.New(&buf, "", 0).Printf, "legacy-session")
	rep.OnReload([]string{"basic.model", "advanced.scanners"}, fmt.Errorf("wrapper-secret: %w", &RestartRequiredError{Fields: []string{"advanced.scanners"}, Applied: []string{"basic.model"}}))
	if strings.Contains(buf.String(), "wrapper-secret") {
		t.Fatal("wrapped raw error leaked")
	}
	entry := readLog(t, &buf)
	if entry.Status != "restart_required" || !reflect.DeepEqual(entry.Applied, []string{"basic.model"}) || !reflect.DeepEqual(entry.RestartRequired, []string{"advanced.scanners"}) {
		t.Fatalf("legacy partial %+v", entry)
	}
	rep.OnReload([]string{"basic.model"}, nil)
	if entry = readLog(t, &buf); entry.Status != "observed" || len(entry.Applied) != 0 {
		t.Fatalf("legacy callback falsely claimed an effect %+v", entry)
	}
	// Nil is an explicit opt-out rather than an unexpected global logger.
	NewLogReporter(nil, "discard").OnReload(nil, errors.New("must not panic"))
}
