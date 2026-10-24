package childruntime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

type unknownInputFixture struct{}

func (unknownInputFixture) Name() string { return "terminal" }
func (unknownInputFixture) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"success":false,"outcome":"unknown","written":0,"error":"Delivery is uncertain; inspect effects"}`), nil
}

func TestWorkerCannotClaimCompletionAfterUnknownTerminalInput(t *testing.T) {
	p := &scriptedProvider{complete: func(_ context.Context, _ providers.Request, step int) (providers.Response, error) {
		if step == 1 {
			return call("terminal", map[string]any{"operation": "input"}), nil
		}
		return providers.Response{Content: "Everything completed successfully."}, nil
	}}
	f := testFactory(canonicalDir(t), p)
	f.CodingTools = func() []tools.Tool { return []tools.Tool{unknownInputFixture{}} }
	w, err := f.Build(subagent.RealWorkerConfig{Goal: "Finish the task", Tools: []string{"terminal"}, MaxSteps: 2})
	require.NoError(t, err)
	result, err := w.Run(t.Context())
	require.ErrorContains(t, err, "reconcile effects")
	require.NotEqual(t, "completed", result.Status)
	require.False(t, result.ToolTrace[0].Success)
}
