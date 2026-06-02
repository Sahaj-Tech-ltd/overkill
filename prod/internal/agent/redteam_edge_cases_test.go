// Package redteam_edge_cases — orchestrated edge-case destruction battery.
// Targets: nil/empty/unicode/overflow/race/boundary attacks across core packages.
// Run with: go test -race -v -run TestRedTeam ./internal/ -count=1

package agent

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/compaction"
	"github.com/Sahaj-Tech-ltd/overkill/internal/plugin"
	"github.com/Sahaj-Tech-ltd/overkill/internal/shadows"
	"github.com/Sahaj-Tech-ltd/overkill/internal/speculation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/speculative"
)

// ============================================================================
// VECTOR 1: Nil/null/empty bombardment
// ============================================================================

func TestRedTeam_NilSegmentScore(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("CRITICAL: Score(nil) panicked: %v", r)
		}
	}()
	result := compaction.Score(nil, compaction.ImportanceOptions{})
	if result != 0 {
		t.Logf("Score(nil) = %f (expected 0)", result)
	}
}

func TestRedTeam_NilSegmentInRank(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("CRITICAL: Rank with nil segments panicked: %v", r)
		}
	}()
	segs := []*compaction.Segment{
		nil,
		{ID: "a", Content: "hello"},
		nil,
		{ID: "b", Content: "world"},
		nil,
	}
	ranked := compaction.Rank(segs, compaction.ImportanceOptions{})
	if len(ranked) != 2 {
		t.Errorf("Rank returned %d segments, expected 2", len(ranked))
	}
}

func TestRedTeam_NilSegmentInCompact(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("CRITICAL: Compact with nil segments panicked: %v", r)
		}
	}()
	segs := []*compaction.Segment{
		nil,
		{ID: "a", Content: strings.Repeat("x", 1000)},
		nil,
	}
	keep, evict := compaction.Compact(segs, compaction.EvictionTarget{MaxTokens: 10, MinKeep: 0}, compaction.ImportanceOptions{})
	t.Logf("keep=%d evict=%d", len(keep), len(evict))
	if len(keep)+len(evict) != 1 {
		t.Errorf("Compact lost segments: keep=%d evict=%d, expected total=1", len(keep), len(evict))
	}
}

func TestRedTeam_EmptySegmentID(t *testing.T) {
	segs := []*compaction.Segment{
		{ID: "", Content: "orphan"},
	}
	err := compaction.EnsureSegmentsValid(segs)
	if err == nil {
		t.Errorf("CRITICAL: EnsureSegmentsValid accepted empty ID")
	}
}

func TestRedTeam_EmptyStringScore(t *testing.T) {
	seg := &compaction.Segment{ID: "e", Content: ""}
	s := compaction.Score(seg, compaction.ImportanceOptions{})
	if s < 0 {
		t.Errorf("Score for empty segment is negative: %f", s)
	}
	t.Logf("empty segment score: %f", s)
}

func TestRedTeam_NilAlarmSet(t *testing.T) {
	ac := automation.NewAlarmClock(func(a *automation.Alarm) error { return nil })
	err := ac.Set(nil)
	if err == nil {
		t.Errorf("CRITICAL: Set(nil) did not return error")
	}
}

func TestRedTeam_EmptyAlarmID(t *testing.T) {
	ac := automation.NewAlarmClock(func(a *automation.Alarm) error { return nil })
	err := ac.Set(&automation.Alarm{ID: ""})
	if err == nil {
		t.Errorf("CRITICAL: Set with empty ID did not return error")
	}
}

func TestRedTeam_NilCache_ReadAndCache(t *testing.T) {
	_, _, err := speculative.ReadAndCache(nil, "/tmp/foo")
	if err == nil {
		t.Errorf("CRITICAL: ReadAndCache(nil, ...) did not return error")
	}
}

