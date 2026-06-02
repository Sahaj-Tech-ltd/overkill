package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
)

type StepResult struct {
	Thought     string
	Action      string
	Observation string
	ToolCalls   []providers.ToolCall
	ToolResults []ToolResult
	Tokens      providers.Usage
	Done        bool
}

type ToolResult struct {
	ToolCallID string          `json:"tool_call_id"`
	ToolName   string          `json:"tool_name"`
	Output     json.RawMessage `json:"output"`
	Error      error           `json:"-"`
}

func (a *Agent) step(ctx context.Context) (*StepResult, error) {
	prov := a.provider
	if prov == nil {
		return nil, fmt.Errorf("agent: provider not configured (Config.Provider was nil)")
	}
	req := a.buildRequest()

	resp, err := providers.WithRetryCtx(ctx, func() (*providers.Response, error) {
		r, e := prov.Complete(ctx, req)
		return &r, e
	}, func(err error) bool {
		var httpErr *providers.HTTPError
		if errors.As(err, &httpErr) {
			return httpErr.IsRetryable()
		}
		return false
	})
	if err != nil {
		return nil, fmt.Errorf("provider complete: %w", err)
	}

	if resp.Model != "" {
		// SetModel takes the write lock; bare assignment races with the
		// many RLock-protected reads of a.model elsewhere (OpenCode #2).
		a.SetModel(resp.Model)
	}

	result := &StepResult{
		Thought:   resp.Content,
		Tokens:    resp.Usage,
		ToolCalls: resp.ToolCalls,
	}

	// §4.10 post-response filter (sycophancy strip etc.). Mirrors
	// the stream path. The react path is the non-streaming variant
	// used by `overkill run`; both code paths now share the cleanup.
	filtered := a.applyResponseFilter(resp.Content)

	if len(resp.ToolCalls) == 0 {
		a.appendMessage(providers.Message{
			Role:    "assistant",
			Content: filtered,
		})
		result.Done = true
		result.Thought = filtered
		// §4.19 journal: record agent reply (no tool calls).
		a.recordFlight(journal.EntryAgentReply, filtered)
		return result, nil
	}

	a.appendMessage(providers.Message{
		Role:      "assistant",
		Content:   filtered,
		ToolCalls: resp.ToolCalls,
	})

	// §4.19 journal: record agent reply + tool call dispatch.
	a.recordFlight(journal.EntryAgentReply, filtered+" [dispatched "+fmt.Sprintf("%d", len(resp.ToolCalls))+" tool calls]")

	result.ToolResults = make([]ToolResult, 0, len(resp.ToolCalls))

	for _, tc := range resp.ToolCalls {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		if a.hooks != nil {
			a.hooks.Fire(ctx, hooks.BeforeToolCall, hooks.Event{
				ToolName:  tc.Name,
				ToolInput: json.RawMessage(tc.Arguments),
				SessionID: a.SessionID(),
			})
		}

		if !a.checkToolApproval(tc.Name, tc.Arguments) {
			deniedErr := fmt.Errorf("tool %q denied by user", tc.Name)
			result.ToolResults = append(result.ToolResults, ToolResult{
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Error:      deniedErr,
			})
			a.appendToolResultMessage(tc.ID, tc.Name, json.RawMessage(`{}`), deniedErr)
			continue
		}

		var input json.RawMessage
		if tc.Arguments != "" {
			input = json.RawMessage(tc.Arguments)
		} else {
			input = json.RawMessage("{}")
		}

		// Forethought: assess the impact of this tool call BEFORE execution
		// so subscribers (plugins, journal, telemetry) can flag protected-path
		// edits, dangerous shell, etc. Defensive — never panics the loop.
		a.emitImpact(tc.Name, input)

		// Master plan §4.3 Pre-Exec Command Scanner: re-run the security
		// scanners against the LLM-generated tool call. The user-input
		// scan at Run() entry doesn't see this — a jailbroken model can
		// synthesise destructive commands that bypass that gate. We feed
		// the scanner the actual command/path so deny patterns fire HERE
		// even when the original user message looked benign.
		if blocked, reason := a.preToolScan(tc.Name, string(input)); blocked {
			a.emit("tool_call_blocked", map[string]any{
				"tool":       tc.Name,
				"reason":     reason,
				"session_id": a.SessionID(),
			})
			blockErr := fmt.Errorf("tool %q blocked by security scanner: %s", tc.Name, reason)
			result.ToolResults = append(result.ToolResults, ToolResult{
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Error:      blockErr,
			})
			a.appendToolResultMessage(tc.ID, tc.Name, json.RawMessage(`{}`), blockErr)
			continue
		}

		// §6.5 Red Team trigger: emit a recommendation when a
		// write-class tool touches a critical-system path (auth /
		// crypto / payments / data-loss). Non-blocking — surfaces
		// the suggestion via event so the user can opt into
		// wall_ouroboros without paying the LLM cost on every call.
		a.preToolRedTeamCheck(tc.Name, input)

		// §4.8: auto-snapshot before destructive ops. Best-effort — a
		// snapshot failure is logged via emit but does NOT block the
		// tool. The user's intent is primary; the snapshot is a safety
		// net, not a gate.
		if reason, err := a.preToolCheckpoint(tc.Name, string(input)); err != nil {
			a.emit("checkpoint_failed", map[string]any{
				"tool":       tc.Name,
				"reason":     reason,
				"error":      err.Error(),
				"session_id": a.SessionID(),
			})
		} else if reason != "" {
			a.emit("checkpoint_taken", map[string]any{
				"tool":       tc.Name,
				"reason":     reason,
				"session_id": a.SessionID(),
			})
		}

		// Notify subscribers (plugins, journal) before execution.
		a.emit("tool_call", map[string]any{
			"tool":       tc.Name,
			"input":      string(input),
			"session_id": a.SessionID(),
		})
		// Preamble streaming: emit a short natural-language preamble
		// before tool execution (like Codex's "thinking" messages).
		if a.thinkEnabled() {
			a.emit("thinking", map[string]any{
				"message":    generatePreamble(tc.Name),
				"tool":       tc.Name,
				"session_id": a.SessionID(),
			})
		}
		toolResult, toolErr := a.executeTool(ctx, tc.Name, input)

		// §4.19 journal: record tool execution. Best-effort only.
		a.recordFlight(journal.EntryToolCall, tc.Name+" input="+string(input))

		// Append to the cryptographic receipt chain so the non-streaming
		// react path produces the same audit trail as the streaming path.
		// Old code only appended in stream.go — every TUI / Run()-based
		// tool call (the entire non-streaming code path) had no record.
		if a.receipts != nil {
			a.receipts.Append(a.SessionID(), tc.Name, input, toolResult, toolErr)
		}

		if a.hooks != nil {
			hookOutput := toolResult
			if toolErr != nil {
				// json.Marshal escapes embedded quotes; Sprintf'd
				// {"error":"%s"} produced invalid JSON when the error
				// text contained a `"` (e.g. `file "x" not found`).
				errJSON, _ := json.Marshal(map[string]string{"error": toolErr.Error()})
				hookOutput = json.RawMessage(errJSON)
			}
			a.hooks.Fire(ctx, hooks.AfterToolCall, hooks.Event{
				ToolName:   tc.Name,
				ToolInput:  input,
				ToolOutput: hookOutput,
				SessionID:  a.SessionID(),
			})
		}

		result.ToolResults = append(result.ToolResults, ToolResult{
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			Output:     toolResult,
			Error:      toolErr,
		})

		a.appendToolResultMessage(tc.ID, tc.Name, toolResult, toolErr)

		// §4.1 mid-loop steering drain (Phase 1.5 #1): if the user
		// injected guidance between tool calls, append it to history
		// now so the next model step sees it. Two modes baked into
		// the SteeringQueue: one-at-a-time and drain-all.
		//
		// L15: yield for up to 30ms so a steering message in-flight
		// (e.g. gateway /steer arriving concurrently) lands before we
		// drain. Wait() blocks while the queue is empty; a very short
		// timeout means this is essentially a non-blocking check.
		a.mu.RLock()
		sq := a.steering
		a.mu.RUnlock()
		if sq != nil && sq.Pending() == 0 {
			yieldCtx, yieldCancel := context.WithTimeout(ctx, 30*time.Millisecond)
			_ = sq.Wait(yieldCtx)
			yieldCancel()
		}
		a.drainSteering()
	}

	// Reflexion pass (paper #51 AlphaGRPO / Shinn 2023). Same shape
	// as the stream-path injection in stream.go — for each failed
	// tool, produce a structured note and append as a system message
	// before the next model turn. Budgeted to avoid drowning the
	// next prompt when many tools fail at once.
	if rf, budget := a.getReflector(); rf != nil && budget > 0 {
		notes := 0
		for i, tr := range result.ToolResults {
			if notes >= budget {
				break
			}
			errStr := ""
			if tr.Error != nil {
				errStr = tr.Error.Error()
			}
			outStr := string(tr.Output)
			if errStr == "" && !rf.IsFailure(tr.ToolName, outStr, "") {
				continue
			}
			callArgs := ""
			if i < len(result.ToolCalls) {
				callArgs = result.ToolCalls[i].Arguments
			}
			f := Failure{
				ToolName: tr.ToolName,
				Input:    callArgs,
				Output:   outStr,
				Error:    errStr,
			}
			refl := rf.Reflect(f)
			note := rf.FormatNote(tr.ToolName, refl)
			if note == "" {
				continue
			}
			a.appendMessage(providers.Message{
				Role:    "system",
				Content: note,
			})
			notes++
			a.emit("reflexion", map[string]any{
				"tool":       tr.ToolName,
				"mode":       refl.Mode,
				"confidence": refl.Confidence,
				"session_id": a.SessionID(),
			})
		}
	}

	return result, nil
}

