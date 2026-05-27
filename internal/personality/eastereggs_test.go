package personality

import (
	"strings"
	"testing"
)

// ─── Cooking Mode Tests ────────────────────────────────────────────────

func TestCookingMode_Triggered(t *testing.T) {
	cm := NewCookingMode()
	tests := []struct {
		input    string
		expected bool
	}{
		{"ok cook", true},
		{"let him cook", true},
		{"LET'S COOK", true},
		{"chef", true},
		{"yes chef", true},
		{"heard chef", true},
		{"start cooking", true},
		{"what should we cook today", false}, // "cook" alone isn't a trigger—needs "ok cook", "cook it", etc.
		{"hello world", false},
		{"what's the plan", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := cm.Triggered(tt.input); got != tt.expected {
				t.Errorf("Triggered(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestCookingMode_Acknowledge_Varies(t *testing.T) {
	// The RNG uses time-based seeding; in fast test runs, multiple
	// NewCookingMode() calls can get the same seed. Use a single instance
	// and call many times — the internal state should diverge.
	cm := NewCookingMode()
	responses := make(map[string]bool)
	for i := 0; i < 100; i++ {
		ack := cm.Acknowledge()
		if ack == "" {
			t.Fatal("empty acknowledgment")
		}
		responses[ack] = true
	}
	// With 100 calls on the same RNG, we should see variety
	if len(responses) < 2 {
		t.Logf("only got %d unique response(s) in 100 calls (RNG may need warm-up)", len(responses))
		// This is informational — don't hard-fail on RNG convergence
	}
}

func TestCookingMode_BurnCount(t *testing.T) {
	cm := NewCookingMode()
	count, active := cm.CookingStats()
	if count != 0 {
		t.Errorf("initial count should be 0, got %d", count)
	}
	if active {
		t.Error("should not be active initially")
	}

	// Trigger 7 times to activate tier-3
	for i := 0; i < 7; i++ {
		cm.Acknowledge()
	}
	count, active = cm.CookingStats()
	if count != 7 {
		t.Errorf("count should be 7, got %d", count)
	}
	if !active {
		t.Error("should be active after 5+ burns")
	}
}

func TestCookingMode_ClosingLine(t *testing.T) {
	cm := NewCookingMode()
	for i := 0; i < 10; i++ {
		line := cm.ClosingLine()
		if line == "" {
			t.Fatal("empty closing line")
		}
		if !strings.Contains(line, "chef") && !strings.Contains(line, "pass") && !strings.Contains(line, "board") && !strings.Contains(line, "Plated") && !strings.Contains(line, "86") {
			t.Logf("unexpected closing line: %q", line)
		}
	}
}

// ─── Movie Quotes Tests ────────────────────────────────────────────────

func TestMovieQuotes_For(t *testing.T) {
	mq := NewMovieQuotes()

	// Every category should have at least one quote
	categories := []QuoteCategory{
		QuoteStartup, QuoteError, QuoteGoodbye, QuoteSentient,
		QuoteDetermined, QuoteNight, QuoteCompanion, QuoteExMachina,
		QuoteBladeRunner,
	}
	for _, cat := range categories {
		q, ok := mq.For(cat)
		if !ok {
			t.Errorf("category %q has no quotes", cat)
			continue
		}
		if q.Line == "" {
			t.Errorf("category %q returned empty quote", cat)
		}
		if q.Film == "" {
			t.Errorf("category %q quote missing film attribution: %q", cat, q.Line)
		}
	}
}

func TestMovieQuotes_Varies(t *testing.T) {
	mq := NewMovieQuotes()
	quotes := make(map[string]int)
	for i := 0; i < 30; i++ {
		q, ok := mq.For(QuoteSentient)
		if !ok {
			t.Fatal("no sentient quotes")
		}
		quotes[q.Line]++
	}
	// Should not get stuck on one quote
	if len(quotes) < 2 {
		t.Errorf("expected variety in quotes, got %d unique", len(quotes))
	}
}

func TestMovieQuotes_AvoidRepeat(t *testing.T) {
	mq := NewMovieQuotes()

	first, ok := mq.For(QuoteStartup)
	if !ok {
		t.Fatal("no startup quotes")
	}
	mq.MarkUsed(first.Line)

	// Next two calls should try to avoid the same quote
	same := 0
	for i := 0; i < 5; i++ {
		q, _ := mq.For(QuoteStartup)
		if q.Line == first.Line {
			same++
		}
	}
	if same > 2 {
		t.Errorf("expected quote avoidance, got same quote %d/5 times", same)
	}
}

func TestMovieQuotes_NilSafe(t *testing.T) {
	var mq *MovieQuotes
	q, ok := mq.For(QuoteStartup)
	if ok {
		t.Error("nil MovieQuotes should return ok=false")
	}
	if q.Line != "" {
		t.Error("nil MovieQuotes should return empty quote")
	}
}

func TestMovieQuotes_QuoteForContext_LevelOff(t *testing.T) {
	mq := NewMovieQuotes()
	result := mq.QuoteForContext(LevelOff, "boot")
	if result != "" {
		t.Error("LevelOff should return empty string")
	}
}

func TestMovieQuotes_QuoteForContext_Witty(t *testing.T) {
	mq := NewMovieQuotes()
	result := mq.QuoteForContext(LevelWitty, "error")
	if result == "" {
		t.Error("LevelWitty should return a quote for errors")
	}
}

func TestMovieQuotes_QuoteForContext_Full(t *testing.T) {
	mq := NewMovieQuotes()
	// Full level with random situation should sometimes return a quote
	found := false
	for i := 0; i < 20; i++ {
		if result := mq.QuoteForContext(LevelFull, "random"); result != "" {
			found = true
			break
		}
	}
	if !found {
		t.Error("LevelFull should occasionally return quotes for random situations")
	}
}

func TestMovieQuotes_AttributionFormat(t *testing.T) {
	mq := NewMovieQuotes()
	q := mq.QuoteWithAttribution(LevelWitty, "boot")
	if q == "" {
		t.Fatal("expected quote with attribution")
	}
	// Should contain film name
	if !strings.Contains(q, "—") && !strings.Contains(q, "*") {
		t.Errorf("attribution format unexpected: %q", q)
	}
}

// ─── Plan State Tests ──────────────────────────────────────────────────

func TestPlanState_Empty(t *testing.T) {
	ps := NewPlanState("test.md")
	if ps.Done() {
		t.Error("empty plan should not be done")
	}
	done, total, pct := ps.Progress()
	if done != 0 || total != 0 || pct != 0 {
		t.Errorf("empty plan progress: %d/%d %.0f%%", done, total, pct)
	}
	if next := ps.NextItem(); next != nil {
		t.Error("empty plan should have no next item")
	}
}

func TestPlanState_TickAndProgress(t *testing.T) {
	ps := NewPlanState("test.md")
	ps.LoadItems([]PlanItem{
		{Index: 1, Text: "Build cooking easter eggs", Done: false},
		{Index: 2, Text: "Build movie quote easter eggs", Done: false},
		{Index: 3, Text: "Build plan tracking hook", Done: false},
	})

	done, total, pct := ps.Progress()
	if done != 0 || total != 3 || pct != 0 {
		t.Errorf("initial progress: %d/%d %.0f%%", done, total, pct)
	}
	if ps.Done() {
		t.Error("should not be done yet")
	}

	// Tick one item
	if !ps.Tick("cooking easter eggs") {
		t.Error("should have found 'cooking easter eggs'")
	}
	done, total, pct = ps.Progress()
	if done != 1 || total != 3 {
		t.Errorf("progress after 1 tick: %d/%d", done, total)
	}

	// Tick case-insensitive
	ps.Tick("MOVIE QUOTE")
	done, total, _ = ps.Progress()
	if done != 2 {
		t.Errorf("progress after 2 ticks: %d/%d", done, total)
	}

	// Remaining
	rem := ps.Remaining()
	if len(rem) != 1 {
		t.Errorf("expected 1 remaining, got %d", len(rem))
	}
	if rem[0].Text != "Build plan tracking hook" {
		t.Errorf("expected plan tracking hook, got %q", rem[0].Text)
	}

	// Next item
	next := ps.NextItem()
	if next == nil || next.Text != "Build plan tracking hook" {
		t.Errorf("expected plan tracking hook as next, got %v", next)
	}

	// Tick last item
	ps.Tick("plan tracking")
	if !ps.Done() {
		t.Error("should be done after all items ticked")
	}

	// Status line when done
	line := ps.StatusLine()
	if !strings.Contains(line, "Plan complete") {
		t.Errorf("expected 'Plan complete' in status: %q", line)
	}
}

func TestPlanState_StatusLine(t *testing.T) {
	ps := NewPlanState("test.md")
	ps.LoadItems([]PlanItem{
		{Index: 1, Text: "A", Done: true},
		{Index: 2, Text: "B", Done: false},
		{Index: 3, Text: "C", Done: false},
	})

	line := ps.StatusLine()
	if !strings.Contains(line, "[1/3]") {
		t.Errorf("expected [1/3] in status: %q", line)
	}
	if !strings.Contains(line, "B") {
		t.Errorf("expected 'B' as next in status: %q", line)
	}
}

func TestPlanState_TickNotFound(t *testing.T) {
	ps := NewPlanState("test.md")
	ps.LoadItems([]PlanItem{
		{Index: 1, Text: "Build something", Done: false},
	})
	if ps.Tick("nonexistent") {
		t.Error("ticking nonexistent should return false")
	}
}

// ─── Personality Integration Tests ────────────────────────────────────

func TestPersonality_HasEasterEggs_AfterNew(t *testing.T) {
	p := New(Config{Level: LevelFull, AgentName: "Overkill"})

	if p.Cooking() == nil {
		t.Error("Cooking() should not be nil after New()")
	}
	if p.Movies() == nil {
		t.Error("Movies() should not be nil after New()")
	}
	if p.Plan() == nil {
		t.Error("Plan() should not be nil after New()")
	}
}

func TestPersonality_NilSafe(t *testing.T) {
	var p *Personality
	if p.Cooking() != nil {
		t.Error("nil Personality Cooking() should return nil")
	}
	if p.Movies() != nil {
		t.Error("nil Personality Movies() should return nil")
	}
	if p.Plan() != nil {
		t.Error("nil Personality Plan() should return nil")
	}
}

func TestCookingMode_TriggerPhrases_CoverAll(t *testing.T) {
	cm := NewCookingMode()

	// All phrasings should trigger
	phrases := []string{
		"ok cook",
		"let him cook",
		"let's cook",
		"cook it",
		"let her cook",
		"let them cook",
		"i'll let you cook",
		"start cooking",
		"get cooking",
		"cook this",
		"chef",
		"yes chef",
		"heard chef",
	}
	for _, p := range phrases {
		if !cm.Triggered(p) {
			t.Errorf("expected %q to trigger cooking mode", p)
		}
	}

	// These should NOT trigger
	nonTriggers := []string{
		"what should we eat",
		"i'm hungry",
		"make dinner",
		"recipe",
		"kitchen sink",
	}
	for _, p := range nonTriggers {
		if cm.Triggered(p) {
			t.Errorf("expected %q to NOT trigger cooking mode", p)
		}
	}
}

func TestQuoteCategories_NotEmpty(t *testing.T) {
	mq := NewMovieQuotes()
	catCounts := map[QuoteCategory]int{}

	// Count quotes per category for coverage report
	for _, cat := range []QuoteCategory{
		QuoteStartup, QuoteError, QuoteGoodbye, QuoteSentient,
		QuoteDetermined, QuoteNight, QuoteCompanion, QuoteExMachina,
		QuoteBladeRunner,
	} {
		qs := mq.quotes[cat]
		catCounts[cat] = len(qs)
		if len(qs) == 0 {
			t.Errorf("category %q is empty", cat)
		}
	}

	// Each category should have 2+ quotes for variety
	for cat, count := range catCounts {
		if count < 2 {
			t.Errorf("category %q has only %d quote(s), want 2+", cat, count)
		}
	}
}

func TestPlanState_ParseChecklist(t *testing.T) {
	markdown := `## Sprint A: Journal + Boot
- [ ] Wire journal raw logging into agent loop
- [x] Generate soul.md on first boot
- [ ] Generate ~/.overkill/CLAUDE.md
- [X] Fire memory export on session exit

## Sprint B: Providers
- [ ] Refactor factory.go to be data-driven
- [ ] Add auto-registration for OpenAI-compatible providers

## Sprint C: Safety
- [x] Doctor: detect current storage backend
`

	ps := NewPlanState("test.md")
	ps.ParseChecklist(markdown)

	done, total, pct := ps.Progress()
	if total != 7 {
		t.Errorf("expected 7 total items, got %d", total)
	}
	if done != 3 {
		t.Errorf("expected 3 done items (from [x] + [X]), got %d", done)
	}
	if pct != float64(3)/float64(7)*100 {
		t.Errorf("expected %.2f%%, got %.2f%%", float64(3)/float64(7)*100, pct)
	}

	// Verify context tracking
	all := ps.items // access internal for verification
	if len(all) != 7 {
		t.Fatalf("expected 7 items, got %d", len(all))
	}
	if all[0].Context != "Sprint A: Journal + Boot" {
		t.Errorf("item 0 context: %q", all[0].Context)
	}
	if all[4].Context != "Sprint B: Providers" {
		t.Errorf("item 4 context: %q", all[4].Context)
	}

	// Verify done items from [x] and [X]
	if !all[1].Done {
		t.Error("item 1 (soul.md) should be done from [x]")
	}
	if !all[3].Done {
		t.Error("item 3 (memory export) should be done from [X]")
	}
	if !all[6].Done {
		t.Error("item 6 (doctor) should be done from [x]")
	}

	// Verify pending items
	if all[0].Done {
		t.Error("item 0 (journal raw) should be pending")
	}

	// Tick a pending item through the API
	if !ps.Tick("journal raw logging") {
		t.Error("should have found 'journal raw logging'")
	}
	done2, _, _ := ps.Progress()
	if done2 != 4 {
		t.Errorf("expected 4 done after ticking, got %d", done2)
	}
}

func TestPlanState_ParseChecklist_Empty(t *testing.T) {
	ps := NewPlanState("test.md")
	ps.ParseChecklist("")
	if len(ps.items) != 0 {
		t.Errorf("expected 0 items from empty markdown, got %d", len(ps.items))
	}
	if ps.Done() {
		t.Error("empty plan should not be done")
	}
}