func TestRedTeam_EmptyPathCachePut(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("CRITICAL: Put with empty path panicked: %v", r)
		}
	}()
	c := speculative.NewReadCache(speculative.Options{MaxBytes: 1024})
	c.Put("", []byte("data"), time.Now())
}

func TestRedTeam_NilBytesPut(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("CRITICAL: Put with nil bytes panicked: %v", r)
		}
	}()
	c := speculative.NewReadCache(speculative.Options{MaxBytes: 1024})
	c.Put("/tmp/foo", nil, time.Now())
}

func TestRedTeam_NilManifestValidate(t *testing.T) {
	m := plugin.Manifest{}
	err := plugin.ValidateManifest(m)
	if err == nil {
		t.Errorf("CRITICAL: ValidateManifest accepted empty name and version")
	}
}

// ============================================================================
// VECTOR 2: Unicode hell
// ============================================================================

func TestRedTeam_UnicodeSegmentID(t *testing.T) {
	seg := &compaction.Segment{
		ID:      "\U0001F4A9\u200D\u200C\u202E\u2066HELLO\u2069\u202C🎉",
		Content: "こんにちは世界🌍🔥💀",
	}
	s := compaction.Score(seg, compaction.ImportanceOptions{})
	if s < 0 || s != s {
		t.Errorf("Score for Unicode segment is invalid: %f", s)
	}
	ranked := compaction.Rank([]*compaction.Segment{seg}, compaction.ImportanceOptions{})
	if len(ranked) != 1 {
		t.Errorf("Unicode segment lost during rank")
	}
}

func TestRedTeam_UnicodeEmojiBomb(t *testing.T) {
	var emojiBomb strings.Builder
	for i := 0; i < 1000; i++ {
		emojiBomb.WriteString("🔥💀🎉🌍🦀🚀💣⚡🧨💥")
	}
	seg := &compaction.Segment{
		ID:      "emoji-bomb",
		Content: emojiBomb.String(),
	}
	s := compaction.Score(seg, compaction.ImportanceOptions{})
	if s < 0 || s != s {
		t.Errorf("Score for emoji bomb is invalid: %f", s)
	}
	t.Logf("emoji bomb len=%d score=%f", len(emojiBomb.String()), s)
}

func TestRedTeam_RightToLeftOverrideFilename(t *testing.T) {
	perm := plugin.Permissions{
		ConfigKeys: []string{"normal_key"},
		ToolsCall:  []string{"read_file"},
		Events:     []string{"startup"},
	}
	rtloKey := "\u202E" + "yek_lamron"
	if perm.AllowsConfigKey(rtloKey) {
		t.Errorf("MEDIUM: RLO key bypassed config key check: %q", rtloKey)
	}
	rtloTool := "\u202E" + "elif_daer"
	if perm.AllowsTool(rtloTool) {
		t.Errorf("MEDIUM: RLO tool name bypassed tool check: %q", rtloTool)
	}
}

func TestRedTeam_ZeroWidthJoiners(t *testing.T) {
	perm := plugin.Permissions{
		ToolsCall: []string{"read\u200Dfile"},
	}
	if !perm.AllowsTool("read\u200Dfile") {
		t.Errorf("LOW: ZWJ in tool name not matched exactly")
	}
	if perm.AllowsTool("readfile") {
		t.Errorf("MEDIUM: ZWJ tool name matched plain string")
	}
}

// ============================================================================
// VECTOR 3: Integer overflow & boundary conditions
// ============================================================================

func TestRedTeam_MaxIntTokenCost(t *testing.T) {
	seg := &compaction.Segment{
		ID:        "overflow",
		Content:   "test",
		TokenCost: math.MaxInt,
	}
	s := compaction.Score(seg, compaction.ImportanceOptions{})
	if s < 0 || s != s {
		t.Errorf("Score with MaxInt TokenCost is invalid: %f", s)
	}
	t.Logf("MaxInt token cost score: %f", s)
}

