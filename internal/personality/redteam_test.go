// Red-team test suite for internal/personality.
// Targets edge cases, concurrency, and adversarial inputs across
// FrustrationDetector, BlindSpotDetector, TransparencyEngine,
// ColdStartManager, and MemoEngine.
package personality

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── concurrency-safe AlertSink for race tests ──────────────────────────────
// (captureSink is already declared in alerts_wiring_test.go without a mutex;
// rtSink is a separate type used only where concurrent access is needed.)

type rtSink struct {
	mu    sync.Mutex
	count int
}

func (r *rtSink) Create(alertType, message, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count++
	return nil
}

func (r *rtSink) n() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// ── FrustrationDetector ────────────────────────────────────────────────────

// RT-F1: Empty message — no detection, no panic.
func TestFrustrationDetector_EmptyMessage(t *testing.T) {
	d := NewFrustrationDetector(nil, "s1")
	if got := d.Observe(""); got {
		t.Error("expected false for empty input, got true")
	}
	if got := d.Observe("   "); got {
		t.Error("expected false for whitespace-only input, got true")
	}
}

// RT-F2: Single all-caps 1-char message "A" — should NOT detect (requires ≥4 letters).
func TestFrustrationDetector_SingleCapChar(t *testing.T) {
	sink := &rtSink{}
	d := NewFrustrationDetector(sink, "s2")
	got := d.Observe("A")
	if got {
		t.Errorf("single char 'A' fired frustration alert unexpectedly")
	}
}

// RT-F3: Single profanity word — "wtf" is in the lexicon, score ≥ 2 requires two signals.
// Verify whether one profanity word alone trips the detector (it scores 1 point).
func TestFrustrationDetector_SingleProfanity(t *testing.T) {
	sink := &rtSink{}
	d := NewFrustrationDetector(sink, "s3")
	fired := d.Observe("wtf")
	// Score: lexicon match (+1) only → score=1 < 2 → should NOT fire.
	if fired {
		t.Logf("FINDING: single profanity 'wtf' fired frustration — score threshold may be too low")
	} else {
		t.Log("OK: single profanity 'wtf' did not fire (score < 2 as expected)")
	}

	// Now try with emphatic punctuation too (score should reach 2).
	d2 := NewFrustrationDetector(&rtSink{}, "s3b")
	fired2 := d2.Observe("wtf!!")
	if !fired2 {
		t.Log("NOTE: 'wtf!!' (lexicon + emphatic punct) did not fire — check scoring")
	} else {
		t.Log("OK: 'wtf!!' fired frustration as expected (score ≥ 2)")
	}
}

// RT-F4: 10,000-char message with frustration signal near the end.
func TestFrustrationDetector_LargeMessage(t *testing.T) {
	sink := &rtSink{}
	d := NewFrustrationDetector(sink, "s4")

	// Build a 10k char message: padding + frustration at char ~9999.
	padding := strings.Repeat("a ", 4950) // ~9900 chars
	msg := padding + "WTF THIS IS BROKEN!!"
	start := time.Now()
	fired := d.Observe(msg)
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Logf("FINDING: large message took %v — possible perf issue", elapsed)
	}
	if !fired {
		t.Log("NOTE: large message with 'WTF THIS IS BROKEN!!' did not fire — check caps+punct scoring on mixed content")
	}
}

// RT-F5: Unicode frustration — mathematical bold "𝕎𝕋𝔽". These are not ASCII A-Z,
// so the caps-count loop won't see them; lexicon match also won't fire.
func TestFrustrationDetector_UnicodeFrustration(t *testing.T) {
	sink := &rtSink{}
	d := NewFrustrationDetector(sink, "s5")
	fired := d.Observe("𝕎𝕋𝔽")
	if fired {
		t.Logf("FINDING: unicode math-bold '𝕎𝕋𝔽' fired frustration — false positive")
	} else {
		t.Log("OK: unicode math-bold did not fire (no ASCII caps, no lexicon match)")
	}
}

