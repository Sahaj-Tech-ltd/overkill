package agent

import (
	"context"
	"fmt"
	"time"
)

// streamSequential owns the outer admission while each normal streaming loop
// runs on the same Agent, with its current item history/authority and goal lease.
func (a *Agent) streamSequential(ctx context.Context, input string, checkpoint *SequentialCheckpoint, existing ...*GoalAccountingState) (<-chan StreamEvent, error) {
	var supplied *GoalAccountingState
	if len(existing) > 0 {
		supplied = existing[0]
	}
	releaseSupplied := func() {
		if supplied != nil {
			_ = supplied.RecordTurnEnd(ctx, 0)
			supplied.cancel(nil)
		}
	}
	queue, err := a.beginSequential(input, checkpoint)
	if err != nil {
		releaseSupplied()
		a.running.Store(false)
		return nil, err
	}
	workspace, err := openSequentialWorkspace(ctx, a.mentionWorkspace)
	if err != nil {
		releaseSupplied()
		a.finishSequential()
		a.running.Store(false)
		return nil, err
	}
	var goalTurn *GoalAccountingState
	if len(existing) > 0 {
		goalTurn = existing[0]
	}
	if goalTurn == nil {
		ctx, goalTurn, err = a.beginGoalTurn(ctx)
		if err != nil {
			workspace.Close()
			a.finishSequential()
			a.running.Store(false)
			return nil, err
		}
	}
	out := make(chan StreamEvent, 64)
	go func() {
		defer close(out)
		defer a.running.Store(false)
		defer a.finishSequential()
		defer workspace.Close()
		if goalTurn != nil {
			defer goalTurn.cancel(nil)
		}
		var terminalErr error
		var terminalResult *RunResult
		a.mu.RLock()
		start, total := a.sequentialExecution.current, len(a.sequentialExecution.items)
		a.mu.RUnlock()
		for index := start; index < total; index++ {
			continuing := checkpoint != nil && index == start && checkpoint.Started
			var scope string
			if continuing {
				scope = checkpoint.Scope
			} else {
				scope, err = a.beginSequentialItem(index)
				if err != nil {
					terminalErr = err
					break
				}
			}
			a.mu.RLock()
			item := a.sequentialExecution.items[index]
			a.mu.RUnlock()
			queue.UpdateItem(item.Index, "active", "", 0)
			itemCtx, cancel, scopeErr := a.sequentialItemContext(ctx, scope)
			if scopeErr != nil {
				terminalErr = scopeErr
				break
			}
			var prompt sequentialItemPrompt
			var events <-chan StreamEvent
			if !continuing {
				prompt, err = workspace.ItemPrompt(itemCtx, item, total, a.sequentialFileBudget())
			} else {
				prompt.UserInput = item.Description
			}
			if err == nil {
				var userPrompt string
				itemCtx, userPrompt = withSequentialPrompt(itemCtx, prompt)
				events, err = a.streamWithAttachments(itemCtx, userPrompt, nil, continuing, true)
			}
			var itemResult *RunResult
			var itemErr error
			checkpointed := false
			if err != nil {
				itemErr = err
			} else {
				for event := range events {
					if event.Type == EventError {
						itemErr = event.Error
						if itemErr == nil {
							itemErr = fmt.Errorf("sequential item failed")
						}
						continue
					}
					if event.Type == EventDone {
						itemResult = event.Result
						if event.Metadata != nil && event.Metadata["flow_checkpoint"] != nil {
							checkpointed = true
							copy := event
							copy.Result = nil
							copy.Type = EventStatus
							copy.Phase = "checkpointed"
							sendStreamEvent(ctx, out, copy)
						}
						continue
					}
					sendStreamEvent(ctx, out, event)
				}
				if itemErr == nil && itemResult == nil {
					itemErr = fmt.Errorf("sequential item stream ended without completion")
				}
			}
			if itemResult != nil && itemResult.Blocked {
				itemErr = fmt.Errorf("sequential item blocked: %s", itemResult.BlockReason)
			}
			if itemCtx.Err() != nil && !a.sequentialCheckpointed() {
				a.checkpointContextExit(itemCtx, input, 0, "cancelled_by_user")
			}
			if checkpointed && itemErr == nil {
				itemErr = fmt.Errorf("sequential item checkpoint saved; awaiting continuation")
			}
			saved := a.finishSequentialItem(itemResult, itemErr)
			checkpointed = checkpointed || saved
			cancel()
			status, message := "done", ""
			if itemErr != nil {
				status = "failed"
				message = itemErr.Error()
			}
			queue.UpdateItem(item.Index, status, message, time.Since(item.StartedAt))
			if checkpointed || ctx.Err() != nil || itemErr == errAgentEmergencyStop {
				for next := index + 1; next < total; next++ {
					queue.UpdateItem(next+1, "skipped", "", 0)
				}
				if itemErr != nil {
					terminalErr = itemErr
				} else {
					terminalErr = fmt.Errorf("sequential queue checkpoint saved; awaiting continuation")
				}
				break
			}
		}
		if terminalErr == nil {
			terminalResult = a.sequentialSummary()
		}
		if goalTurn != nil {
			if usageErr := goalTurn.RecordTurnEnd(ctx, 0); usageErr != nil {
				terminalErr = usageErr
			}
		}
		if terminalErr != nil {
			sendStreamEvent(ctx, out, StreamEvent{Type: EventError, Error: terminalErr})
			return
		}
		sendStreamEvent(ctx, out, StreamEvent{Type: EventDone, Result: terminalResult, Model: a.Model()})
	}()
	return out, nil
}
