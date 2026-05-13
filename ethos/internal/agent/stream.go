package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type EventType int

const (
	EventToken EventType = iota
	EventToolStart
	EventToolOutput
	EventDone
	EventError
)

type StreamEvent struct {
	Type     EventType              `json:"type"`
	Content  string                 `json:"content,omitempty"`
	ToolCall *providers.ToolCall    `json:"tool_call,omitempty"`
	Result   *RunResult             `json:"result,omitempty"`
	Error    error                  `json:"-"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

func (a *Agent) Stream(ctx context.Context, userInput string) (<-chan StreamEvent, error) {
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

	a.mu.Lock()
	a.history = append(a.history, providers.Message{
		Role:    "user",
		Content: userInput,
	})
	a.mu.Unlock()

	out := make(chan StreamEvent, 64)

	go func() {
		defer close(out)

		runResult := &RunResult{
			Model: a.Model(),
		}

		for step := 0; step < a.maxSteps; step++ {
			select {
			case <-ctx.Done():
				out <- StreamEvent{Type: EventError, Error: ctx.Err()}
				return
			default:
			}

			a.checkBudget()

			req := a.buildRequest()

			ch, err := a.provider.Stream(ctx, req)
			if err != nil {
				if a.hooks != nil {
					a.hooks.Fire(ctx, hooks.OnError, hooks.Event{
						Error:     err,
						SessionID: a.sessionID,
					})
				}
				a.emitRecovery(err)
				out <- StreamEvent{Type: EventError, Error: fmt.Errorf("agent: stream: %w", err)}
				return
			}

			var contentBuf string
			var toolCalls []providers.ToolCall
			var usage providers.Usage

			var streamErr error
			for chunk := range ch {
				select {
				case <-ctx.Done():
					out <- StreamEvent{Type: EventError, Error: ctx.Err()}
					return
				default:
				}

				// Mid-stream transport failure: producer signals via
				// Chunk.Err. We MUST NOT commit the accumulated partial
				// content as an assistant message — that's the silent
				// wrong-answer path. Surface the error and bail.
				if chunk.Err != nil {
					streamErr = chunk.Err
					break
				}

				if chunk.Content != "" {
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

			if streamErr != nil {
				if a.hooks != nil {
					a.hooks.Fire(ctx, hooks.OnError, hooks.Event{
						Error:     streamErr,
						SessionID: a.sessionID,
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

			if len(toolCalls) == 0 {
				runResult.Response = filtered
				runResult.ToolCalls += 0

				a.appendMessage(providers.Message{
					Role:    "assistant",
					Content: filtered,
				})

				runResult.Confidence = a.assessTurnConfidence(userInput)
				out <- StreamEvent{Type: EventDone, Result: runResult}
				return
			}

			runResult.ToolCalls += len(toolCalls)

			a.appendMessage(providers.Message{
				Role:      "assistant",
				Content:   filtered,
				ToolCalls: toolCalls,
			})

			var toolWg sync.WaitGroup
			toolResults := make([]ToolResult, len(toolCalls))

			for i, tc := range toolCalls {
				toolWg.Add(1)
				go func(idx int, call providers.ToolCall) {
					defer toolWg.Done()

					out <- StreamEvent{
						Type:     EventToolStart,
						ToolCall: &call,
					}

					if a.hooks != nil {
						a.hooks.Fire(ctx, hooks.BeforeToolCall, hooks.Event{
							ToolName:  call.Name,
							ToolInput: json.RawMessage(call.Arguments),
							SessionID: a.sessionID,
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
							Type:     EventToolOutput,
							Content:  call.Name,
							ToolCall: &call,
							Metadata: map[string]interface{}{"output": "", "error": deniedErr},
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
							"session_id": a.sessionID,
						})
						blockErr := fmt.Errorf("tool %q blocked by security scanner: %s", call.Name, reason)
						toolResults[idx] = ToolResult{
							ToolCallID: call.ID,
							ToolName:   call.Name,
							Output:     json.RawMessage(`{}`),
							Error:      blockErr,
						}
						out <- StreamEvent{
							Type:     EventToolOutput,
							Content:  call.Name,
							ToolCall: &call,
							Metadata: map[string]interface{}{"output": "", "error": blockErr},
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
							"session_id": a.sessionID,
						})
					} else if reason != "" {
						a.emit("checkpoint_taken", map[string]any{
							"tool":       call.Name,
							"reason":     reason,
							"session_id": a.sessionID,
						})
					}

					output, toolErr := a.executeTool(ctx, call.Name, input)

					toolResults[idx] = ToolResult{
						ToolCallID: call.ID,
						ToolName:   call.Name,
						Output:     output,
						Error:      toolErr,
					}

					if a.hooks != nil {
						hookOutput := output
						if toolErr != nil {
							hookOutput = json.RawMessage(fmt.Sprintf(`{"error":"%s"}`, toolErr.Error()))
						}
						a.hooks.Fire(ctx, hooks.AfterToolCall, hooks.Event{
							ToolName:   call.Name,
							ToolInput:  input,
							ToolOutput: hookOutput,
							SessionID:  a.sessionID,
						})
					}

					out <- StreamEvent{
						Type:     EventToolOutput,
						Content:  call.Name,
						ToolCall: &call,
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
		}

		out <- StreamEvent{
			Type:  EventError,
			Error: fmt.Errorf("agent: exceeded max steps (%d)", a.maxSteps),
		}
	}()

	return out, nil
}
