package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestRunAndStreamReflectionUsesEnabledTools(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, tts := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/tts=%t", streaming, tts), func(t *testing.T) {
				registry := tools.NewRegistry()
				if tts {
					require.NoError(t, registry.Register(&mockTool{name: "tts.speak"}))
				}
				provider := &externalOutputCaptureProvider{mockProvider: &mockProvider{responses: []providers.Response{{Content: "Research response."}}}}
				a := New(Config{Provider: provider, Tools: registry, Model: "fixture", SystemPrompt: "Stable system prompt.", MaxTokens: 4096, MaxSteps: 3, SessionID: "capability-reflection"})
				input := "Dense research paper about transformer attention mechanisms and their implications for long-context LLMs."
				if streaming {
					events, err := a.Stream(t.Context(), input)
					require.NoError(t, err)
					for event := range events {
						require.NotEqual(t, EventError, event.Type, "%v", event.Error)
					}
				} else {
					_, err := a.Run(t.Context(), input)
					require.NoError(t, err)
				}
				require.NotEmpty(t, provider.captured)
				prompts := []string{provider.captured[0].SystemPrompt}
				for _, message := range provider.captured[0].Messages {
					if message.Role == "system" {
						prompts = append(prompts, message.Content)
					}
				}
				require.Equal(t, tts, strings.Contains(strings.Join(prompts, "\n"), "MODALITY: offer to read this aloud via TTS"))
				require.Equal(t, "Stable system prompt.", a.systemPrompt, "request-only hint must not mutate persistent prompt")
				reflection := a.reflectBeforeAction(input)
				for _, tool := range reflection.AvailableTools {
					require.True(t, registry.Has(tool.Name), "invented capability %s", tool.Name)
				}
			})
		}
	}
}