// checkToolApproval gates a tool call on the user's permission decision.
// Tools are considered risky if their name or arguments match common destructive
// patterns; everything else is auto-allowed.
func (a *Agent) checkToolApproval(toolName, args string) bool {
	risk := classifyToolRisk(toolName, args)
	if risk == "low" {
		return true
	}
	return a.approvalCheck(toolName, args, risk)
}

// classifyToolRisk returns a coarse risk label for permission UX. Anything
// touching the shell, filesystem writes, or network mutations is at least medium.
func classifyToolRisk(toolName, args string) string {
	switch toolName {
	case "shell", "bash":
		return "high"
	case "fs_write", "write_file", "edit_file", "fs_delete", "patch":
		return "medium"
	case "git":
		return "medium"
	case "web_fetch":
		return "low"
	case "browser_eval":
		return "high"
	case "browser_click", "browser_fill", "browser_select":
		return "medium"
	case "browser_open", "browser_screenshot", "browser_text",
		"browser_markdown", "browser_wait":
		return "low"
	case "browser_navigate":
		var a struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal([]byte(args), &a)
		if a.URL == "" {
			return "low"
		}
		s := strings.ToLower(a.URL)
		switch {
		case strings.HasPrefix(s, "javascript:"),
			strings.HasPrefix(s, "data:"),
			strings.HasPrefix(s, "vbscript:"),
			strings.HasPrefix(s, "chrome:"):
			return "high"
		case strings.HasPrefix(s, "file:"), strings.HasPrefix(s, "ftp:"):
			return "medium"
		default:
			return "low"
		}
	}
	return "low"
}

func (a *Agent) executeTool(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error) {
	if a.toolRegistry == nil {
		return nil, fmt.Errorf("no tool registry configured")
	}

	// Privilege gate (master plan §4.3): in reader mode, write-like calls
	// are denied with a structured error the model can see and react to
	// (e.g. ask the user to flip mode).
	if g := a.privilege.Load(); g != nil {
		if ok, why := g.Allow(name, input); !ok {
			return nil, fmt.Errorf("%w: %s", security.ErrWriteDenied, why)
		}
	}

	t, err := a.toolRegistry.Get(name)
	if err != nil {
		return nil, fmt.Errorf("tool %q not found", name)
	}

	output, err := t.Execute(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("tool %q execution failed: %w", name, err)
	}

	return output, nil
}