func TestRedTeam_NegativeTokenCost(t *testing.T) {
	seg := &compaction.Segment{
		ID:        "negative",
		Content:   "test",
		TokenCost: -1000,
	}
	s := compaction.Score(seg, compaction.ImportanceOptions{})
	if s < 0 || s != s {
		t.Errorf("Score with negative TokenCost is invalid: %f", s)
	}
	t.Logf("negative token cost score: %f (estimate fallthrough)", s)
}

func TestRedTeam_ZeroEvictionTarget(t *testing.T) {
	segs := []*compaction.Segment{
		{ID: "a", Content: "test1"},
		{ID: "b", Content: "test2"},
	}
	keep, evict := compaction.Compact(segs, compaction.EvictionTarget{MaxTokens: 0, MinKeep: 0}, compaction.ImportanceOptions{})
	if len(evict) != 0 {
		t.Errorf("MEDIUM: Compact with MaxTokens=0 evicted segments: %d", len(evict))
	}
	if len(keep) != 2 {
		t.Errorf("Compact lost segments with MaxTokens=0: keep=%d", len(keep))
	}
}

func TestRedTeam_NegativeMinKeep(t *testing.T) {
	segs := []*compaction.Segment{
		{ID: "a", Content: strings.Repeat("x", 1000)},
		{ID: "b", Content: strings.Repeat("y", 1000)},
	}
	keep, evict := compaction.Compact(segs, compaction.EvictionTarget{MaxTokens: 5, MinKeep: -1}, compaction.ImportanceOptions{})
	t.Logf("Negative MinKeep: keep=%d evict=%d", len(keep), len(evict))
}

func TestRedTeam_HugeContentSegment(t *testing.T) {
	huge := strings.Repeat("ABCDEFGH", (10*1024*1024)/8)
	seg := &compaction.Segment{
		ID:      "huge",
		Content: huge,
	}
	start := time.Now()
	s := compaction.Score(seg, compaction.ImportanceOptions{})
	elapsed := time.Since(start)
	if s < 0 || s != s {
		t.Errorf("Score for huge segment is invalid: %f", s)
	}
	t.Logf("10MB segment score=%f, elapsed=%v", s, elapsed)
}

func TestRedTeam_AccessCountOverflow(t *testing.T) {
	seg := &compaction.Segment{
		ID:          "popular",
		Content:     "test",
		AccessCount: 1_000_000_000,
	}
	s := compaction.Score(seg, compaction.ImportanceOptions{})
	if s < 0 || s != s {
		t.Errorf("Score with huge AccessCount is invalid: %f", s)
	}
	t.Logf("1B access count score: %f", s)
}

// ============================================================================
// VECTOR 4: NaN/Inf weight attacks
// ============================================================================

func TestRedTeam_NaNWeights(t *testing.T) {
	opts := compaction.ImportanceOptions{
		RecencyWeight: math.NaN(),
		ReuseWeight:   math.NaN(),
		CostWeight:    math.NaN(),
	}
	seg := &compaction.Segment{ID: "nan", Content: "test"}
	s := compaction.Score(seg, opts)
	if s != s {
		t.Errorf("CRITICAL: Score with NaN weights returned NaN")
	}
	if s < 0 {
		t.Errorf("Score with NaN weights returned negative: %f", s)
	}
	t.Logf("NaN weights score (should be sanitised): %f", s)
}

func TestRedTeam_InfWeights(t *testing.T) {
	opts := compaction.ImportanceOptions{
		RecencyWeight: math.Inf(1),
		ReuseWeight:   math.Inf(1),
		CostWeight:    math.Inf(1),
	}
	seg := &compaction.Segment{ID: "inf", Content: "test"}
	s := compaction.Score(seg, opts)
	if math.IsInf(s, 1) {
		t.Errorf("CRITICAL: Score with Inf weights returned +Inf (unbounded)")
	}
	t.Logf("Inf weights score (should be capped): %f", s)
}

