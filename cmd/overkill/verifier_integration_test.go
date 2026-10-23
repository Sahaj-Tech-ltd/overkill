package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/stretchr/testify/require"
)

type verificationScript struct {
	path, broken, fixed string
	repair              bool
	readOnly            bool
	requests            []providers.Request
}

func (p *verificationScript) Name() string              { return "verification-fixture" }
func (p *verificationScript) Models() []providers.Model { return nil }
func (p *verificationScript) next(request providers.Request) providers.Response {
	step := len(p.requests)
	p.requests = append(p.requests, request)
	if step == 0 {
		action := "write"
		if p.readOnly {
			action = "read"
		}
		input, _ := json.Marshal(map[string]string{"action": action, "path": p.path, "content": p.broken})
		return providers.Response{ToolCalls: []providers.ToolCall{{ID: "write-broken", Name: "fs", Arguments: string(input)}}}
	}
	if step == 1 && p.repair {
		input, _ := json.Marshal(map[string]string{"action": "edit", "path": p.path, "old": p.broken, "new": p.fixed})
		return providers.Response{ToolCalls: []providers.ToolCall{{ID: "repair-file", Name: "fs", Arguments: string(input)}}}
	}
	return providers.Response{Content: "Everything is fixed!"}
}
func (p *verificationScript) Complete(_ context.Context, request providers.Request) (providers.Response, error) {
	return p.next(request), nil
}
func (p *verificationScript) Stream(_ context.Context, request providers.Request) (<-chan providers.Chunk, error) {
	response := p.next(request)
	output := make(chan providers.Chunk, 2)
	output <- providers.Chunk{Content: response.Content, ToolCalls: response.ToolCalls}
	output <- providers.Chunk{Done: true}
	close(output)
	return output, nil
}

func TestPostWriteVerificationRealRunAndStream(t *testing.T) {
	for _, format := range []string{"json", "go"} {
		for _, stream := range []bool{false, true} {
			for _, repair := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%t/repair=%t", format, stream, repair), func(t *testing.T) {
					t.Setenv("OVERKILL_NO_VERIFY", "")
					workspace := t.TempDir()
					script := &verificationScript{path: "broken." + format, broken: "{", fixed: "{}", repair: repair}
					if format == "go" {
						require.NoError(t, os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module verification-fixture\n\ngo 1.26\n"), 0600))
						script.broken = "package fixture\nfunc Broken( {\n"
						script.fixed = "package fixture\nfunc Fixed() {}\n"
					}
					services, err := newRuntimeServices(config.Default(), nil, t.TempDir())
					require.NoError(t, err)
					metadata := session.NewSession(workspace)
					runtime, err := services.BuildSession(context.Background(), metadata)
					require.NoError(t, err)
					defer runtime.Cleanup()
					require.NotNil(t, runtime.PostWriteVerifier, "shared assembly must enable verification by default")
					a := agent.New(agent.Config{Provider: script, Tools: runtime.Tools, SessionID: metadata.ID, Model: "fixture", MaxSteps: 5})
					defer a.Shutdown()
					a.SetPostWriteVerifier(runtime.PostWriteVerifier)
					var result *agent.RunResult
					if stream {
						events, err := a.Stream(context.Background(), "Write the requested file")
						require.NoError(t, err)
						for event := range events {
							require.NoError(t, event.Error)
							if event.Type == agent.EventDone {
								result = event.Result
							}
						}
					} else {
						result, err = a.Run(context.Background(), "Write the requested file")
						require.NoError(t, err)
					}
					require.NotNil(t, result)
					require.GreaterOrEqual(t, len(script.requests), 2)
					feedback := false
					for _, message := range script.requests[1].Messages {
						if message.Role == "system" && strings.Contains(message.Content, "[verify]") && strings.Contains(message.Content, script.path) {
							feedback = true
						}
					}
					require.True(t, feedback, "the next provider step must receive the real failed-file check")
					if repair {
						require.False(t, result.Blocked, result.BlockReason)
						data, err := os.ReadFile(filepath.Join(workspace, script.path))
						require.NoError(t, err)
						require.Equal(t, script.fixed, string(data))
					} else {
						require.True(t, result.Blocked)
						require.Contains(t, result.BlockReason, "[verify]")
						require.NotContains(t, result.Response, "Everything is fixed!")
					}
					// Every tool result must refer to a provider-issued tool call. Verification
					// feedback must not fabricate a tool_use_id that provider APIs will reject.
					known := map[string]bool{}
					for _, message := range a.History() {
						for _, call := range message.ToolCalls {
							known[call.ID] = true
						}
						if message.Role == "tool" {
							require.True(t, known[message.ToolCallID], "unknown tool result: %s", message.ToolCallID)
						}
					}
				})
			}
		}
	}
}

func TestVerificationDoesNotTreatFSReadAsWrite(t *testing.T) {
	t.Setenv("OVERKILL_NO_VERIFY", "")
	workspace := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "broken.json"), []byte("{"), 0600))
	services, err := newRuntimeServices(config.Default(), nil, t.TempDir())
	require.NoError(t, err)
	runtime, err := services.BuildSession(context.Background(), session.NewSession(workspace))
	require.NoError(t, err)
	defer runtime.Cleanup()
	provider := &verificationScript{path: "broken.json", readOnly: true}
	a := agent.New(agent.Config{Provider: provider, Tools: runtime.Tools, Model: "fixture"})
	defer a.Shutdown()
	a.SetPostWriteVerifier(runtime.PostWriteVerifier)
	result, err := a.Run(context.Background(), "Read the requested file")
	require.NoError(t, err)
	require.False(t, result.Blocked)
	for _, message := range a.History() {
		require.NotContains(t, message.Content, "[verify]")
	}
}

func TestVerificationExplicitDisableAndLifecycle(t *testing.T) {
	workspace := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "file.json"), []byte("{}"), 0600))
	t.Setenv("OVERKILL_NO_VERIFY", "1")
	verifier, err := newVerifierAdapter(context.Background(), workspace)
	require.NoError(t, err)
	require.Nil(t, verifier)
	t.Setenv("OVERKILL_NO_VERIFY", "0")
	lifetime, cancel := context.WithCancel(context.Background())
	verifier, err = newVerifierAdapter(lifetime, workspace)
	require.NoError(t, err)
	require.NotNil(t, verifier)
	cancel()
	note := verifier.VerifyToolCall(context.Background(), "fs", json.RawMessage(`{"action":"write","path":"file.json"}`))
	require.Contains(t, note, "[verify]", "a closed session cannot claim a successful verification")
}
