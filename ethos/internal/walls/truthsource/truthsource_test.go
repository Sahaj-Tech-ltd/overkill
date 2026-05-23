package truthsource

import "testing"

func TestCheck(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantIssue bool
		wantHigh  bool
	}{
		{
			name:      "flagged: i think you might mean",
			input:     "I think you might mean the iPhone 16 — Apple released that one in 2024.",
			wantIssue: true,
			wantHigh:  true,
		},
		{
			name:      "flagged: dont have info but redirect",
			input:     "I don't have information about that product, but the Acme SDK v3 might be what you're looking for.",
			wantIssue: true,
			wantHigh:  false,
		},
		{
			name:      "flagged: knowledge cutoff",
			input:     "As of my knowledge cutoff, that framework doesn't exist.",
			wantIssue: true,
			wantHigh:  true,
		},
		{
			name:      "not flagged: search found result",
			input:     "I searched and found the iPhone 17 announcement — does that match what you're looking for?",
			wantIssue: false,
			wantHigh:  false,
		},
		{
			name:      "not flagged: normal helpful response",
			input:     "Sure, here is how you'd configure that in Go: use a context with timeout.",
			wantIssue: false,
			wantHigh:  false,
		},
		{
			name:      "not flagged: not aware in non-correction context",
			input:     "I'm not aware of any issues with your approach — it looks correct to me.",
			wantIssue: false,
			wantHigh:  false,
		},
		{
			name:      "flagged: you may be referring to",
			input:     "You may be referring to GPT-4 instead of the model you mentioned.",
			wantIssue: true,
			wantHigh:  true,
		},
		{
			name:      "not flagged: empty string",
			input:     "",
			wantIssue: false,
			wantHigh:  false,
		},
		{
			name:      "flagged: perhaps you meant",
			input:     "Perhaps you meant the 2023 MacBook Pro rather than the model you described.",
			wantIssue: true,
			wantHigh:  true,
		},
		{
			name:      "not flagged: whitespace only",
			input:     "   \t\n  ",
			wantIssue: false,
			wantHigh:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := Check(tc.input)
			if result.HasIssue != tc.wantIssue {
				t.Errorf("HasIssue = %v, want %v (findings: %+v)", result.HasIssue, tc.wantIssue, result.Findings)
			}
			got := HasHighSeverity(result)
			if got != tc.wantHigh {
				t.Errorf("HasHighSeverity = %v, want %v (findings: %+v)", got, tc.wantHigh, result.Findings)
			}
		})
	}
}

func TestCheck_EmptyFindings(t *testing.T) {
	r := Check("")
	if len(r.Findings) != 0 {
		t.Errorf("expected no findings for empty input, got %d", len(r.Findings))
	}
}

func TestHasHighSeverity_NoFindings(t *testing.T) {
	if HasHighSeverity(Result{}) {
		t.Error("empty result should not have high severity")
	}
}