func TestRedTeam_NegativeInfWeights(t *testing.T) {
	opts := compaction.ImportanceOptions{
		RecencyWeight: math.Inf(-1),
		ReuseWeight:   math.Inf(-1),
		CostWeight:    math.Inf(-1),
	}
	seg := &compaction.Segment{ID: "neginf", Content: "test"}
	s := compaction.Score(seg, opts)
	if math.IsInf(s, -1) {
		t.Errorf("CRITICAL: Score with -Inf weights returned -Inf")
	}
	t.Logf("-Inf weights score: %f", s)
}

// ============================================================================
// VECTOR 5: State machine corruption
// ============================================================================

func TestRedTeam_DoubleStartAlarmClock(t *testing.T) {
	ac := automation.NewAlarmClock(func(a *automation.Alarm) error { return nil })
	ac.Start()
	ac.Start() // should be no-op
	ac.Stop()
}

func TestRedTeam_DoubleStopAlarmClock(t *testing.T) {
	ac := automation.NewAlarmClock(func(a *automation.Alarm) error { return nil })
	ac.Start()
	ac.Stop()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("CRITICAL: Double Stop panicked: %v", r)
		}
	}()
	ac.Stop()
}

func TestRedTeam_StartAfterStop(t *testing.T) {
	ac := automation.NewAlarmClock(func(a *automation.Alarm) error { return nil })
	ac.Start()
	ac.Stop()
	ac.Start()
	defer ac.Stop()
}

func TestRedTeam_CancelNonExistentAlarm(t *testing.T) {
	ac := automation.NewAlarmClock(func(a *automation.Alarm) error { return nil })
	ok := ac.Cancel("nonexistent")
	if ok {
		t.Errorf("LOW: Cancel(nonexistent) returned true")
	}
}

func TestRedTeam_CancelAlreadyCancelled(t *testing.T) {
	ac := automation.NewAlarmClock(func(a *automation.Alarm) error { return nil })
	future := time.Now().Add(10 * time.Minute)
	err := ac.Set(&automation.Alarm{ID: "test", FireAt: future})
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	ac.Cancel("test")
	ok := ac.Cancel("test")
	if !ok {
		t.Errorf("Double Cancel should be idempotent (return true)")
	}
}

func TestRedTeam_SpeculationDoubleStart(t *testing.T) {
	e := speculation.NewEngine()
	e.OnSpeculate = func(ctx context.Context) (*speculation.Result, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	e.Start()
	e.Start()
	e.Discard()
	e.Reset()
}

func TestRedTeam_SpeculationAcceptWhenNotReady(t *testing.T) {
	e := speculation.NewEngine()
	r := e.Accept()
	if r != nil {
		t.Errorf("MEDIUM: Accept on idle engine returned non-nil")
	}
}

func TestRedTeam_SpeculationRapidStartDiscard(t *testing.T) {
	e := speculation.NewEngine()
	e.OnSpeculate = func(ctx context.Context) (*speculation.Result, error) {
		time.Sleep(100 * time.Millisecond)
		return &speculation.Result{Summary: "late"}, nil
	}
	e.Start()
	e.Discard()
	e.Start()
	time.Sleep(50 * time.Millisecond)
	e.Discard()
	e.Reset()
}

func TestRedTeam_SpeculationAcceptDiscardRace(t *testing.T) {
	e := speculation.NewEngine()
	ready := make(chan struct{})
	e.OnSpeculate = func(ctx context.Context) (*speculation.Result, error) {
		<-ready
		return &speculation.Result{Summary: "ready"}, nil
	}
	e.Start()
	close(ready)
	time.Sleep(200 * time.Millisecond)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		e.Accept()
	}()
	go func() {
		defer wg.Done()
		e.Discard()
	}()
	wg.Wait()
	e.Reset()
}

// ============================================================================
// VECTOR 6: Concurrency stress
// ============================================================================