// RT-F6: Rapid-fire 1000 Observe calls — cooldown should prevent flooding.
func TestFrustrationDetector_RapidFire(t *testing.T) {
	sink := &rtSink{}
	d := NewFrustrationDetector(sink, "s6")
	// Use a message that scores ≥ 2: lexicon + emphatic punct.
	msg := "wtf!! wrong again!!"
	fires := 0
	for i := 0; i < 1000; i++ {
		if d.Observe(msg) {
			fires++
		}
	}
	// Cooldown = 5 minutes; after first fire, zero more should fire.
	if fires > 1 {
		t.Errorf("FINDING: cooldown not working — fired %d times over 1000 rapid calls", fires)
	}
	t.Logf("fires=%d over 1000 rapid calls (expected ≤ 1)", fires)
}

// RT-F7: IsHot(0) — zero duration edge case.
func TestFrustrationDetector_IsHotZeroDuration(t *testing.T) {
	d := NewFrustrationDetector(nil, "s7")
	// No alerts fired yet.
	if d.IsHot(0) {
		t.Error("FINDING: IsHot(0) returned true before any alert")
	}
	// Fire an alert by reaching score ≥ 2.
	d.Observe("wtf!! wrong again")
	// With 0 duration, even a just-fired alert should NOT be "hot".
	if d.IsHot(0) {
		t.Log("FINDING: IsHot(0) returned true — any alert is 'hot' with zero window (expected false)")
	}
}

// RT-F8: Concurrent Observe from 20 goroutines — race detector coverage.
func TestFrustrationDetector_ConcurrentObserve(t *testing.T) {
	sink := &rtSink{}
	d := NewFrustrationDetector(sink, "s8")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			d.Observe(fmt.Sprintf("wtf!! broken again %d", n))
			_ = d.IsHot(time.Minute)
		}(i)
	}
	wg.Wait()
	// If we reach here without a data race or panic, concurrency is safe.
}

// ── BlindSpotDetector ──────────────────────────────────────────────────────

// RT-B1: Observe same verb 1000 times — MaxAlerts must prevent flooding.
func TestBlindSpotDetector_MaxAlerts(t *testing.T) {
	bsd := NewBlindSpotDetector()
	bsd.MaxAlerts = 1
	for i := 0; i < 1000; i++ {
		bsd.Observe("fix")
	}
	alerts := 0
	for i := 0; i < 1000; i++ {
		if msg, ok := bsd.Check(); ok {
			alerts++
			_ = msg
		}
	}
	if alerts > 1 {
		t.Errorf("FINDING: MaxAlerts=1 but fired %d alerts", alerts)
	}
}

// RT-B2: Unknown verb (not in predefined list) — handled gracefully.
func TestBlindSpotDetector_UnknownVerb(t *testing.T) {
	bsd := NewBlindSpotDetector()
	// "deploy" is not in blindSpotVerbs.
	for i := 0; i < 10; i++ {
		bsd.Observe("deploy")
	}
	msg, ok := bsd.Check()
	if ok {
		t.Logf("NOTE: unknown verb 'deploy' accumulated and fired: %q", msg)
	} else {
		t.Log("OK: unknown verb 'deploy' observed but Check() returned no alert (threshold not reached via unknown key)")
	}
}

// RT-B3: NextWarning before any Observe — empty string, no panic.
func TestBlindSpotDetector_NextWarningBeforeObserve(t *testing.T) {
	bsd := NewBlindSpotDetector()
	got := bsd.NextWarning()
	if got != "" {
		t.Errorf("FINDING: NextWarning() before any Observe returned %q, want empty", got)
	}
}

// RT-B3b: nil receiver NextWarning — no panic.
func TestBlindSpotDetector_NilNextWarning(t *testing.T) {
	var bsd *BlindSpotDetector
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("FINDING: nil BlindSpotDetector.NextWarning() panicked: %v", r)
		}
	}()
	_ = bsd.NextWarning()
}

// RT-B4: Threshold=0 — fires on first observation (count >= 0 always true).
func TestBlindSpotDetector_ThresholdZero(t *testing.T) {
	bsd := NewBlindSpotDetector()
	bsd.Threshold = 0
	bsd.Observe("fix")
	msg, ok := bsd.Check()
	if !ok {
		t.Error("FINDING: Threshold=0 did not fire on first observation — boundary condition missed")
	} else {
		t.Logf("OK: Threshold=0 fired on first observation: %q", msg)
	}
}

