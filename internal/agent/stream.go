package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type EventType int

const (
	EventToken EventType = iota
	EventToolStart
	EventToolOutput
	EventDone
	EventError
	EventStatus
	EventReasoning
)

type StreamEvent struct {
	Type     EventType              `json:"type"`
	Content  string                 `json:"content,omitempty"`
	ToolCall *providers.ToolCall    `json:"tool_call,omitempty"`
	Result   *RunResult             `json:"result,omitempty"`
	Error    error                  `json:"-"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`

	// Plan-aligned wire fields for SSE streaming.
	Phase      string      `json:"phase,omitempty"`       // for status events
	ToolName   string      `json:"tool_name,omitempty"`   // for tool_call events
	ToolInput  interface{} `json:"tool_input,omitempty"`  // for tool_call events
	ToolOutput string      `json:"tool_output,omitempty"` // for tool_call events
	Model      string      `json:"model,omitempty"`       // for done events
	Tokens     int         `json:"tokens,omitempty"`      // for done events

	// Provenance tags where this message came from (§7.1.9).
	// Set by the gateway/cron/subagent before streaming.
	Provenance *Provenance `json:"provenance,omitempty"`
}

func (a *Agent) Stream(ctx context.Context, userInput string) (<-chan StreamEvent, error) {
	return a.StreamWithProvenance(ctx, userInput, nil, nil)
}

