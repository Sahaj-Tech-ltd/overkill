package learning

import (
	"testing"
)

func TestIsCorrection(t *testing.T) {
	tests := []struct {
		msg      string
		expected bool
	}{
		{"no, that's not what I wanted", true},
		{"no that is wrong", true},
		{"wrong, do it differently", true},
		{"actually, I wanted the file renamed not deleted", true},
		{"instead, please use the other approach", true},
		{"correct: you should use badger not boltdb", true},
		{"correction: the path is ~/.overkill not ~/.config/overkill", true},
		{"that's wrong, the API endpoint is /v2 not /v1", true},
		{"that is wrong", true},
		{"don't do that", true},
		{"dont do that", true},
		{"please don't", true},
		{"i meant to say use the production config", true},
		{"i said blue not red", true},
		{"you misunderstood the requirement", true},
		{"you misinterpreted what I asked for", true},
		{"that isn't right", true},
		{"that is not correct", true},
		{"that is not what i meant", true},
		{"try again", true},
		{"do it differently", true},
		{"not like that", true},
		{"it's not what i meant", true},

		// Negative cases
		{"hello world", false},
		{"can you help me with something", false},
		{"what is the capital of France", false},
		{"how do I install badger", false},
		{"", false},
		{"   ", false},
		{"I think that's wrong but let me check", false}, // not at start
		{"look at this actually very interesting article", false}, // mid-sentence
	}

	for _, tt := range tests {
		t.Run(tt.msg[:min(len(tt.msg), 40)], func(t *testing.T) {
			got := IsCorrection(tt.msg)
			if got != tt.expected {
				t.Errorf("IsCorrection(%q) = %v, want %v", tt.msg, got, tt.expected)
			}
		})
	}
}

func TestExtractCorrect(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"no, use badger instead", "use badger instead"},
		{"wrong, the port is 8080", "the port is 8080"},
		{"actually, I want it in /tmp", "I want it in /tmp"},
		{"instead, rename the file", "rename the file"},
		{"correct: the endpoint is /api/v2", "the endpoint is /api/v2"},
		{"correction: use PostgreSQL not MySQL", "use PostgreSQL not MySQL"},
		{"that's wrong, do it this way", "do it this way"},
		{"don't do that, the config is in ~/.overkill", "the config is in ~/.overkill"},
		{"please don't, use the safe approach", "use the safe approach"},
		{"i meant to use the new API", "to use the new API"},
		{"i wanted the blue one", "the blue one"},
		{"i said rename not delete", "rename not delete"},
		{"no. This is a new sentence.", "This is a new sentence."},
	}

	for _, tt := range tests {
		t.Run(tt.input[:min(len(tt.input), 40)], func(t *testing.T) {
			got := ExtractCorrect(tt.input)
			if got != tt.expected {
				t.Errorf("ExtractCorrect(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestTokenize(t *testing.T) {
	tokens := tokenize("Hello, World! This is a test.")
	if len(tokens) == 0 {
		t.Fatal("expected tokens")
	}
	// Check that tokens are lowercase and punctuation-free
	for _, tok := range tokens {
		for _, r := range tok {
			if r < 'a' || r > 'z' {
				t.Errorf("token %q contains non-lowercase letter: %c", tok, r)
			}
		}
	}
	// Check deduplication
	tokens2 := tokenize("hello hello world world")
	count := 0
	for _, tok := range tokens2 {
		if tok == "hello" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 'hello' to appear once in deduplicated tokens, got %d times", count)
	}
}

func TestSimilarity(t *testing.T) {
	// Identical sets
	q := tokenize("install badger database")
	d := tokenize("install badger database")
	score := similarity(q, d)
	if score < 0.9 {
		t.Errorf("expected high similarity for identical sets, got %f", score)
	}

	// Completely different sets
	q = tokenize("install badger database")
	d = tokenize("eat pizza pasta")
	score = similarity(q, d)
	if score > 0.01 {
		t.Errorf("expected near-zero similarity for different sets, got %f", score)
	}

	// Partially overlapping
	q = tokenize("install badger database")
	d = tokenize("badger installation guide")
	score = similarity(q, d)
	if score < 0.1 {
		t.Errorf("expected moderate similarity for overlapping sets, got %f", score)
	}
}

func TestCorrectionKey(t *testing.T) {
	c1 := NewCorrection("context A", "wrong A", "correct A")
	c2 := NewCorrection("context A", "wrong A", "correct B") // same context+wrong
	c3 := NewCorrection("context B", "wrong A", "correct C") // different context

	if c1.Key() != c2.Key() {
		t.Errorf("same context+wrong should produce same key")
	}
	if c1.Key() == c3.Key() {
		t.Errorf("different context should produce different key")
	}
}

func TestCorrectionMarshal(t *testing.T) {
	c := NewCorrection("test context", "wrong response", "correct response")
	data, err := c.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	c2, err := UnmarshalCorrection(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c2.Context != c.Context || c2.Wrong != c.Wrong || c2.Correct != c.Correct {
		t.Errorf("roundtrip mismatch: %+v vs %+v", c, c2)
	}
}

func TestFormatPrompt(t *testing.T) {
	corrections := []*Correction{
		{Context: "install a database", Wrong: "use boltdb", Correct: "use badger instead"},
		{Context: "create config file", Wrong: "put it in /etc", Correct: "put it in ~/.overkill"},
	}
	prompt := FormatPrompt(corrections)
	if prompt == "" {
		t.Error("expected non-empty prompt")
	}
	if !contains(prompt, "install a database") {
		t.Error("prompt should contain context")
	}
	if !contains(prompt, "badger") {
		t.Error("prompt should contain correction")
	}
}

func TestMatchScore(t *testing.T) {
	c := &Correction{
		Context: "how do I install badger on Linux",
		Wrong:   "you should use apt-get install badger",
		Correct: "use go get github.com/dgraph-io/badger",
	}

	// Query similar to context
	queryTokens := tokenize("install badger on linux")
	score := c.MatchScore(queryTokens)
	if score < 0.3 {
		t.Errorf("expected high match score for context-query, got %f", score)
	}

	// Query completely different
	queryTokens = tokenize("make a pizza with mushrooms")
	score = c.MatchScore(queryTokens)
	if score > 0.01 {
		t.Errorf("expected near-zero match for unrelated query, got %f", score)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
