// Package main — alarm fire dispatcher.
//
// When an alarm fires inside the daemon, this is what runs. The
// design intent (master plan §7.1 Layer 2): a cheap sub-agent reads
// the alarm's prompt, decides whether there's real work, and either
// does it or quick-exits without burning a turn on the main model.
//
// Today's implementation is lean — no tool-use, no multi-step
// reasoning. The cheap model gets one shot: "look at this prompt and
// summarise what should happen next". The output is recorded into the
// ledger so the user sees "alarm fired, here's the AI's take" in
// `overkill task list`. A future iteration can promote this into a
// full sub-agent run with shell access; for now the conservative
// shape keeps token spend predictable.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// daemonFlowStore is set in runDaemonStart so the alarm dispatch
// callback can route resume prompts. Module-level so the fire
// closure doesn't have to thread it through every layer.
var daemonFlowStore agent.FlowStore

// daemonRoutines is the live RoutineEngine, set in runDaemonStart so
// the agent event bus can deliver events to registered routines.
// Nil when the automation Badger DB failed to open.
var daemonRoutines *automation.RoutineEngine

// daemonSOPEngine is the live SOP engine, shared between the alarm
// dispatcher and the §7.1 Layer 3 webhook server so an external
// trigger and an internal alarm can drive the same procedures.
var daemonSOPEngine *automation.SOPEngine

// daemonDashboard is the live §4.19 SSE memory dashboard. Set in
// runDaemonStart; callers (alert sink, observation pipeline)
// broadcast to it when present.
var daemonDashboard *journal.DashboardServer

// alarmDispatchModel is the env override for the cheap-tier model the
// alarm sub-agent uses. Default values are tried in order until one
// resolves against the loaded provider registry.
const alarmDispatchModelEnv = "OVERKILL_ALARM_MODEL"

// pickAlarmModel returns the cheapest available model suitable for
// alarm dispatch. Prefers OVERKILL_ALARM_MODEL env var, then scans
// the loaded provider registry for the cheapest capable model.