// StreamWithProvenance is the full entry point. Provenance tags the
// message origin (§7.1.9) so the agent knows whether this came from a
// user, sub-agent, or cron job. Callers that don't set provenance get
// the default (user-originated).
func (a *Agent) StreamWithProvenance(ctx context.Context, userInput string, attachments []providers.Attachment, provenance *Provenance) (<-chan StreamEvent, error) {
	ch, err := a.StreamWithAttachments(ctx, userInput, attachments)
	if err != nil {
		return nil, err
	}
	if provenance == nil || provenance.Kind == ProvenanceUser {
		return ch, nil
	}
	// Wrap the channel: prepend the provenance prefix to the user input
	// and tag the first event with the provenance metadata so downstream
	// consumers (web dashboard, journal) can display origin info.
	wrapped := make(chan StreamEvent, 32)
	go func() {
		defer close(wrapped)
		first := true
		for ev := range ch {
			if first {
				first = false
				ev.Provenance = provenance
			}
			select {
			case wrapped <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return wrapped, nil
}

// StreamWithAttachments is the attachment-aware entry point. The plain
// Stream() delegates here with no attachments so existing call sites are
// untouched. Attachments are scanned-against the text input only — the
// bytes themselves bypass the text command scanners since they wouldn't
// produce meaningful matches against image data.
func (a *Agent) StreamWithAttachments(ctx context.Context, userInput string, attachments []providers.Attachment) (<-chan StreamEvent, error) {
	for _, scanner := range a.scanners {
		result, err := scanner.Scan(userInput)
		if err != nil {
			return nil, fmt.Errorf("agent: security scan failed: %w", err)
		}
		if result.Blocked {
			ch := make(chan StreamEvent, 1)
			ch <- StreamEvent{
				Type: EventDone,
				Result: &RunResult{
					Blocked:     true,
					BlockReason: fmt.Sprintf("blocked by %s: %s", scanner.Name(), result.Findings[0].Description),
					Model:       a.Model(),
				},
			}
			close(ch)
			return ch, nil
		}
	}

	// §7.5 Slash command fast-path: parse slash commands in the streaming
	// path so /safe, /auto, /yolo, /plan, /build, /think, and /compact
	// work from TUI and all 8 gateways (not just non-streaming RPC).
	if sc := ParseSlashCommand(userInput); sc != nil {
		msg, handled := a.handleSlashCommand(sc)
		if handled {
			ch := make(chan StreamEvent, 1)
			ch <- StreamEvent{
				Type:   EventDone,
				Result: &RunResult{Response: msg, Model: a.Model()},
				Model:  a.Model(),
			}
			close(ch)
			return ch, nil
		}
	}

	// Resume context: if the prior run was interrupted (user cancel
	// or estop), surface "here's what you were doing" as a system
	// note PREPENDED to this turn's user message. The agent decides
	// whether to continue the prior plan or pivot — we don't auto-
	// resume because the user may have cancelled to redirect.
	if note := a.consumeInterruptNote(); note != "" {
		a.mu.Lock()
		a.history = append(a.history, providers.Message{
			Role:    "system",
			Content: note,
		})
		a.mu.Unlock()
	}

	a.mu.Lock()
	a.history = append(a.history, providers.Message{
		Role:        "user",
		Content:     userInput,
		Attachments: attachments,
	})
	a.mu.Unlock()

	out := make(chan StreamEvent, 64)

	go func() {
		defer close(out)

		// Emit status: thinking phase
		out <- StreamEvent{Type: EventStatus, Phase: "thinking"}

		runResult := &RunResult{
			Model: a.Model(),
		}

		for step := 0; step < a.maxSteps; step++ {
			select {
			case <-ctx.Done():
				// User cancelled mid-task. Save state so the NEXT
				// turn surfaces "you were doing X" context — the
				// agent doesn't forget the plan and panic-improvise.
				a.checkpointInterrupt(userInput, step, "cancelled_by_user")
				out <- StreamEvent{Type: EventError, Error: ctx.Err()}
				return
			case <-a.StopCh():
				// Emergency stop — abort cleanly with a distinct error
				// so the user sees "halted by estop" rather than a
				// generic context-cancelled. Also checkpoint so the
				// next run can see the halt rationale.
				a.checkpointInterrupt(userInput, step, "halted_by_estop")
				out <- StreamEvent{Type: EventError, Error: fmt.Errorf("agent: halted by estop")}
				return
			default:
			}

			a.checkBudget()

			prov := a.provider
			if prov == nil {
				out <- StreamEvent{Type: EventError, Error: fmt.Errorf("agent: provider not configured (Config.Provider was nil)")}
				return
			}
			req := a.buildRequest()

			ch, err := prov.Stream(ctx, req)
			if err != nil {
				if a.hooks != nil {
					a.hooks.Fire(ctx, hooks.OnError, hooks.Event{
						Error:     err,
						SessionID: a.SessionID(),
					})
				}
				a.emitRecovery(err)
				out <- StreamEvent{Type: EventError, Error: fmt.Errorf("agent: stream: %w", err)}
				return
			}

			var contentBuf string
			var toolCalls []providers.ToolCall
			var usage providers.Usage
			var emittedResponding bool

			var streamErr error
			// Explicit select on chunk + ctx + estop. The original
			// `for chunk := range ch` form only checked ctx after a
			// chunk arrived; if the provider goroutine cancelled
			// itself on ctx.Done and closed ch without ever sending,
			// we'd exit the loop cleanly and miss the cancel — the
			// step would then EventDone with empty content instead
			// of checkpointing as an interrupt.
		inner:
			for {
				select {
				case <-ctx.Done():
					a.checkpointInterrupt(userInput, step, "cancelled_by_user_mid_stream")
					out <- StreamEvent{Type: EventError, Error: ctx.Err()}
					return
				case <-a.StopCh():
					a.checkpointInterrupt(userInput, step, "halted_by_estop")
					out <- StreamEvent{Type: EventError, Error: fmt.Errorf("agent: halted by estop")}
					return
				case chunk, ok := <-ch:
					if !ok {
						// Channel closed — provider finished or was
						// cancelled. If ctx is done, treat as cancel;
						// otherwise fall through to post-loop handling
						// (tool calls, EventDone).
						if ctx.Err() != nil {
							a.checkpointInterrupt(userInput, step, "cancelled_by_user_mid_stream")
							out <- StreamEvent{Type: EventError, Error: ctx.Err()}
							return
						}
						break inner
					}

					// Mid-stream transport failure: producer signals via
					// Chunk.Err. We MUST NOT commit the accumulated partial
					// content as an assistant message — that's the silent
					// wrong-answer path. Surface the error and bail.
					if chunk.Err != nil {
						streamErr = chunk.Err
						break inner
					}

					if chunk.Content != "" {
						if !emittedResponding {
							emittedResponding = true
							out <- StreamEvent{Type: EventStatus, Phase: "responding"}
						}
						contentBuf += chunk.Content
						out <- StreamEvent{Type: EventToken, Content: chunk.Content}
					}

					if len(chunk.ToolCalls) > 0 {
						toolCalls = append(toolCalls, chunk.ToolCalls...)
					}

					if chunk.Usage != nil {
						usage = *chunk.Usage
					}
				}
			}

			if streamErr != nil {
				if a.hooks != nil {
					a.hooks.Fire(ctx, hooks.OnError, hooks.Event{
						Error:     streamErr,
						SessionID: a.SessionID(),
					})
				}
				a.emitRecovery(streamErr)
				out <- StreamEvent{Type: EventError, Error: fmt.Errorf("agent: stream: %w", streamErr)}
				return
			}

			runResult.Steps++
			runResult.TotalTokens += usage.InputTokens + usage.OutputTokens

			// §4.10 post-stream filter (e.g. sycophancy strip). Runs
			// once on the assembled text — streaming UX is unchanged.
			filtered := a.applyResponseFilter(contentBuf)

			// Batch G3: hallucination scan against the session's
			// evidence corpus. Annotates [?] after backtick-quoted
			// identifiers that don't appear elsewhere in history.
			// Conservative — annotation only, no deletions; false
			// positives become noise, not data loss.
			if hs := a.getHallucinationScanner(); hs != nil {
				filtered = hs.Scan(filtered, a.buildEvidenceCorpus(nil))
			}

			if len(toolCalls) == 0 {
				runResult.Response = filtered
				runResult.ToolCalls += 0

				a.appendMessage(providers.Message{
					Role:    "assistant",
					Content: filtered,
				})

				// §4.19 journal: record agent reply (no tool calls).
				a.recordFlight(journal.EntryAgentReply, filtered)

				// Surface the final assistant turn as an event so the
				// journal adapter can persist it AND run regex-based
				// derived passes (e.g. failed-hypothesis extraction).
				if filtered != "" {
					a.emit("agent_reply", map[string]any{
						"content":    filtered,
						"session_id": a.SessionID(),
					})
				}

				runResult.Confidence = a.assessTurnConfidence(userInput)
				out <- StreamEvent{Type: EventStatus, Phase: "done"}
				// Fire after_turn hooks (§7.1) — best-effort, never blocks.
				a.fireAfterTurn()
				out <- StreamEvent{
					Type:   EventDone,
					Result: runResult,
					Model:  runResult.Model,
					Tokens: runResult.TotalTokens,
				}
				return
			}

			runResult.ToolCalls += len(toolCalls)

			a.appendMessage(providers.Message{
				Role:      "assistant",
				Content:   filtered,
				ToolCalls: toolCalls,
			})

			// §4.19 journal: record agent reply + tool call dispatch.
			a.recordFlight(journal.EntryAgentReply, filtered+" [dispatched "+fmt.Sprintf("%d", len(toolCalls))+" tool calls]")

			// Same agent_reply emission for the mixed reply+tool_call
			// branch — the agent's prose still warrants persistence and
			// derived extraction. The tool_call event handles the
			// other half separately.
			if filtered != "" {
				a.emit("agent_reply", map[string]any{
					"content":    filtered,
					"session_id": a.SessionID(),
				})
			}

			var toolWg sync.WaitGroup
			toolResults := make([]ToolResult, len(toolCalls))

			// Concurrency control: concurrent-safe tools (Read, Grep,
			// Glob) can run in parallel. Non-concurrent tools (Bash)
			// run exclusively. A Bash error cascades — siblings in
			// the same batch bail instead of executing blindly.
			var toolConcurrencyMu sync.RWMutex
			bashErrCtx, bashErrCancel := context.WithCancel(ctx)
			defer bashErrCancel() // RT: always cancel on scope exit — prevents context leak
			var bashErrOnce sync.Once

			for i, tc := range toolCalls {
				toolWg.Add(1)
				go func(idx int, call providers.ToolCall) {
					defer toolWg.Done()

					// Gate: acquire concurrency lock based on tool kind.
					// Concurrent-safe → RLock (shared). Others → Lock (exclusive).
					_, isConc := a.toolRegistry.GetConcurrency(call.Name, json.RawMessage(call.Arguments))
					if isConc {
						toolConcurrencyMu.RLock()
						defer toolConcurrencyMu.RUnlock()
					} else {
						toolConcurrencyMu.Lock()
						defer toolConcurrencyMu.Unlock()
					}

					// Check sibling abort BEFORE executing.
					select {
					case <-bashErrCtx.Done():
						bailErr := fmt.Errorf("cancelled: a parallel bash command in this batch errored")
						toolResults[idx] = ToolResult{
							ToolCallID: call.ID,
							ToolName:   call.Name,
							Output:     json.RawMessage(`{}`),
							Error:      bailErr,
						}
						out <- StreamEvent{
							Type:      EventToolOutput,
							Content:   call.Name,
							ToolCall:  &call,
							ToolName:  call.Name,
							ToolInput: parseToolInput(call.Arguments),
							Metadata:  map[string]interface{}{"output": "", "error": bailErr},
						}
						return
					default:
					}

					out <- StreamEvent{
						Type:     EventToolStart,
						ToolCall: &call,
						ToolName: call.Name,
						ToolInput: func() interface{} {
							var v interface{}
							if err := json.Unmarshal([]byte(call.Arguments), &v); err != nil {
								return call.Arguments
							}
							return v
						}(),
					}

					if a.hooks != nil {
						a.hooks.Fire(ctx, hooks.BeforeToolCall, hooks.Event{
							ToolName:  call.Name,
							ToolInput: json.RawMessage(call.Arguments),
							SessionID: a.SessionID(),
						})
					}

					if !a.checkToolApproval(call.Name, call.Arguments) {
						deniedErr := fmt.Errorf("tool %q denied by user", call.Name)
						toolResults[idx] = ToolResult{
							ToolCallID: call.ID,
							ToolName:   call.Name,
							Output:     json.RawMessage(`{}`),
							Error:      deniedErr,
						}
						out <- StreamEvent{
							Type:      EventToolOutput,
							Content:   call.Name,
							ToolCall:  &call,
							ToolName:  call.Name,
							ToolInput: parseToolInput(call.Arguments),
							Metadata:  map[string]interface{}{"output": "", "error": deniedErr},
						}
						return
					}

					var input json.RawMessage
					if call.Arguments != "" {
						input = json.RawMessage(call.Arguments)
					} else {
						input = json.RawMessage("{}")
					}

					a.emitImpact(call.Name, input)

					// §4.3 Pre-Exec Command Scanner — same fix as react.go
					// path. A jailbroken model's tool call is scanned HERE,
					// not just on the original user message.
					if blocked, reason := a.preToolScan(call.Name, string(input)); blocked {
						a.emit("tool_call_blocked", map[string]any{
							"tool":       call.Name,
							"reason":     reason,
							"session_id": a.SessionID(),
						})
						blockErr := fmt.Errorf("tool %q blocked by security scanner: %s", call.Name, reason)
						toolResults[idx] = ToolResult{
							ToolCallID: call.ID,
							ToolName:   call.Name,
							Output:     json.RawMessage(`{}`),
							Error:      blockErr,
						}
						out <- StreamEvent{
							Type:      EventToolOutput,
							Content:   call.Name,
							ToolCall:  &call,
							ToolName:  call.Name,
							ToolInput: parseToolInput(call.Arguments),
							Metadata:  map[string]interface{}{"output": "", "error": blockErr},
						}
						return
					}

					// §6.5 Red Team trigger — emit a recommendation
					// when this tool call touches a critical-system
					// path. Non-blocking.
					a.preToolRedTeamCheck(call.Name, input)

					// §4.8 auto-snapshot. Same best-effort policy as the
					// react path; never blocks the tool call.
					if reason, ckErr := a.preToolCheckpoint(call.Name, string(input)); ckErr != nil {
						a.emit("checkpoint_failed", map[string]any{
							"tool":       call.Name,
							"reason":     reason,
							"error":      ckErr.Error(),
							"session_id": a.SessionID(),
						})
					} else if reason != "" {
						a.emit("checkpoint_taken", map[string]any{
							"tool":       call.Name,
							"reason":     reason,
							"session_id": a.SessionID(),
						})
					}

					// Preamble streaming: emit a short natural-language preamble
					// before tool execution in the streaming path.
					if a.thinkEnabled() {
						a.emit("thinking", map[string]any{
							"message":    generatePreamble(call.Name),
							"tool":       call.Name,
							"session_id": a.SessionID(),
						})
					}

					output, toolErr := a.executeTool(ctx, call.Name, input)

					// §4.19 journal: record tool execution. Best-effort only.
					a.recordFlight(journal.EntryToolCall, call.Name+" input="+string(input))

					// Sibling abort cascade: if a Bash/shell tool errors,
					// cancel all other tools in this batch that haven't
					// started yet.
					if toolErr != nil && isBashTool(call.Name) {
						bashErrOnce.Do(func() { bashErrCancel() })
					}

					// Append a cryptographic receipt for the audit
					// chain. Hashes only — no payload bodies — so the
					// chain stays small while still proving "yes, this
					// tool ran with this input and produced this output".
					if a.receipts != nil {
						a.receipts.Append(a.SessionID(), call.Name, input, output, toolErr)
					}

					toolResults[idx] = ToolResult{
						ToolCallID: call.ID,
						ToolName:   call.Name,
						Output:     output,
						Error:      toolErr,
					}

					if a.hooks != nil {
						hookOutput := output
						if toolErr != nil {
							// See react.go: Sprintf produces invalid JSON
							// for errors containing `"`. json.Marshal
							// handles escaping correctly.
							errJSON, _ := json.Marshal(map[string]string{"error": toolErr.Error()})
							hookOutput = json.RawMessage(errJSON)
						}
						a.hooks.Fire(ctx, hooks.AfterToolCall, hooks.Event{
							ToolName:   call.Name,
							ToolInput:  input,
							ToolOutput: hookOutput,
							SessionID:  a.SessionID(),
						})
					}

					out <- StreamEvent{
						Type:       EventToolOutput,
						Content:    call.Name,
						ToolCall:   &call,
						ToolName:   call.Name,
						ToolInput:  parseToolInput(call.Arguments),
						ToolOutput: string(output),
						Metadata: map[string]interface{}{
							"output": string(output),
							"error":  toolErr,
						},
					}
					// §4.1 mid-loop steering drain (Phase 1.5 #1):
					// inject queued user guidance into history so the
					// next provider call sees it. Safe inside the
					// per-call goroutine — drainSteering takes the
					// agent mutex internally and appendMessage is
					// already concurrent-safe.
					a.drainSteering()
				}(i, tc)
			}

			toolWg.Wait()

			for _, tr := range toolResults {
				a.appendToolResultMessage(tr.ToolCallID, tr.ToolName, tr.Output, tr.Error)
			}

			// Post-write verification (Batch G2). For every tool
			// result that succeeded AND is a write-class tool, run
			// the verifier. Failures append as an extra tool message
			// so the model sees "you wrote broken code" on its NEXT
			// turn, inside the same step loop iteration.
			//
			// Also collects the per-turn write paths for the
			// end-of-loop reward-hack audit (paper #48).
			var turnWritePaths []string
			if v := a.getPostWriteVerifier(); v != nil {
				for i, tr := range toolResults {
					if tr.Error != nil || !v.IsWriteTool(tr.ToolName) {
						continue
					}
					call := toolCalls[i]
					var input json.RawMessage
					if call.Arguments != "" {
						input = json.RawMessage(call.Arguments)
					}
					turnWritePaths = append(turnWritePaths, v.ExtractWritePaths(tr.ToolName, input)...)

					note := v.VerifyToolCall(ctx, tr.ToolName, input)
					if note == "" {
						continue
					}
					// Surface the verifier note as a system message
					// only. The previous design also appended a
					// synthetic tool_result with a fabricated
					// tool_use_id ("<id>:verify") to make the verify
					// note look like a tool turn — Anthropic and
					// OpenAI both 400 on tool_result blocks whose
					// tool_use_id isn't in the prior assistant turn,
					// so that path broke every write-class turn.
					// System-message-only is enough: the model reads
					// it on the next step, same as the reward-hack
					// audit below.
					a.appendMessage(providers.Message{
						Role:    "system",
						Content: note,
					})
				}
				// Reward-hack audit (paper #48 design input). One
				// pass per step-iteration over EVERY path the turn
				// touched — sees the cross-file picture
				// VerifyToolCall can't (each call only sees its own
				// inputs). A note here lands BEFORE the next step
				// runs, so the model self-corrects in the same turn.
				if len(turnWritePaths) > 0 {
					if note := v.AuditTurnPaths(dedupPaths(turnWritePaths)); note != "" {
						a.appendMessage(providers.Message{
							Role:    "system",
							Content: note,
						})
					}
				}
			}

			// Reflexion pass (paper #51 AlphaGRPO / Shinn 2023). For
			// each failed tool in this batch, ask the reflector for a
			// structured "you tried X, it failed because Y, try Z"
			// note and inject it as a system message. Budgeted so a
			// many-failure batch doesn't drown the next prompt.
			if rf, budget := a.getReflector(); rf != nil && budget > 0 {
				notes := 0
				for i, tr := range toolResults {
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
					call := toolCalls[i]
					f := Failure{
						ToolName: tr.ToolName,
						Input:    call.Arguments,
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
		}

		// Max-steps exit. If a FlowStore is wired we checkpoint the
		// in-flight state and surface a TimedOut event so the caller
		// can schedule an alarm for resume. Otherwise we exit with the
		// legacy EventError so existing tests + callers without flow
		// support behave unchanged.
		a.mu.RLock()
		store := a.flowStore
		sink := a.flowAlarmSink
		model := a.model
		histCopy := append([]providers.Message(nil), a.history...)
		a.mu.RUnlock()

		reason := fmt.Sprintf("exceeded max steps (%d)", a.maxSteps)
		if store != nil {
			flowID := flowIDFor(a.SessionID(), userInput)
			state, ckErr := CheckpointFlow(
				store, flowID, a.SessionID(), userInput, model, "", histCopy, a.maxSteps, reason,
			)
			if ckErr == nil && state != nil {
				if sink != nil {
					// Sink is best-effort; a panicking sink doesn't
					// take the agent down with it.
					func() {
						defer func() {
							if r := recover(); r != nil {
								// Log via emit so we don't import zerolog here.
								a.emit("flow_alarm_sink_panic", map[string]any{
									"flow_id": state.ID,
									"panic":   fmt.Sprintf("%v", r),
								})
							}
						}()
						sink(state)
					}()
				}
				out <- StreamEvent{
					Type:  EventStatus,
					Phase: "done",
				}
				out <- StreamEvent{
					Type: EventDone,
					Result: &RunResult{
						Model:    model,
						Steps:    a.maxSteps,
						Response: fmt.Sprintf("Task hit max-steps budget. Checkpoint saved (flow %s) — will resume.", state.ID),
					},
					Metadata: map[string]interface{}{
						"flow_checkpoint": state.ID,
						"resumes":         state.Resumes,
					},
				}
				return
			}
			// Checkpoint failed — fall through to EventError so the
			// user sees the error rather than a silent stuck task.
		}

		out <- StreamEvent{
			Type:  EventError,
			Error: fmt.Errorf("agent: %s", reason),
		}
	}()

	return out, nil
}

// consumeInterruptNote returns a system-prompt-style summary of a
// prior interrupted run for the current session, AND clears the
// stored state so the note doesn't replay on every subsequent turn.
// Returns "" when there's nothing to surface (no flow store, no
// matching record, store error).
//
// The note is short on purpose — the model is going to read it once
// per turn, and we don't want a 200-line history dump per message.
// We surface: reason for interrupt, step count at interrupt, and the
// original user input that started the interrupted task. The agent
// can ask the user "should I continue X or pivot?" if the new input
// is ambiguous.
func (a *Agent) consumeInterruptNote() string {
	if a == nil {
		return ""
	}
	a.mu.RLock()
	store := a.flowStore
	a.mu.RUnlock()
	if store == nil {
		return ""
	}
	// List + find the most recent interrupted record for this
	// session. There should be at most one — flow IDs are derived
	// from (session, userInput) so different inputs collide on the
	// hash space but rarely in practice. Newest by CreatedAt wins.
	all, err := store.List()
	if err != nil || len(all) == 0 {
		return ""
	}
	var newest *FlowState
	for _, fs := range all {
		if fs.SessionID != a.SessionID() {
			continue
		}
		if !isInterruptReason(fs.Reason) {
			continue
		}
		if newest == nil || fs.CreatedAt.After(newest.CreatedAt) {
			newest = fs
		}
	}
	if newest == nil {
		return ""
	}
	// Delete first so a panic in the format step doesn't leave the
	// note stuck. The model can always re-derive intent from its
	// own history if the note never lands.
	_ = store.Delete(newest.ID)
	return formatInterruptNote(newest)
}

// isInterruptReason reports whether a checkpoint's reason field
// came from the user-cancel / estop paths (versus a max-steps
// exhaustion which should NOT surface as a resume note — that one
// fires its own alarm-driven resume via the daemon).
func isInterruptReason(reason string) bool {
	switch reason {
	case "cancelled_by_user", "cancelled_by_user_mid_stream", "halted_by_estop":
		return true
	}
	return false
}

// formatInterruptNote turns a FlowState into the system-prompt blob
// injected on the next turn. Short, terse, model-friendly — not a
// human-readable report.
func formatInterruptNote(state *FlowState) string {
	if state == nil {
		return ""
	}
	what := state.Reason
	switch what {
	case "cancelled_by_user", "cancelled_by_user_mid_stream":
		what = "cancelled by the user"
	case "halted_by_estop":
		what = "halted via estop"
	}
	original := state.UserInput
	if len(original) > 200 {
		original = original[:197] + "..."
	}
	return fmt.Sprintf(
		"[resume context] Your previous run was %s at step %d/%d. "+
			"The original request was: %q. If the new message continues that task, pick up "+
			"where you stopped. If it pivots, acknowledge briefly and switch — don't restart "+
			"the old plan silently.",
		what, state.Step, len(state.History), original,
	)
}

// checkpointInterrupt persists the in-flight state when the user
// cancels (ESC twice) or estops (overkill estop). Mirrors the
// max-steps checkpoint path but with a "cancelled_by_user" /
// "halted_by_estop" reason so the next-turn resume context can
// distinguish "agent ran out of budget" from "user pulled the plug".
//
// Best-effort: a flow store that isn't wired (TUI without daemon) is
// a no-op, the interrupt still propagates as EventError. We don't
// invoke the alarm sink — interrupts shouldn't auto-resume on a
// timer; the user will (or won't) come back to the conversation when
// they're ready, and the next manual turn picks up the context.
func (a *Agent) checkpointInterrupt(userInput string, step int, reason string) {
	if a == nil {
		return
	}
	a.mu.RLock()
	store := a.flowStore
	model := a.model
	histCopy := append([]providers.Message(nil), a.history...)
	a.mu.RUnlock()
	if store == nil {
		return
	}
	flowID := flowIDFor(a.SessionID(), userInput)
	// Same defensive recover() pattern as the max-steps path — a
	// misbehaving store mustn't take the cancel path down with it.
	defer func() {
		if r := recover(); r != nil {
			a.emit("checkpoint_interrupt_panic", map[string]any{
				"flow_id": flowID,
				"reason":  reason,
				"panic":   fmt.Sprintf("%v", r),
			})
		}
	}()
	_, _ = CheckpointFlow(store, flowID, a.SessionID(), userInput, model, "", histCopy, step, reason)
}

// parseToolInput unmarshals tool call arguments JSON into an interface{}.
// Used to populate StreamEvent.ToolInput for SSE streaming consumers.
func parseToolInput(args string) interface{} {
	if args == "" {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		return args
	}
	return v
}

// isBashTool reports whether a tool name is Bash/shell-like for
// sibling-abort cascade purposes.
func isBashTool(name string) bool {
	switch name {
	case "bash", "shell", "powershell", "pwsh", "cmd":
		return true
	}
	return false
}
