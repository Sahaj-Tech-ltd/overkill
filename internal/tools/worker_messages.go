package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
)

// WorkerMessageTool binds the sender in its constructor. Messages cannot
// enlarge task grants, forge human answers, launch work or cancel a sibling.
type WorkerMessageTool struct {
	store  *subagent.CommunicationStore
	worker string
}

func NewWorkerMessageTool(store *subagent.CommunicationStore, worker string) *WorkerMessageTool {
	return &WorkerMessageTool{store: store, worker: worker}
}
func (*WorkerMessageTool) Name() string { return "worker_messages" }
func (*WorkerMessageTool) Description() string {
	return "List named sibling workers, exchange scoped updates/questions/correlated answers, inspect correspondence or acknowledge admitted input. Sender is application-bound. Accepted means durably queued; admitted means added to context, not understood or task completed. Messages do not create work or change permissions. Completed workers need a new explicit delegate_task followup_of attempt."
}
func (*WorkerMessageTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["list","inbox","send","acknowledge"]},"to":{"type":"string"},"kind":{"type":"string","enum":["update","question","answer","steer"]},"content":{"type":"string","maxLength":16000},"idempotency_key":{"type":"string","maxLength":128},"in_reply_to":{"type":"string"},"message_id":{"type":"string"},"recent":{"type":"boolean","description":"Latest 100-message snapshot with current delivery states; otherwise use after cursor"},"after":{"type":"integer","minimum":0},"wait_ms":{"type":"integer","minimum":0,"maximum":1000}},"required":["action"],"additionalProperties":false}`)
}
func (t *WorkerMessageTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	if t.store == nil || t.worker == "" {
		return nil, fmt.Errorf("worker: communication unavailable")
	}
	var in struct {
		Action    string `json:"action"`
		To        string `json:"to"`
		Kind      string `json:"kind"`
		Content   string `json:"content"`
		Key       string `json:"idempotency_key"`
		Reply     string `json:"in_reply_to"`
		MessageID string `json:"message_id"`
		After     int64  `json:"after"`
		WaitMS    int    `json:"wait_ms"`
		Recent    bool   `json:"recent"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return nil, err
	}
	if in.WaitMS < 0 || in.WaitMS > 1000 {
		return nil, fmt.Errorf("worker: wait_ms must be 0..1000")
	}
	switch in.Action {
	case "list":
		workers, err := t.store.Workers(ctx, t.worker == subagent.ParentWorker)
		if err != nil {
			return nil, err
		}
		if t.worker != subagent.ParentWorker {
			for i := range workers {
				workers[i].Result = nil
			}
		}
		result := map[string]any{"self": t.worker, "workers": workers, "history_limit": 100}
		if t.worker == subagent.ParentWorker {
			handoffs, err := t.store.TerminalHandoffs(ctx)
			if err != nil {
				return nil, err
			}
			result["terminal_handoffs"] = handoffs
		}
		return json.Marshal(result)
	case "send":
		message, err := t.store.Send(ctx, t.worker, in.To, in.Kind, in.Content, in.Key, in.Reply)
		if err != nil {
			return nil, err
		}
		return json.Marshal(message)
	case "acknowledge":
		if err := t.store.Acknowledge(ctx, t.worker, in.MessageID); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]bool{"acknowledged": true})
	case "inbox":
		if in.Recent {
			if in.After != 0 || in.WaitMS != 0 {
				return nil, fmt.Errorf("worker: recent snapshot cannot combine with cursor or wait")
			}
			messages, err := t.store.RecentInbox(ctx, t.worker)
			if err != nil {
				return nil, err
			}
			next := int64(0)
			if len(messages) > 0 {
				next = messages[len(messages)-1].Sequence
			}
			return json.Marshal(map[string]any{"messages": messages, "next_cursor": next, "snapshot": "latest100", "earlier_may_be_omitted": len(messages) == 100, "has_more": false})
		}
		deadline := time.NewTimer(time.Duration(in.WaitMS) * time.Millisecond)
		defer deadline.Stop()
		for {
			messages, err := t.store.Inbox(ctx, t.worker, in.After)
			if err != nil {
				return nil, err
			}
			if len(messages) > 0 || in.WaitMS == 0 {
				next := in.After
				for _, m := range messages {
					next = m.Sequence
				}
				return json.Marshal(map[string]any{"messages": messages, "next_cursor": next, "has_more": len(messages) == 100})
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-deadline.C:
				return json.Marshal(map[string]any{"messages": messages, "next_cursor": in.After, "has_more": false})
			case <-time.After(40 * time.Millisecond):
			}
		}
	default:
		return nil, fmt.Errorf("worker: unsupported communication action")
	}
}