// RT-B5: Concurrent Observe + Check from 20 goroutines — race detector.
func TestBlindSpotDetector_ConcurrentObserveCheck(t *testing.T) {
	bsd := NewBlindSpotDetector()
	bsd.MaxAlerts = 100
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			bsd.Observe(fmt.Sprintf("verb%d", n%5))
		}(i)
		go func() {
			defer wg.Done()
			_, _ = bsd.Check()
		}()
	}
	wg.Wait()
}

// ── TransparencyEngine ─────────────────────────────────────────────────────

// RT-T1: NextWarning with no recorded failures — empty, no panic.
func TestTransparencyEngine_NextWarningNoFailures(t *testing.T) {
	te := NewTransparencyEngine("gpt-4")
	got := te.NextWarning()
	if got != "" {
		t.Errorf("FINDING: NextWarning() with no failures returned %q, want empty", got)
	}
}

// RT-T1b: nil receiver NextWarning — no panic.
func TestTransparencyEngine_NilNextWarning(t *testing.T) {
	var te *TransparencyEngine
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("FINDING: nil TransparencyEngine.NextWarning() panicked: %v", r)
		}
	}()
	_ = te.NextWarning()
}

// RT-T2: RecordFailure with empty task type — stored? retrieved?
func TestTransparencyEngine_EmptyTaskType(t *testing.T) {
	te := NewTransparencyEngine("claude-3")
	te.RecordFailure("", "claude-3")
	te.RecordFailure("", "claude-3")
	msg, ok := te.Check("")
	if ok {
		t.Logf("NOTE: empty task type stored and triggered warning: %q", msg)
	} else {
		t.Log("OK: empty task type did not trigger warning")
	}
}

// RT-T3: RecordFailure 10,000 times same task type — count cap?
func TestTransparencyEngine_RecordFailureHigh(t *testing.T) {
	te := NewTransparencyEngine("m1")
	for i := 0; i < 10_000; i++ {
		te.RecordFailure("refactor", "m1")
	}
	// Verify count is stored (no cap enforced by current impl).
	te.mu.Lock()
	var found int
	for _, f := range te.failures {
		if f.TaskType == "refactor" && f.Model == "m1" {
			found = f.Count
		}
	}
	te.mu.Unlock()
	if found != 10_000 {
		t.Logf("NOTE: after 10k RecordFailure calls, count=%d (expected 10000 — no cap)", found)
	}
}

// RT-T4: Check() with nil model ID — panic?
func TestTransparencyEngine_NilModelID(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("FINDING: Check() with empty model panicked: %v", r)
		}
	}()
	te := NewTransparencyEngine("")
	te.RecordFailure("debug", "")
	te.RecordFailure("debug", "")
	msg, ok := te.Check("debug")
	t.Logf("Check with empty model: ok=%v msg=%q", ok, msg)
}

// RT-T5: MaxAlerts=0 — never fires.
func TestTransparencyEngine_MaxAlertsZero(t *testing.T) {
	te := NewTransparencyEngine("m1")
	te.maxWarnings = 0
	te.RecordFailure("debug", "m1")
	te.RecordFailure("debug", "m1")
	msg, ok := te.Check("debug")
	if ok {
		t.Errorf("FINDING: maxWarnings=0 still fired: %q", msg)
	}
	warn := te.NextWarning()
	if warn != "" {
		t.Errorf("FINDING: maxWarnings=0 NextWarning returned %q", warn)
	}
}

// RT-T6: Concurrent RecordFailure + Check from 20 goroutines — race.
func TestTransparencyEngine_ConcurrentAccess(t *testing.T) {
	te := NewTransparencyEngine("m1")
	te.maxWarnings = 100
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			te.RecordFailure(fmt.Sprintf("task%d", n%3), "m1")
		}(i)
		go func(n int) {
			defer wg.Done()
			_, _ = te.Check(fmt.Sprintf("task%d", n%3))
		}(i)
	}
	wg.Wait()
}

