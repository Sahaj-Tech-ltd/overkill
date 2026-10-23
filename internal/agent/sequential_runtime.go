package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/compaction/seahorse"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type sequentialItemKey struct{}
type sequentialSourceKey struct{}

func withSequentialPrompt(ctx context.Context, prompt sequentialItemPrompt) (context.Context, string) {
	return context.WithValue(ctx, sequentialSourceKey{}, prompt.Source), prompt.UserInput
}
func sequentialSource(ctx context.Context) string {
	source, _ := ctx.Value(sequentialSourceKey{}).(string)
	return source
}

func isSequentialItem(ctx context.Context) bool {
	active, _ := ctx.Value(sequentialItemKey{}).(bool)
	return active
}

var sequentialNumbered = regexp.MustCompile(`(?m)^\s*\d+[.)]\s+`)

func sequentialItems(input string) []WorkItem {
	d := NewDecomposer()
	d.maxItems = 11
	return d.Decompose(sequentialNumbered.ReplaceAllString(input, "- "))
}

// SequentialCheckpoint is scheduler data, never tool/provider authority. Full
// transcript and active model view are separate so compaction cannot erase the
// original actions and prior-item text cannot leak into another item's prompt.
type SequentialCheckpoint struct {
	Input            string              `json:"input"`
	Items            []WorkItem          `json:"items"`
	Current          int                 `json:"current"`
	Scope            string              `json:"scope"`
	Started          bool                `json:"started"`
	CompletedHistory []providers.Message `json:"completed_history"`
	RawItemHistory   []providers.Message `json:"raw_item_history"`
	ActiveHistory    []providers.Message `json:"active_history"`
}
type sequentialFrame struct {
	scope        string
	raw          []providers.Message
	cursor       int
	checkpointed bool
}
type sequentialExecution struct {
	input      string
	items      []WorkItem
	current    int
	durable    []providers.Message
	frame      *sequentialFrame
	wasEnabled bool
}

