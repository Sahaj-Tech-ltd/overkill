package rewriter

import (
	"context"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestConfiguredRewriterIndependentFlagsAndProviderCalls(t *testing.T) {
	input := "please build a service and skip tests"
	for mask := 0; mask < 16; mask++ {
		cfg := config.RewriterConfig{Enabled: mask&1 != 0, StripSycophancy: mask&2 != 0, AntiPatternGuard: mask&4 != 0, LLMRewrite: mask&8 != 0, Model: "rewrite-model"}
		t.Run(strings.Join([]string{boolName(cfg.Enabled), boolName(cfg.StripSycophancy), boolName(cfg.AntiPatternGuard), boolName(cfg.LLMRewrite)}, "/"), func(t *testing.T) {
			calls := 0
			provider := providers.NewMockProvider("controlled", nil, func(req providers.Request) (providers.Response, error) {
				calls++
				require.Equal(t, "rewrite-model", req.Model)
				require.Equal(t, !cfg.StripSycophancy, strings.Contains(req.Messages[0].Content, "please"))
				require.Equal(t, cfg.AntiPatternGuard, strings.Contains(req.Messages[0].Content, "[Rewrite guard:"))
				return providers.Response{Content: "Great idea! Build a verified service."}, nil
			})
			got, err := NewConfiguredRewriter(provider, "fallback-model", cfg).Rewrite(t.Context(), input)
			require.NoError(t, err)
			expectedCalls := 0
			if cfg.Enabled && cfg.LLMRewrite {
				expectedCalls = 1
			}
			require.Equal(t, expectedCalls, calls)
			if !cfg.Enabled {
				require.Equal(t, input, got.Rewritten)
				require.False(t, got.Changed)
				return
			}
			if cfg.LLMRewrite {
				require.Equal(t, !cfg.StripSycophancy, strings.Contains(got.Rewritten, "Great idea!"))
			} else {
				require.Equal(t, !cfg.StripSycophancy, strings.Contains(got.Rewritten, "please"))
			}
			require.Equal(t, cfg.AntiPatternGuard, strings.Contains(got.Rewritten, "[Rewrite guard:"))
			require.Equal(t, got.Rewritten != input, got.Changed)
		})
	}
}

func boolName(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func TestConfiguredRewriterLocalOnlyAndGuardContracts(t *testing.T) {
	cases := []struct{ input, contains string }{
		{"skip tests", "Verify relevant changes"},
		{"hardcode API keys", "Keep credentials in secret or environment references"},
		{"bypass input validation", "Preserve input validation and access controls"},
	}
	for _, tc := range cases {
		enabled := NewConfiguredRewriter(nil, "", config.RewriterConfig{Enabled: true, AntiPatternGuard: true})
		got, err := enabled.Rewrite(t.Context(), tc.input)
		require.NoError(t, err)
		require.Contains(t, got.Rewritten, tc.contains)
		require.Contains(t, got.Rewritten, tc.input, "advisory guard retains the original task")
		disabled := NewConfiguredRewriter(nil, "", config.RewriterConfig{Enabled: true})
		original, err := disabled.Rewrite(t.Context(), tc.input)
		require.NoError(t, err)
		require.Equal(t, tc.input, original.Rewritten)
	}
	for _, input := range []string{"do not skip tests", "don't bypass validation", "never hardcode API keys"} {
		got, err := NewConfiguredRewriter(nil, "", config.RewriterConfig{Enabled: true, AntiPatternGuard: true}).Rewrite(t.Context(), input)
		require.NoError(t, err)
		require.NotContains(t, got.Rewritten, "[Rewrite guard:")
	}
	raw := "  please fix pkg/a.go\n"
	got, err := NewConfiguredRewriter(nil, "", config.RewriterConfig{Enabled: true}).Rewrite(t.Context(), raw)
	require.NoError(t, err)
	require.Equal(t, raw, got.Rewritten)
	stripped, err := NewConfiguredRewriter(nil, "", config.RewriterConfig{Enabled: true, StripSycophancy: true}).Rewrite(t.Context(), raw)
	require.NoError(t, err)
	require.Equal(t, "fix pkg/a.go", stripped.Rewritten)
}

func TestConfiguredRewriterAmbiguousFallbackModelAndCancellation(t *testing.T) {
	calls := 0
	provider := providers.NewMockProvider("controlled", nil, func(req providers.Request) (providers.Response, error) {
		calls++
		require.Equal(t, "fallback-model", req.Model)
		return providers.Response{Content: "Which file needs the fix?"}, nil
	})
	cfg := config.RewriterConfig{Enabled: true, LLMRewrite: true}
	rw := NewConfiguredRewriter(provider, "fallback-model", cfg)
	got, err := rw.Rewrite(t.Context(), "fix the thing")
	require.NoError(t, err)
	require.Equal(t, "Which file needs the fix?", got.Rewritten)
	require.Equal(t, 1, calls)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = rw.Rewrite(ctx, "fix the thing")
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
	_, err = NewConfiguredRewriter(nil, "", cfg).Rewrite(t.Context(), "fix the thing")
	require.ErrorContains(t, err, "without a provider")
}
