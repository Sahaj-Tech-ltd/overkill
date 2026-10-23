package providers

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestResolveTokenLimits(t *testing.T) {
	models := []Model{{ID: "known", ContextWindow: 128000, MaxTokens: 200000, DefaultMaxTokens: 8192}, {ID: "legacy", MaxTokens: 64000}, {ID: "copied", ContextWindow: 16000, DefaultMaxTokens: 16000}}
	for _, tc := range []struct {
		name, id                  string
		override, context, output int
		invalid                   bool
	}{
		{"catalog", "known", 0, 128000, 8192, false},
		{"explicit output", "known", 2048, 128000, 2048, false},
		{"legacy context only", "legacy", 0, 64000, 0, false},
		{"legacy copied default ignored", "copied", 0, 16000, 0, false},
		{"unknown provider default", "custom", 0, 32768, 0, false},
		{"unknown explicit output", "custom", 1024, 32768, 1024, false},
		{"negative override", "known", -1, 0, 0, true},
		{"above output cap", "known", 8193, 0, 0, true},
		{"equal context", "legacy", 64000, 0, 0, true},
		{"unknown oversized override", "custom", 200000, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, o, err := ResolveTokenLimits(models, tc.id, tc.override)
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.context, c)
			require.Equal(t, tc.output, o)
		})
	}
	for _, m := range []Model{{ID: "bad", ContextWindow: -1}, {ID: "bad", MaxTokens: -1}, {ID: "bad", DefaultMaxTokens: -1}, {ID: "bad", MaxOutputTokens: -1}, {ID: "bad", ContextWindow: 8000, MaxOutputTokens: 8000}} {
		_, _, err := ResolveTokenLimits([]Model{m}, "bad", 0)
		require.Error(t, err)
	}
}