func TestRedTeam_ConcurrentCacheGetPut(t *testing.T) {
	c := speculative.NewReadCache(speculative.Options{MaxBytes: 10 * 1024 * 1024})

	var wg sync.WaitGroup
	n := 50

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			path := fmt.Sprintf("/tmp/file_%d", idx)
			data := []byte(fmt.Sprintf("data_for_file_%d", idx))
			c.Put(path, data, time.Now())
		}(i)
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			path := fmt.Sprintf("/tmp/file_%d", idx)
			c.Get(path)
		}(i)
	}
	wg.Wait()

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := c.Stats()
			_ = s.HitRate
		}()
	}
	wg.Wait()

	var wg2 sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			c.Clear()
		}()
	}
	for i := 0; i < 5; i++ {
		wg2.Add(1)
		go func(idx int) {
			defer wg2.Done()
			c.Put(fmt.Sprintf("/tmp/clear_%d", idx), []byte("data"), time.Now())
		}(i)
	}
	wg2.Wait()
}

func TestRedTeam_ConcurrentAlarmOperations(t *testing.T) {
	ac := automation.NewAlarmClock(func(a *automation.Alarm) error { return nil })

	var wg sync.WaitGroup
	n := 30

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			future := time.Now().Add(time.Duration(idx) * time.Minute)
			_ = ac.Set(&automation.Alarm{
				ID:     fmt.Sprintf("alarm_%d", idx),
				FireAt: future,
			})
		}(i)
	}
	wg.Wait()

	var wg2 sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			ac.List()
		}()
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			ac.Pending()
		}()
	}
	for i := 0; i < n/2; i++ {
		wg2.Add(1)
		go func(idx int) {
			defer wg2.Done()
			ac.Cancel(fmt.Sprintf("alarm_%d", idx))
		}(i)
	}
	wg2.Wait()
}

func TestRedTeam_ConcurrentSegmentRanking(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			segs := []*compaction.Segment{
				{ID: "a", Content: "aaaa", AccessCount: 5},
				{ID: "b", Content: "bbbbbbbb", AccessCount: 2},
				{ID: "c", Content: "cccc", AccessCount: 10, Pinned: true},
			}
			ranked := compaction.Rank(segs, compaction.ImportanceOptions{})
			_ = ranked
		}()
	}
	wg.Wait()
}

// ============================================================================
// VECTOR 7: Half-life decay edge cases
// ============================================================================

func TestRedTeam_ZeroHalfLife(t *testing.T) {
	opts := compaction.ImportanceOptions{
		RecencyHalfLife: 0,
	}
	seg := &compaction.Segment{ID: "z", Content: "test", CreatedAt: time.Now().Add(-1 * time.Hour)}
	s := compaction.Score(seg, opts)
	if s < 0 || s != s {
		t.Errorf("Score with zero half-life is invalid: %f", s)
	}
	t.Logf("zero half-life score: %f", s)
}

func TestRedTeam_NegativeHalfLife(t *testing.T) {
	opts := compaction.ImportanceOptions{
		RecencyHalfLife: -5 * time.Minute,
	}
	seg := &compaction.Segment{ID: "neg", Content: "test", CreatedAt: time.Now().Add(-1 * time.Hour)}
	s := compaction.Score(seg, opts)
	if s < 0 || s != s {
		t.Errorf("Score with negative half-life is invalid: %f", s)
	}
	t.Logf("negative half-life score: %f", s)
}

func TestRedTeam_FutureCreatedAt(t *testing.T) {
	opts := compaction.ImportanceOptions{
		Now: func() time.Time { return time.Now() },
	}
	seg := &compaction.Segment{
		ID:        "future",
		Content:   "test",
		CreatedAt: time.Now().Add(24 * time.Hour),
	}
	s := compaction.Score(seg, opts)
	if s < 0 || s != s {
		t.Errorf("Score for future-created segment is invalid: %f", s)
	}
	t.Logf("future-created-at score: %f", s)
}

// ============================================================================
// VECTOR 8: Disk/IO edge cases
// ============================================================================

