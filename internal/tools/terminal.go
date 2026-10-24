package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/pty"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/terminal"
)

// TerminalTool is bound to an application-owned attempt. Neither the owner nor
// human-control authority can be supplied by a model. Coordinated processes require
// a broker-owned lifetime workspace reservation. The execution guard refuses
// this adapter if its manager has no reservation provider.
type TerminalTool struct {
	manager *terminal.Manager
	owner   terminal.Owner
	scanner *security.CommandScanner
}

func NewTerminalTool(manager *terminal.Manager, owner terminal.Owner, workspace string) *TerminalTool {
	return &TerminalTool{manager: manager, owner: owner, scanner: security.NewCommandScanner(security.WithProjectPath(workspace))}
}
func (*TerminalTool) Name() string      { return "terminal" }
func (t *TerminalTool) Available() bool { return t.manager != nil && terminal.Supported() }
func (*TerminalTool) Description() string {
	return "Own a persistent trusted-host terminal across tool calls: start, list, read, input, resize, interrupt, close or forget. Command syntax is " + pty.ShellName() + ". Use the returned opaque id and byte cursor. Processes last until closed, owner completion/stop, session shutdown or the one-hour lifetime limit. Human takeover pauses agent input. Read fresh control_revision and input_sequence before input; retry identities never replay old keys. Backend restart interrupts processes; commands are never replayed. When offered, handoff explicitly transfers a child terminal to its configured parent; ownership/lifetime grants are not enlarged. The child then loses access and the parent must read fresh state. This is not an OS sandbox."
}
func (t *TerminalTool) Schema() json.RawMessage {
	schema := `{"type":"object","properties":{"operation":{"type":"string","enum":["start","list","read","input","resize","interrupt","close","forget"]},"id":{"type":"string"},"control_revision":{"type":"integer","minimum":1,"description":"Latest control_revision from start/list/read; changes on takeover/return/owner transfer"},"sequence":{"type":"integer","minimum":1,"description":"For input/interrupt: latest input_sequence + 1; retry the exact same revision/sequence/bytes after a lost response. Older sequences are rejected, never replayed."},"command":{"type":"string"},"cwd":{"type":"string"},"data":{"type":"string"},"cursor":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":65536},"wait_ms":{"type":"integer","minimum":0,"maximum":1000},"rows":{"type":"integer","minimum":1,"maximum":1000},"cols":{"type":"integer","minimum":1,"maximum":1000}},"required":["operation"],"additionalProperties":false}`
	if t.manager != nil && t.manager.HandoffAvailable(t.owner) {
		schema = strings.Replace(schema, `"close","forget"]`, `"close","forget","handoff"]`, 1)
	}
	return json.RawMessage(schema)
}
func (t *TerminalTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	if !t.Available() {
		return nil, fmt.Errorf("terminal: persistent terminal service is unavailable")
	}
	var in struct {
		Operation, ID, Command, CWD, Data string
		ControlRevision                   uint64 `json:"control_revision"`
		Sequence                          uint64 `json:"sequence"`
		Cursor                            uint64
		Limit                             int
		WaitMS                            int `json:"wait_ms"`
		Rows, Cols                        uint16
	}
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return nil, fmt.Errorf("terminal: input: %w", err)
	}
	if in.Rows > 1000 || in.Cols > 1000 || in.WaitMS < 0 || in.WaitMS > 1000 || in.Limit < 0 || in.Limit > 65536 {
		return nil, fmt.Errorf("terminal: invalid dimensions or output bounds")
	}
	scan := func(command string) error {
		result, err := t.scanner.Scan(command)
		if err != nil {
			return fmt.Errorf("terminal: command scan failed: %w", err)
		}
		if result != nil && result.Blocked {
			return fmt.Errorf("terminal: command blocked by scanner")
		}
		return nil
	}
	switch in.Operation {
	case "start":
		if err := scan(in.Command); err != nil {
			return nil, err
		}
		value, err := t.manager.Start(ctx, t.owner, in.Command, in.CWD, in.Rows, in.Cols)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	case "list":
		return json.Marshal(map[string]any{"terminals": t.manager.List(t.owner, false)})
	case "read":
		value, err := t.manager.Read(ctx, t.owner, in.ID, false, in.Cursor, in.Limit, time.Duration(in.WaitMS)*time.Millisecond)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	case "input":
		if err := scan(in.Data); err != nil {
			return nil, err
		}
		receipt, err := t.manager.WriteInput(ctx, t.owner, in.ID, false, in.ControlRevision, in.Sequence, []byte(in.Data))
		if err != nil {
			return nil, err
		}
		return json.Marshal(receipt)
	case "resize":
		if err := t.manager.ResizeAt(t.owner, in.ID, false, in.ControlRevision, in.Rows, in.Cols); err != nil {
			return nil, err
		}
	case "interrupt":
		receipt, err := t.manager.WriteInput(ctx, t.owner, in.ID, false, in.ControlRevision, in.Sequence, []byte{3})
		if err != nil {
			return nil, err
		}
		return json.Marshal(receipt)
	case "close":
		if err := t.manager.CloseAt(t.owner, in.ID, false, in.ControlRevision); err != nil {
			return nil, err
		}
	case "handoff":
		value, err := t.manager.HandoffToParent(ctx, t.owner, in.ID, false, in.ControlRevision)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	case "forget":
		if err := t.manager.Forget(t.owner, in.ID, false); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("terminal: unknown operation %q", in.Operation)
	}
	return json.RawMessage(`{"ok":true}`), nil
}
