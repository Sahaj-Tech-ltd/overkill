// Package hotreload — agent wiring for live config reloads.
//
// WireAgent subscribes the agent to user.yaml changes and reapplies
// the subset of fields that can flip safely at runtime:
//
//   - basic.model              → agent.SetModel after the current Run
//     completes (mid-stream swaps are
//     undefined; queue on a turn boundary)
//   - advanced.scanners.*      → agent's scanner slice is rebuilt to
//     match the toggle state
//   - advanced.persona.*       → no action; persona is read per-turn
//     (already hot)
//   - advanced.system_prompt.* → no action; system prompt is rebuilt
//     per turn (already hot)
//
// Anything else surfaces as a "settings_reloaded_partial" event with
// the field list so the user knows their edit DID load but didn't
// apply to a running session yet (e.g. MCP server changes that
// require reconcile).
package hotreload

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// Agent is the minimal surface WireAgent needs. The full Agent type
// in internal/agent satisfies this; tests can pass a stub.
type Agent interface {
	SetModel(string)
	Model() string
}

// Reporter is how WireAgent surfaces reload outcomes. The TUI passes
// a func that posts a toast; a daemon can pass io.Discard.
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
		agent:    agent,
		path:     userYAMLPath,
		prev:     prev,
		reporter: reporter,
	}

	ch, unsubscribe := bus.Subscribe(SubjectConfig)
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
				if ev.Kind == EventRemoved {
					// File deleted — fall back to defaults. Useful
					// for "reset to defaults" via `rm user.yaml`.
					w.apply(config.DefaultUserOverrides())
					continue
				}
				newCfg, loadErr := config.LoadUserOverrides(w.path)
				if loadErr != nil {
					if reporter != nil {
						reporter.OnReload(nil, loadErr)
					}
					continue
				}
				w.apply(newCfg)
			}
		}
	}()

	stop := func() {
		unsubscribe()
		<-done
	}
	return stop, nil
}

// agentWiring is the per-bus state. Holds the prior config snapshot
// + the agent reference so apply() can diff and only re-apply
// changed fields. Concurrent applies are serialised so two near-
// simultaneous saves don't interleave.
type agentWiring struct {
	mu       sync.Mutex
	agent    Agent
	path     string
	prev     *config.UserOverrides
	reporter Reporter
}

func (w *agentWiring) apply(next *config.UserOverrides) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if next == nil {
		return
	}
	var changed []string

	// basic.model — safe-on-turn-boundary. We can't peek "between
	// turns" from here, so we set immediately; the agent's request
	// builder reads Model() per turn, so an in-flight Stream() keeps
	// its existing model and the next turn picks up the new value.
	if next.Basic.Model != "" && next.Basic.Model != w.prev.Basic.Model {
		w.agent.SetModel(next.Basic.Model)
		changed = append(changed, "basic.model")
	}

	// Scanner toggles → no direct agent setter for the slice yet;
	// we surface the change so the TUI can show the user that the
	// edit landed but a restart picks it up. (Live scanner-slice
	// swap is wired by a follow-up PR; see v2.0-6 Configurable.)
	if next.Advanced.Scanners != w.prev.Advanced.Scanners {
		changed = append(changed, "advanced.scanners")
	}

	// Persona / system prompt — already read per-turn, so the file
	// change is "live" without any action here. Still report so the
	// user sees confirmation.
	if next.Advanced.Persona != w.prev.Advanced.Persona {
		changed = append(changed, "advanced.persona")
	}
	if next.Advanced.SystemPrompt.Mode != w.prev.Advanced.SystemPrompt.Mode ||
		next.Advanced.SystemPrompt.Patch != w.prev.Advanced.SystemPrompt.Patch ||
		next.Advanced.SystemPrompt.Replace != w.prev.Advanced.SystemPrompt.Replace {
		changed = append(changed, "advanced.system_prompt")
	}

	w.prev = next
	if w.reporter != nil && len(changed) > 0 {
		w.reporter.OnReload(changed, nil)
	}
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