// alarmDispatchFire returns a fire callback suitable for AlarmClock.
// The callback runs the alarm's Prompt through a cheap model, records
// the run in the ledger, and stores the model's one-line summary back
// on the alarm.Result field so the user can see what happened.
//
// Failures are recorded but never panic — a misconfigured model
// shouldn't take the daemon down.
func alarmDispatchFire(ledger *automation.Ledger) func(*automation.Alarm) error {
	return func(a *automation.Alarm) error {
		// Prefer Prompt (new field). Legacy alarms used Action as a
		// shell command — keep that path working by falling back when
		// Prompt is empty.
		prompt := strings.TrimSpace(a.Prompt)
		legacyShell := strings.TrimSpace(a.Action)
		if prompt == "" && legacyShell == "" {
			return errors.New("alarm has no prompt or action")
		}

		entry := ledger.Begin("alarm", a.Name)
		defer ledger.Heartbeat(entry.ID) // last heartbeat before we Complete/Fail

		// Resume path: if the prompt is "overkill:flow:resume:<id>",
		// route to ResumeFlow instead of the cheap-model summariser.
		// Resume builds a full agent (history restored) and re-enters
		// the stream loop.
		if flowID := agent.ExtractFlowID(prompt); flowID != "" {
			result, err := runFlowResume(flowID)
			if err != nil {
				ledger.Fail(entry.ID, err)
				a.Result = "resume failed: " + err.Error()
				fmt.Fprintf(os.Stderr, "alarm flow resume: %v\n", err)
				return err
			}
			ledger.Complete(entry.ID, firstLine(result))
			a.Result = firstLine(result)
			return nil
		}

		// Legacy shell path: run as before. The new dispatcher only
		// kicks in for prompt-bearing alarms — this keeps backwards
		// compatibility for any caller that set up shell alarms before
		// the prompt field existed.
		if prompt == "" {
			out, err := shellExecutor(legacyShell)
			if err != nil {
				ledger.Fail(entry.ID, err)
				a.Result = "shell failed: " + err.Error()
				return err
			}
			ledger.Complete(entry.ID, firstLine(out))
			a.Result = firstLine(out)
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result, err := runAlarmSubAgent(ctx, prompt)
		if err != nil {
			ledger.Fail(entry.ID, err)
			a.Result = "dispatch failed: " + err.Error()
			fmt.Fprintf(os.Stderr, "alarm dispatch: %v\n", err)
			return err
		}
		ledger.Complete(entry.ID, result)
		a.Result = result
		return nil
	}
}

// runAlarmSubAgent loads provider config, picks the cheap-tier model,
// and issues a single Complete call with a system prompt scoped to
// "answer briefly; declare actionable next steps OR explicitly say
// nothing-to-do". Returns the response text.
func runAlarmSubAgent(ctx context.Context, prompt string) (string, error) {
	configPath, err := config.ConfigPath()
	if err != nil {
		return "", fmt.Errorf("resolve config path: %w", err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	if cfg == nil || len(cfg.Providers) == 0 {
		return "", errors.New("no providers configured — alarm sub-agent needs at least one provider in ~/.overkill/config.toml")
	}

	// Resolve the model. Env override wins; otherwise walk the default
	// list and pick the first that has a configured provider.
	modelID := strings.TrimSpace(os.Getenv(alarmDispatchModelEnv))
	if modelID == "" {
		modelID = pickCheapModel(cfg)
	}
	if modelID == "" {
		return "", errors.New("no cheap-tier model available — set OVERKILL_ALARM_MODEL or configure a haiku/mini class model")
	}

	provider, err := providersByModel(cfg, modelID)
	if err != nil {
		return "", fmt.Errorf("resolve provider for %s: %w", modelID, err)
	}

	req := providers.Request{
		Model: modelID,
		Messages: []providers.Message{
			{Role: "user", Content: prompt},
		},
		SystemPrompt: alarmSubAgentSystemPrompt,
		MaxTokens:    300,
		Temperature:  0.2,
	}
	resp, err := provider.Complete(ctx, req)
	if err != nil {
		return "", fmt.Errorf("provider call: %w", err)
	}
	return firstLine(resp.Content), nil
}

// alarmSubAgentSystemPrompt is the contract the cheap model operates
// under. Keep it short — every token here is paid per fire.
const alarmSubAgentSystemPrompt = `You are an alarm-handler sub-agent for Overkill.
The user set an alarm with a brief instruction. You have NO tools — just
respond with one of:
- "<one-line summary of what should happen next>" if action is needed
- "nothing to do" if the alarm's premise no longer applies
Be terse. Max 200 chars. No preamble. No follow-up questions.`

// pickCheapModel scans configured providers and returns the cheapest
// model suitable for alarm dispatch. Falls back to cfg.Agent.DefaultModel.
func pickCheapModel(cfg *config.Config) string {
	// If OVERKILL_ALARM_MODEL env is set, use it directly.
	if env := os.Getenv(alarmDispatchModelEnv); env != "" {
		return env
	}
	// Scan all configured providers for their cheapest model.
	var best struct {
		id    string
		cost  float64
		found bool
	}
	for _, p := range cfg.Providers {
		for _, m := range p.Models {
			cost := m.CostOut
			if cost > 0 && (!best.found || cost < best.cost) {
				best.id = m.ID
				best.cost = cost
				best.found = true
			}
		}
	}
	if best.found {
		return best.id
	}
	return cfg.Agent.DefaultModel
}

// providersByModel hunts for a provider that can run modelID. Uses
// the existing factory so this stays in lock-step with the TUI / CLI
// provider construction.
func providersByModel(cfg *config.Config, modelID string) (providers.Provider, error) {
	vendor := ""
	if slash := strings.Index(modelID, "/"); slash > 0 {
		vendor = strings.ToLower(modelID[:slash])
	}
	for _, p := range cfg.Providers {
		if vendor != "" && !strings.EqualFold(p.Type, vendor) {
			continue
		}
		apiKey := p.APIKey
		if apiKey == "" {
			apiKey = os.Getenv(providerEnvVar(p.Name))
		}
		provider, err := providers.NewProvider(providers.FactoryConfig{
			Name:    p.Name,
			Type:    p.Type,
			APIKey:  apiKey,
			BaseURL: p.BaseURL,
			Headers: p.Headers,
		})
		if err == nil {
			return provider, nil
		}
	}
	return nil, fmt.Errorf("no configured provider matches model %s", modelID)
}

// runFlowResume is the bridge between an alarm fire and the agent's
// ResumeFlow entrypoint. Builds a daemon-scoped agent (cheap model,
// fresh tool registry) and re-enters the streaming loop from where
// the original task timed out.
//
// We use the same cheap-tier model as the alarm dispatch summariser
// because the resume is "continue this task carefully", not "fresh
// reasoning from scratch" — the history is already there, the model
// just needs to take the next step.
func runFlowResume(flowID string) (string, error) {
	if daemonFlowStore == nil {
		return "", errors.New("flow resume: store not wired (daemon started without flow support)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Build a thin agent. We don't have the full TUI's tool registry
	// here; resume runs with NO tools by design — the original task's
	// state is in history, the model just needs to summarise where it
	// is and what's left, OR finish if it can. A future iteration can
	// re-enable tool use for the resume path; today we play it safe.
	cfg, err := configForDaemon()
	if err != nil {
		return "", err
	}
	model := pickCheapModel(cfg)
	if model == "" {
		return "", errors.New("flow resume: no cheap-tier model available")
	}
	provider, err := providersByModel(cfg, model)
	if err != nil {
		return "", fmt.Errorf("flow resume: %w", err)
	}

	a, err := buildResumeAgent(provider, model)
	if err != nil {
		return "", fmt.Errorf("flow resume: build agent: %w", err)
	}

	content, state, err := agent.ResumeFlow(ctx, daemonFlowStore, flowID, a)
	if err != nil {
		if errors.Is(err, agent.ErrFlowExhausted) {
			return "", fmt.Errorf("flow %s exhausted after %d resumes — giving up", flowID, state.Resumes)
		}
		if errors.Is(err, agent.ErrFlowCorrupt) {
			return "", fmt.Errorf("flow %s corrupt — not retrying", flowID)
		}
		return "", err
	}
	return content, nil
}

// configForDaemon loads config the same way runAlarmSubAgent does.
// Pulled out so flow resume and prompt dispatch share one path.
func configForDaemon() (*config.Config, error) {
	configPath, err := config.ConfigPath()
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if cfg == nil || len(cfg.Providers) == 0 {
		return nil, errors.New("no providers configured")
	}
	return cfg, nil
}

// buildResumeAgent constructs an agent suitable for resume. No tools,
// no scanners, no fancy hooks — the resume contract is "model
// continues the conversation"; everything that originally drove the
// task lived in the TUI's agent and is gone now. The history carries
// the intent forward.
func buildResumeAgent(p providers.Provider, model string) (*agent.Agent, error) {
	return agent.New(agent.Config{
		Provider:  p,
		Model:     model,
		MaxSteps:  20, // short — resume is "finish what you can", not exploration
		MaxTokens: 1500,
	}), nil
}

// firstLine extracts the first non-empty line and trims it to 200
// chars. Used to keep ledger Result and alarm.Result short — the
// status surfaces are not log dumps.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			if len(t) > 200 {
				t = t[:197] + "..."
			}
			return t
		}
	}
	return ""
}