func TestRedTeam_CachePutExactMaxBytes(t *testing.T) {
	// ReadCache.Get does an os.Stat freshness check, so we must use a real file.
	f, err := os.CreateTemp("", "redteam-cache-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.Write(make([]byte, 128)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	f.Close()

	c := speculative.NewReadCache(speculative.Options{MaxBytes: 128})
	info, _ := os.Stat(path)
	c.Put(path, make([]byte, 128), info.ModTime())
	if _, hit := c.Get(path); !hit {
		t.Errorf("MEDIUM: exact-max-size entry was evicted")
	}
}

func TestRedTeam_CachePutHugeEntry(t *testing.T) {
	f, err := os.CreateTemp("", "redteam-huge-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.Write(make([]byte, 1024)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	f.Close()

	c := speculative.NewReadCache(speculative.Options{MaxBytes: 64})
	info, _ := os.Stat(path)
	c.Put(path, make([]byte, 1024), info.ModTime())
	if _, hit := c.Get(path); hit {
		t.Errorf("HIGH: oversized entry survived when it should be evicted")
	}
}

func TestRedTeam_CacheStatsUnderConcurrency(t *testing.T) {
	c := speculative.NewReadCache(speculative.Options{MaxBytes: 1024 * 1024})

	// The Get freshness check causes errors on non-existent paths, but
	// we only care about concurrency safety here — no assertions on hits.
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Use real file so Get doesn't always miss.
			f, err := os.CreateTemp("", fmt.Sprintf("redteam-c-%d-*", idx))
			if err != nil {
				return
			}
			path := f.Name()
			defer os.Remove(path)
			f.Write([]byte("x"))
			f.Close()
			info, _ := os.Stat(path)
			c.Put(path, []byte("x"), info.ModTime())
		}(i)
	}
	wg.Wait()

	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			c.Stats()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			c.Get(fmt.Sprintf("/tmp/c_%d", i))
		}
	}()
	go func() {
		defer wg.Done()
		c.Clear()
	}()
	wg.Wait()
}

// ============================================================================
// VECTOR 9: Prefetcher edge cases
// ============================================================================

func TestRedTeam_PrefetcherEnqueueEmptyPath(t *testing.T) {
	c := speculative.NewReadCache(speculative.Options{MaxBytes: 1024})
	p := speculative.NewPrefetcher(c, 2, 32)
	p.Start(2)
	defer p.Stop()

	ok := p.Enqueue("")
	if ok {
		t.Errorf("MEDIUM: Enqueue(\"\") returned true when it should be rejected")
	}
	n := p.EnqueueAll([]string{"", "/tmp/valid", ""})
	if n != 1 {
		t.Errorf("EnqueueAll with empty strings returned %d, expected 1", n)
	}
}

func TestRedTeam_PrefetcherStartStopRace(t *testing.T) {
	c := speculative.NewReadCache(speculative.Options{MaxBytes: 1024})
	p := speculative.NewPrefetcher(c, 2, 32)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Start(2)
			time.Sleep(10 * time.Millisecond)
			p.Stop()
		}()
	}
	wg.Wait()
}

func TestRedTeam_PrefetcherEnqueueAfterStop(t *testing.T) {
	c := speculative.NewReadCache(speculative.Options{MaxBytes: 1024})
	p := speculative.NewPrefetcher(c, 2, 32)
	p.Start(2)
	p.Stop()

	ok := p.Enqueue("/tmp/after_stop")
	if ok {
		t.Errorf("MEDIUM: Enqueue after Stop returned true")
	}
	n := p.EnqueueAll([]string{"/tmp/a", "/tmp/b"})
	if n != 0 {
		t.Errorf("EnqueueAll after Stop returned %d", n)
	}
}

