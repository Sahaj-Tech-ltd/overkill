package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
)

type coordinationAbortKey struct{}

// WithCoordinationAbort lets an application-owned worker stop further model and
// tool iterations after its agreement gate rejects an effect. It never grants
// identity or authority, and ordinary tool calls have no abort callback.
func WithCoordinationAbort(ctx context.Context, abort context.CancelCauseFunc) context.Context {
	return context.WithValue(ctx, coordinationAbortKey{}, abort)
}

// NewCoordinationExecutionGuard binds application identity outside model input.
// The database lock covers admission through synchronous execution, so another
// peer cannot claim/reassign a path between checking it and writing the file.
// This is cooperative tool admission, not an OS or remote-process sandbox.
func NewCoordinationExecutionGuard(store *coordination.Store, peerID, workspace string) ExecutionGuard {
	return func(ctx context.Context, tool Tool, input json.RawMessage) (output json.RawMessage, resultErr error) {
		defer func() {
			if errors.Is(resultErr, coordination.ErrRequestContracts) {
				if abort, ok := ctx.Value(coordinationAbortKey{}).(context.CancelCauseFunc); ok && abort != nil {
					abort(resultErr)
				}
			}
		}()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if store == nil || peerID == "" {
			return nil, fmt.Errorf("coordination: authenticated store and peer are required")
		}
		var paths []string
		switch typed := tool.(type) {
		case *DelegateTool:
			if typed.manager == nil || !typed.manager.GuardsWorkerTools() {
				return nil, fmt.Errorf("coordination: delegation requires a factory with inherited child-tool admission")
			}
			var in delegateInput
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}
			if in.Contract != nil {
				return nil, fmt.Errorf("coordination: autonomous contract driver is not admitted for this session")
			}
			if err := store.CheckExecutionAdmission(ctx, peerID); err != nil {
				return nil, err
			}
			return tool.Execute(ctx, input)
		case *WorkerMessageTool:
			return tool.Execute(ctx, input)
		case *TerminalTool:
			if !typed.manager.ReservationsEnabled() {
				return nil, fmt.Errorf("coordination: terminal requires lifetime workspace reservations")
			}
			return tool.Execute(ctx, input)
		case *acpTaskTool:
			name := tool.Name()
			if name == "acp_status" || name == "acp_await" || name == "acp_sessions" {
				return tool.Execute(ctx, input)
			}
		case *CanvasTool:
			action, _, err := canvasInput(input)
			if err != nil {
				return nil, err
			}
			if action == "state" {
				return tool.Execute(ctx, input)
			}
		case *AskUserTool:
			// Human clarification has no workspace effects; never hold a project lock
			// while waiting for an answer.
			return tool.Execute(ctx, input)
		case *coordinationTool, *contractTool:
			// Store operations authenticate independently and acquire their own
			// transaction locks. Wrapping them would recursively deadlock.
			return tool.Execute(ctx, input)
		case *GrepTool, *LSPHoverTool, *LSPDefinitionTool, *LSPReferencesTool, *LSPSymbolsTool:
			return tool.Execute(ctx, input)
		case *FSTool:
			var in FSInput
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}
			switch in.Action {
			case "read", "glob", "stat":
				return tool.Execute(ctx, input)
			case "write", "edit", "mkdir":
				paths = []string{in.Path}
			default:
				return nil, fmt.Errorf("coordination: unsupported filesystem action %q", in.Action)
			}
		case *PatchTool:
			var in PatchInput
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}
			paths = []string{in.Path}
		}
		for i, path := range paths {
			normalized, err := coordinationWritePath(workspace, path)
			if err != nil {
				return nil, fmt.Errorf("coordination: write path: %w", err)
			}
			paths[i] = normalized
		}
		invoke := func(admitted context.Context) error {
			var err error
			output, err = tool.Execute(admitted, input)
			return err
		}
		var err error
		if len(paths) > 0 {
			err = store.WithOwnedPaths(ctx, peerID, paths, invoke)
		} else {
			// Arbitrary commands/adapters cannot reliably declare every path
			// they may mutate. Require exclusive project ownership instead of
			// guessing effects from command text or a model's description.
			err = store.WithUnrestrictedWorkspace(ctx, peerID, invoke)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", tool.Name(), err)
		}
		return output, nil
	}
}

func coordinationWritePath(workspace, path string) (string, error) {
	root, relative, err := openWorkspace(workspace, path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	// os.Root prevents escapes, but permits contained symlinks. Ownership
	// cannot accept such aliases because the target may belong to a peer.
	current := ""
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("owned writes require a path without symbolic links: %s", relative)
		}
	}
	return filepath.ToSlash(relative), nil
}