func (a *Agent) captureSequentialLocked() {
	execution := a.sequentialExecution
	if execution == nil || execution.frame == nil {
		return
	}
	frame := execution.frame
	if frame.cursor <= len(a.history) {
		frame.raw = append(frame.raw, a.history[frame.cursor:]...)
	}
	frame.cursor = len(a.history)
}
func (a *Agent) durableHistoryLocked() []providers.Message {
	execution := a.sequentialExecution
	if execution == nil {
		return append([]providers.Message(nil), a.history...)
	}
	out := append([]providers.Message(nil), execution.durable...)
	if execution.frame != nil {
		out = append(out, execution.frame.raw...)
		if execution.frame.cursor <= len(a.history) {
			out = append(out, a.history[execution.frame.cursor:]...)
		}
	}
	return out
}
func (a *Agent) sequentialCheckpointInput(input string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.sequentialExecution != nil {
		return a.sequentialExecution.input
	}
	return input
}
func (a *Agent) snapshotSequentialFlow(state *FlowState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	execution := a.sequentialExecution
	if execution == nil || execution.frame == nil {
		return
	}
	a.captureSequentialLocked()
	frame := execution.frame
	frame.checkpointed = true
	state.Sequential = &SequentialCheckpoint{Input: execution.input, Items: append([]WorkItem(nil), execution.items...), Current: execution.current, Scope: frame.scope, Started: len(a.history) > 0, CompletedHistory: append([]providers.Message(nil), execution.durable...), RawItemHistory: append([]providers.Message(nil), frame.raw...), ActiveHistory: append([]providers.Message(nil), a.history...)}
	state.History = a.durableHistoryLocked()
	state.UserInput = execution.input
}
func validateSequentialCheckpoint(checkpoint *SequentialCheckpoint) error {
	if checkpoint == nil {
		return nil
	}
	if len(checkpoint.Input) > 64*1024 || len(checkpoint.Items) < 2 || len(checkpoint.Items) > 10 || checkpoint.Current < 0 || checkpoint.Current >= len(checkpoint.Items) {
		return fmt.Errorf("sequential checkpoint bounds invalid")
	}
	if checkpoint.Started && len(checkpoint.ActiveHistory) == 0 || !checkpoint.Started && (len(checkpoint.ActiveHistory) > 0 || len(checkpoint.RawItemHistory) > 0) {
		return fmt.Errorf("sequential checkpoint start state invalid")
	}
	if _, err := seahorse.WithHistoryScope(context.Background(), checkpoint.Scope); err != nil {
		return err
	}
	for i, item := range checkpoint.Items {
		if item.Index != i+1 || len(item.Description) > 16*1024 {
			return fmt.Errorf("sequential checkpoint item invalid")
		}
		if i < checkpoint.Current && item.Status != WorkItemDone && item.Status != WorkItemFailed && item.Status != WorkItemSkipped {
			return fmt.Errorf("sequential checkpoint completed cursor invalid")
		}
		if i == checkpoint.Current && item.Status != WorkItemActive {
			return fmt.Errorf("sequential checkpoint active cursor invalid")
		}
		if i > checkpoint.Current && item.Status != WorkItemPending {
			return fmt.Errorf("sequential checkpoint future cursor invalid")
		}
	}
	if !flowHistoryComplete(checkpoint.ActiveHistory) || !flowHistoryComplete(checkpoint.RawItemHistory) || !flowHistoryComplete(checkpoint.CompletedHistory) {
		return fmt.Errorf("sequential checkpoint has unresolved tool execution")
	}
	raw, err := json.Marshal(checkpoint)
	if err != nil || len(raw) > 8*1024*1024 {
		return fmt.Errorf("sequential checkpoint exceeds bound")
	}
	return nil
}
func (a *Agent) beginSequential(input string, checkpoint *SequentialCheckpoint) (*QueueState, error) {
	if checkpoint != nil {
		if err := validateSequentialCheckpoint(checkpoint); err != nil {
			return nil, err
		}
	}
	items := sequentialItems(input)
	if checkpoint != nil {
		items = append([]WorkItem(nil), checkpoint.Items...)
	}
	if len(input) > 64*1024 || len(items) < 2 || len(items) > 10 {
		return nil, fmt.Errorf("sequential requests require 2-10 bounded work items")
	}
	for _, item := range items {
		if len(item.Description) > 16*1024 {
			return nil, fmt.Errorf("sequential item exceeds bound")
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sequentialExecution != nil {
		return nil, fmt.Errorf("sequential execution already active")
	}
	execution := &sequentialExecution{input: input, items: items, wasEnabled: a.seqEnabled, durable: append([]providers.Message(nil), a.history...)}
	if checkpoint == nil {
		execution.durable = append(execution.durable, providers.Message{Role: "user", Content: input})
	} else {
		execution.current = checkpoint.Current
		execution.durable = append([]providers.Message(nil), checkpoint.CompletedHistory...)
		if checkpoint.Started {
			execution.frame = &sequentialFrame{scope: checkpoint.Scope, raw: append([]providers.Message(nil), checkpoint.RawItemHistory...), cursor: len(checkpoint.ActiveHistory)}
			a.history = append([]providers.Message(nil), checkpoint.ActiveHistory...)
		} else {
			a.history = append([]providers.Message(nil), execution.durable...)
		}
	}
	a.sequentialExecution = execution
	a.seqEnabled = false
	if a.queueState == nil {
		a.queueState = &QueueState{}
	}
	queue := a.queueState
	queue.SetItems(items)
	for i := 0; i < execution.current; i++ {
		queue.UpdateItem(items[i].Index, items[i].Status.String(), items[i].Error, items[i].CompletedAt.Sub(items[i].StartedAt))
	}
	return queue, nil
}
func (a *Agent) beginSequentialItem(index int) (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	scope := hex.EncodeToString(nonce[:])
	a.mu.Lock()
	defer a.mu.Unlock()
	execution := a.sequentialExecution
	execution.current = index
	execution.items[index].Status = WorkItemActive
	execution.items[index].StartedAt = time.Now()
	execution.frame = &sequentialFrame{scope: scope}
	a.history = nil
	return scope, nil
}
func (a *Agent) finishSequentialItem(result *RunResult, itemErr error) (checkpointed bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	execution := a.sequentialExecution
	if execution == nil || execution.frame == nil {
		return false
	}
	a.captureSequentialLocked()
	checkpointed = execution.frame.checkpointed
	execution.durable = append(execution.durable, execution.frame.raw...)
	item := &execution.items[execution.current]
	item.CompletedAt = time.Now()
	if itemErr != nil {
		item.Status = WorkItemFailed
		item.Error = itemErr.Error()
	} else {
		item.Status = WorkItemDone
		if result != nil {
			item.Result = result.Response
		}
	}
	execution.frame = nil
	a.history = append([]providers.Message(nil), execution.durable...)
	return checkpointed
}
func (a *Agent) finishSequential() {
	a.mu.Lock()
	execution := a.sequentialExecution
	if execution != nil {
		if execution.frame != nil {
			a.captureSequentialLocked()
			execution.durable = append(execution.durable, execution.frame.raw...)
		}
		a.history = append([]providers.Message(nil), execution.durable...)
		a.seqEnabled = execution.wasEnabled
		a.sequentialExecution = nil
	}
	queue := a.queueState
	a.mu.Unlock()
	if queue != nil {
		queue.Finish()
	}
}
func (a *Agent) sequentialSummary() *RunResult {
	a.mu.RLock()
	defer a.mu.RUnlock()
	execution := a.sequentialExecution
	done, failed := 0, 0
	var lines []string
	for _, item := range execution.items {
		if item.Status == WorkItemDone {
			done++
			lines = append(lines, fmt.Sprintf("### Item %d: %s\n\n%s", item.Index, item.Description, item.Result))
		} else if item.Status == WorkItemFailed {
			failed++
			lines = append(lines, fmt.Sprintf("❌ Item %d: %s", item.Index, item.Error))
		}
	}
	return &RunResult{Response: fmt.Sprintf("🧠 **Think mode** — processed %d items: %d done, %d failed\n\n%s", len(execution.items), done, failed, strings.Join(lines, "\n\n")), Model: a.model, sequential: true}
}
func (a *Agent) sequentialItemContext(ctx context.Context, scope string) (context.Context, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	scoped, err := seahorse.WithHistoryScope(ctx, scope)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return context.WithValue(scoped, sequentialItemKey{}, true), cancel, nil
}

func (a *Agent) sequentialFileBudget() int {
	report := a.BudgetReport()
	if report == nil || report.MaxTokens <= 0 {
		return maxSequentialContextBytes
	}
	return max(0, min(maxSequentialContextBytes, (report.MaxTokens-report.TotalEstimate-256)*2))
}

func (a *Agent) sequentialCheckpointed() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sequentialExecution != nil && a.sequentialExecution.frame != nil && a.sequentialExecution.frame.checkpointed
}