// ── ColdStartManager ───────────────────────────────────────────────────────

// RT-C1: IsColdStart with nonexistent path — returns true, no panic.
func TestColdStartManager_NonexistentPath(t *testing.T) {
	m := NewColdStartManager("/tmp/overkill-redteam-nonexistent-xyz123")
	if !m.IsColdStart() {
		t.Error("FINDING: IsColdStart() returned false for nonexistent dir")
	}
}

// RT-C2: IsColdStart with a directory path instead of file.
func TestColdStartManager_DirectoryPath(t *testing.T) {
	dir := t.TempDir()
	// NewColdStartManager appends "relationship.json"; pass dir so path = dir/relationship.json.
	// But let's also test: create a *directory* at that path.
	dirPath := dir + "/relationship.json"
	if err := os.Mkdir(dirPath, 0o750); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// IsColdStart should handle this gracefully — os.Stat on a dir returns Size()=4096+ not 0.
	proto := NewColdStartProtocol()
	result := proto.IsColdStart(dirPath)
	// A directory at the path is not empty — IsColdStart should return false (not cold start),
	// but the file is not a valid JSON relationship file. This is a potential gap.
	t.Logf("IsColdStart with directory at path: %v (dir size > 0 → false = 'not cold start' but file is a dir)", result)
	if !result {
		t.Log("FINDING: a directory at relationship.json path is treated as 'not cold start' — could cause downstream unmarshal errors")
	}
}

// RT-C3: ProcessResponse("") — should not panic; returns a profile.
func TestColdStartManager_EmptyResponse(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("FINDING: ProcessResponse('') panicked: %v", r)
		}
	}()
	dir := t.TempDir()
	m := NewColdStartManager(dir)
	profile, err := m.ProcessFirstResponse("")
	if err != nil {
		t.Logf("NOTE: ProcessFirstResponse('') returned err: %v", err)
	}
	if profile == nil {
		t.Error("FINDING: ProcessFirstResponse('') returned nil profile")
	} else {
		t.Logf("OK: empty response profile: verbosity=%q style=%q", profile.VerbosityPreference, profile.CommunicationStyle)
	}
}

// RT-C3b: ProcessResponse directly on protocol with "".
func TestColdStartProtocol_ProcessResponseEmpty(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("FINDING: ColdStartProtocol.ProcessResponse('') panicked: %v", r)
		}
	}()
	proto := NewColdStartProtocol()
	profile := proto.ProcessResponse("")
	if profile == nil {
		t.Error("FINDING: ProcessResponse('') returned nil")
	}
}

// RT-C4: ProcessResponse on 10KB response — timeout? OOM?
func TestColdStartManager_LargeResponse(t *testing.T) {
	dir := t.TempDir()
	m := NewColdStartManager(dir)
	large := strings.Repeat("I'm working on a critical urgent deployment fix refactor debug test build review migrate generate compile analysis. ", 100) // ~11KB
	start := time.Now()
	profile, err := m.ProcessFirstResponse(large)
	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Errorf("FINDING: 10KB response took %v — possible perf issue", elapsed)
	}
	if err != nil {
		t.Logf("NOTE: err=%v", err)
	}
	if profile != nil {
		t.Logf("OK: 10KB response processed in %v: verbosity=%q", elapsed, profile.VerbosityPreference)
	}
}

// RT-C5: ProcessFirstResponse idempotence — second call returns nil.
func TestColdStartManager_Idempotent(t *testing.T) {
	dir := t.TempDir()
	m := NewColdStartManager(dir)
	p1, _ := m.ProcessFirstResponse("Hello I am working on stuff")
	p2, _ := m.ProcessFirstResponse("Second call should be ignored")
	if p2 != nil {
		t.Errorf("FINDING: second ProcessFirstResponse returned non-nil profile — idempotency broken")
	}
	_ = p1
}

// ── MemoEngine ─────────────────────────────────────────────────────────────

