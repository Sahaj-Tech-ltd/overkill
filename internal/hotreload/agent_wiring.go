// Package hotreload wires live config notifications to an agent.
//
// WireAgent currently applies only basic.model. SetModel changes the model
// used when a subsequent provider request is constructed, including another
// step in an existing Run; it does not rewrite an already submitted request.
// Clearing the override restores the model observed when wiring was attached.
//
// All other user.yaml settings are reported as requiring restart when they
// differ from the initial snapshot. In particular, scanners are NOT rebuilt,
// and persona/system-prompt overrides are NOT installed by this adapter.
// A future live scanner adapter must atomically replace the actual consumers
// before those settings can be classified as applied here.
package hotreload

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// Agent is the minimal surface WireAgent needs. The full Agent type
// in internal/agent satisfies this; tests can pass a stub.
type Agent interface {
	SetModel(string)
	Model() string
}

// Reporter receives observed changes and any read/parse or restart-required
// outcome. Callbacks must return promptly; DiscardReporter suppresses them.
type Reporter interface {
	OnReload(changed []string, err error)
}

// WireAgent subscribes the agent to user-config changes on the bus
// and applies them as they arrive. Returns an unsubscribe func that
// MUST be called on agent shutdown.
//
// The caller supplies the user.yaml path so test runs can point at
// a tempdir. On boot we read the current file once so subsequent
// reloads diff against it; without the prior snapshot every reload
// would look like "everything changed" and we'd noisily re-apply
// every field on every save.
func WireAgent(ctx context.Context, bus *Bus, agent Agent, userYAMLPath string, reporter Reporter) (func(), error) {
	if bus == nil || agent == nil {
		return func() {}, fmt.Errorf("hotreload: bus and agent are required")
	}
	prev, err := config.LoadUserOverrides(userYAMLPath)
	if err != nil {
		return func() {}, err
	}

	w := &agentWiring{
		agent:        agent,
		path:         userYAMLPath,
		prev:         prev,
		baseline:     prev,
		initialModel: agent.Model(),
		reporter:     reporter,
	}

	ch, unsubscribe := bus.Subscribe(SubjectConfig)
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				// B124: after the 200ms debounce fires, some editors
				// (especially those that write to a temp file then
				// rename) may still be flushing. A 200ms grace period
				// avoids partial reads.
				select {
				case <-time.After(200 * time.Millisecond):
				case <-ctx.Done():
					return
				}
				if ev.Kind == EventRemoved {
					// A removed file is a new default snapshot; only the model
					// is applied here, with other differences reported.
					w.apply(config.DefaultUserOverrides())
					continue
				}
				newCfg, loadErr := config.LoadUserOverrides(w.path)
				if loadErr != nil {
					w.report(ReloadResult{Err: loadErr})
					continue
				}
				w.apply(newCfg)
			}
		}
	}()

	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			cancel()
			unsubscribe()
		})
		<-done
	}
	return stop, nil
}

// agentWiring is the per-bus state. Holds the prior config snapshot
// + the agent reference so apply() can diff and only re-apply
// changed fields. Concurrent applies are serialised so two near-
// simultaneous saves don't interleave.
type agentWiring struct {
	mu                sync.Mutex
	agent             Agent
	path              string
	prev              *config.UserOverrides
	baseline          *config.UserOverrides
	initialModel      string
	modelResetPending bool
	reporter          Reporter
}

// ReloadResult separates observed file edits from effects on this live agent.
// RestartRequired is the outstanding difference from wiring-time settings,
// not just this edit. Reverting an unapplied edit clears its restart warning.
// Field names contain no configuration values or credentials. RestartRequired
// does not promise that an arbitrary setting has a startup consumer: callers
// must reassemble with supported settings; this adapter never installs them.
type ReloadResult struct {
	Changed         []string
	Applied         []string
	RestartRequired []string
	Err             error
}

// ResultReporter optionally receives the full outcome instead of OnReload.
// Implementations must still satisfy Reporter for WireAgent compatibility.
type ResultReporter interface{ OnReloadResult(ReloadResult) }

// RestartRequiredError lets legacy reporters distinguish a successfully read
// file with unapplied settings from a parse/read failure using errors.As.
// Applied contains only settings that this adapter actually changed.
type RestartRequiredError struct {
	Fields  []string
	Applied []string
}

func (e *RestartRequiredError) Error() string {
	return "hotreload: restart required for " + strings.Join(e.Fields, ", ")
}

func (w *agentWiring) report(result ReloadResult) {
	if w.reporter == nil {
		return
	}
	if detailed, ok := w.reporter.(ResultReporter); ok {
		detailed.OnReloadResult(result)
		return
	}
	w.reporter.OnReload(result.Changed, result.Err)
}

// settingsDiff reports every top-level YAML setting, retaining each basic or
// advanced child as one unit. DeepEqual compares pointed-to bool values and
// maps/slices rather than pointer identity (a fresh decode allocates pointers).
func settingsDiff(before, after *config.UserOverrides) []string {
	var fields []string
	var walk func(reflect.Value, reflect.Value, string)
	walk = func(a, b reflect.Value, prefix string) {
		typ := a.Type()
		for i := 0; i < a.NumField(); i++ {
			name := strings.Split(typ.Field(i).Tag.Get("yaml"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			field := prefix + name
			if field == "basic" || field == "advanced" {
				walk(a.Field(i), b.Field(i), field+".")
			} else if !reflect.DeepEqual(a.Field(i).Interface(), b.Field(i).Interface()) {
				fields = append(fields, field)
			}
		}
	}
	walk(reflect.ValueOf(before).Elem(), reflect.ValueOf(after).Elem(), "")
	return fields
}

func (w *agentWiring) apply(next *config.UserOverrides) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if next == nil {
		return
	}
	changed := settingsDiff(w.prev, next)
	if len(changed) == 0 {
		return
	}
	result := ReloadResult{Changed: changed}
	for _, field := range changed {
		if field != "basic.model" {
			continue
		}
		model := next.Basic.Model
		if model == "" {
			model = w.initialModel
		}
		w.modelResetPending = model == ""
		if model != "" {
			w.agent.SetModel(model)
			result.Applied = append(result.Applied, field)
		}
	}
	for _, field := range settingsDiff(w.baseline, next) {
		if field != "basic.model" {
			result.RestartRequired = append(result.RestartRequired, field)
		}
	}
	// An empty wiring-time model supplies no known fallback; never call
	// SetModel("") or label clearing a model as applied in that case.
	if w.modelResetPending {
		result.RestartRequired = append(result.RestartRequired, "basic.model")
	}
	w.prev = next
	if len(result.RestartRequired) > 0 {
		result.Err = &RestartRequiredError{
			Fields:  append([]string(nil), result.RestartRequired...),
			Applied: append([]string(nil), result.Applied...),
		}
	}
	w.report(result)
}

// noopReporter ignores reload outcomes. Returned by DiscardReporter.
type noopReporter struct{}

func (noopReporter) OnReload([]string, error) {}

// DiscardReporter is a Reporter that throws every event away. Useful
// for tests and daemon-mode where there's no UI to toast at.
func DiscardReporter() Reporter { return noopReporter{} }

// FileExists is a small helper for callers that want to skip wiring
// when the user config doesn't exist yet (e.g. first boot). Cheaper
// than calling LoadUserOverrides just to detect absence.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