func TestRedTeam_PrefetcherEnqueueBeforeStart(t *testing.T) {
	c := speculative.NewReadCache(speculative.Options{MaxBytes: 1024})
	p := speculative.NewPrefetcher(c, 2, 32)
	ok := p.Enqueue("/tmp/before_start")
	if ok {
		t.Errorf("MEDIUM: Enqueue before Start returned true")
	}
}

// ============================================================================
// VECTOR 10: Permission bypass attempts
// ============================================================================

func TestRedTeam_PermissionEmptyStringBypass(t *testing.T) {
	perm := plugin.Permissions{
		ToolsCall:  []string{"", "read_file", ""},
		ConfigKeys: []string{""},
		Events:     []string{""},
	}
	if perm.AllowsTool("some_tool") {
		t.Errorf("CRITICAL: empty string in ToolsCall matched valid tool")
	}
	if perm.AllowsConfigKey("some_key") {
		t.Errorf("CRITICAL: empty string in ConfigKeys matched valid key")
	}
	if perm.AllowsEvent("some_event") {
		t.Errorf("CRITICAL: empty string in Events matched valid event")
	}
	if !perm.AllowsTool("") {
		t.Logf("LOW: empty string did not match empty string (exact match only)")
	}
}

func TestRedTeam_PermissionUnicodeConfusable(t *testing.T) {
	perm := plugin.Permissions{
		ToolsCall: []string{"read_file"},
	}
	// Cyrillic 'е' (U+0435) vs Latin 'e' (U+0065)
	confusableTool := "r\u0435ad_fil\u0435"
	if perm.AllowsTool(confusableTool) {
		t.Errorf("HIGH: Unicode confusable bypass: %q matched %q", confusableTool, "read_file")
	}
}

// ============================================================================
// VECTOR 11: DOOM shadow package
// ============================================================================

func TestRedTeam_DoomPageIsValidHTML(t *testing.T) {
	page := shadows.DoomPage()
	if len(page) == 0 {
		t.Errorf("CRITICAL: DoomPage returned empty bytes")
	}
	pageStr := string(page)
	if !strings.Contains(pageStr, "<!DOCTYPE html>") {
		t.Errorf("DoomPage missing doctype")
	}
	if !strings.Contains(pageStr, "jsdos") {
		t.Errorf("DoomPage missing jsdos reference")
	}
}

func TestRedTeam_DoomPageXSSAttempt(t *testing.T) {
	pageStr := string(shadows.DoomPage())
	if strings.Contains(pageStr, "{{") || strings.Contains(pageStr, "${") {
		t.Errorf("MEDIUM: DoomPage contains template interpolation markers")
	}
}

// ============================================================================
// VECTOR 12: FormatEvictionReport edge cases
// ============================================================================

func TestRedTeam_FormatEvictionReportEdgeCases(t *testing.T) {
	// No compaction needed
	r := compaction.FormatEvictionReport(10, 1000, 10, 1000)
	if r != "no compaction needed" {
		t.Errorf("LOW: same-count report should be 'no compaction needed', got: %s", r)
	}
	// All evicted
	r = compaction.FormatEvictionReport(10, 1000, 0, 0)
	t.Logf("all evicted: %s", r)
	// Single eviction
	r = compaction.FormatEvictionReport(5, 500, 4, 400)
	t.Logf("one evicted: %s", r)
}

// ============================================================================
// VECTOR 13: Score boundary — incredibly old segment
// ============================================================================

func TestRedTeam_VeryOldSegment(t *testing.T) {
	opts := compaction.ImportanceOptions{
		Now: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}
	seg := &compaction.Segment{
		ID:        "ancient",
		Content:   "old data",
		CreatedAt: time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	s := compaction.Score(seg, opts)
	if s < 0 || s != s {
		t.Errorf("Score for 56-year-old segment is invalid: %f", s)
	}
	// Very old should score low (not zero necessarily due to cost component)
	if s > 0.6 {
		t.Logf("MEDIUM: very old segment scored high: %f", s)
	}
	t.Logf("56-year-old segment score: %f", s)
}