// RT-M1: Empty context — Match returns non-empty default, no panic.
func TestMemoEngine_EmptyInput(t *testing.T) {
	e := NewMemoEngine(nil)
	result := e.Match("")
	if result.Phrase == "" {
		t.Error("FINDING: Match('') returned empty phrase — pickMemo may have failed")
	}
	t.Logf("OK: Match('') = %q (category=%q)", result.Phrase, result.Category)
}

// RT-M2: Pattern with regex special chars — should not panic (Learn silently skips bad patterns).
func TestMemoEngine_RegexSpecialCharsInLearn(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("FINDING: Learn with regex special chars panicked: %v", r)
		}
	}()
	e := NewMemoEngine(nil)
	badPatterns := []string{"[unclosed", "(?bad", "*quantifier", `\`}
	err := e.Learn(context.Background(), badPatterns, []string{"some phrase"}, "bad-category")
	if err != nil {
		t.Logf("NOTE: Learn returned err: %v", err)
	}
	// After bad patterns, Match should still work.
	result := e.Match("anything")
	if result.Phrase == "" {
		t.Error("FINDING: Match returned empty phrase after bad Learn patterns")
	}
}

// RT-M3: Learn("") — empty patterns and phrases.
func TestMemoEngine_LearnEmpty(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("FINDING: Learn with empty patterns panicked: %v", r)
		}
	}()
	e := NewMemoEngine(nil)
	err := e.Learn(context.Background(), []string{}, []string{}, "empty-cat")
	if err != nil {
		t.Logf("NOTE: Learn(empty) returned err: %v", err)
	}
	err2 := e.Learn(context.Background(), []string{""}, []string{""}, "empty-strings")
	if err2 != nil {
		t.Logf("NOTE: Learn(empty strings) returned err: %v", err2)
	}
	// The empty string pattern compiles fine in Go regex ("(?i)" + "" = "(?i)").
	// Verify Match still works.
	result := e.Match("anything")
	if result.Phrase == "" {
		t.Log("FINDING: Match returned empty phrase after Learn with empty strings")
	}
	t.Logf("OK: Learn with empty inputs handled gracefully. Match result: %q", result.Phrase)
}

// RT-M4: 10,000 patterns — performance on Match.
func TestMemoEngine_LargePatternsPerformance(t *testing.T) {
	e := NewMemoEngine(nil)
	// Add 10,000 custom patterns via repeated Learn calls.
	for i := 0; i < 500; i++ {
		patterns := make([]string, 20)
		phrases := make([]string, 5)
		for j := range patterns {
			patterns[j] = fmt.Sprintf("pattern%d_%d", i, j)
		}
		for j := range phrases {
			phrases[j] = fmt.Sprintf("phrase %d %d", i, j)
		}
		_ = e.Learn(context.Background(), patterns, phrases, fmt.Sprintf("cat%d", i))
	}
	// Now benchmark a Match call.
	start := time.Now()
	for k := 0; k < 100; k++ {
		_ = e.Match("pattern250_10 something")
	}
	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Logf("FINDING: 100 Match calls with 10k patterns took %v — perf concern", elapsed)
	}
	t.Logf("OK: 100 Match calls with 10k patterns took %v", elapsed)
}

// RT-M5: Concurrent Match + Learn — race detector.
func TestMemoEngine_ConcurrentMatchLearn(t *testing.T) {
	e := NewMemoEngine(nil)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			_ = e.Learn(context.Background(), []string{fmt.Sprintf("pat%d", n)}, []string{"phrase"}, fmt.Sprintf("cat%d", n))
		}(i)
		go func(n int) {
			defer wg.Done()
			_ = e.Match(fmt.Sprintf("pat%d", n))
		}(i)
	}
	wg.Wait()
}

// RT-M6: pickMemo with empty slice — should return "Processing...", not panic.
func TestPickMemoEmptySlice(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("FINDING: pickMemo(nil) panicked: %v", r)
		}
	}()
	got := pickMemo(nil)
	if got == "" {
		t.Error("FINDING: pickMemo(nil) returned empty string, want fallback 'Processing...'")
	}
	got2 := pickMemo([]string{})
	if got2 == "" {
		t.Error("FINDING: pickMemo([]) returned empty string, want fallback 'Processing...'")
	}
}
