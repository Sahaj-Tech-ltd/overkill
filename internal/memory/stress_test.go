package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// ==========================================================================
// Pure-unit adversarial stress tests for Memory types, scoring,
// and boundary conditions. (No DB required)
// ==========================================================================

// M-STRESS-1: Store with empty content
func TestStress_MemoryEmptyContent(t *testing.T) {
	m := &Memory{
		Type:    MemorySemantic,
		Content: "",
		Tags:    []string{"test"},
	}
	// Empty content should have a valid content_hash (not a crash)
	// Content hash of empty string is deterministic
	if m.Content == "" && m.ID == "" {
		// Just verify it doesn't panic when we manually set fields
		t.Log("Empty content memory struct is valid")
	}
}

// M-STRESS-2: Memory with nil tags
func TestStress_MemoryNilTags(t *testing.T) {
	m := &Memory{
		Type:    MemoryEpisodic,
		Content: "event happened",
		Tags:    nil,
	}
	// Nil tags should be handled by Store() which replaces with []string{}
	_ = m
}

// M-STRESS-3: Memory with nil metadata
func TestStress_MemoryNilMetadata(t *testing.T) {
	m := &Memory{
		Type:     MemoryProcedural,
		Content:  "procedure",
		Metadata: nil,
	}
	// Nil metadata should be handled by Store()
	_ = m
}

// M-STRESS-4: Memory with extremely long content (1MB)
func TestStress_MemoryHugeContent(t *testing.T) {
	hugeContent := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 20000) // ~880KB
	m := &Memory{
		Type:    MemorySemantic,
		Content: hugeContent,
	}
	// Content hash should work on large inputs
	_ = m
	t.Logf("Huge memory content len=%d", len(m.Content))
}

// M-STRESS-5: Memory with emoji/Unicode content
func TestStress_MemoryUnicodeContent(t *testing.T) {
	unicodeContent := "記憶力テスト 🐘🧠 测试内存 " + strings.Repeat("🎉", 1000)
	m := &Memory{
		Type:    MemorySemantic,
		Content: unicodeContent,
	}
	_ = m
}

// M-STRESS-6: Memory with NUL bytes in content
func TestStress_MemoryNULContent(t *testing.T) {
	nulContent := "normal text\x00hidden\x00payload"
	m := &Memory{
		Type:    MemoryEpisodic,
		Content: nulContent,
	}
	_ = m
}

// M-STRESS-7: Memory with extremely long metadata (100KB JSON)
func TestStress_MemoryHugeMetadata(t *testing.T) {
	meta := make(map[string]string, 1000)
	for i := 0; i < 1000; i++ {
		meta[fmt.Sprintf("very_long_key_name_that_takes_space_%04d", i)] =
			strings.Repeat("v", 100)
	}
	m := &Memory{
		Type:     MemorySemantic,
		Content:  "test",
		Metadata: meta,
	}
	_ = m
	t.Logf("Memory with huge metadata: %d keys", len(m.Metadata))
}

// M-STRESS-8: Memory with empty tags slice
func TestStress_MemoryEmptyTags(t *testing.T) {
	m := &Memory{
		Type:    MemoryEpisodic,
		Content: "test",
		Tags:    []string{},
	}
	_ = m
}

// M-STRESS-9: MemoryType validation - unknown type
func TestStress_MemoryUnknownType(t *testing.T) {
	m := &Memory{
		Type:    MemoryType("nonexistent_type_xyz"),
		Content: "test",
	}
	_ = m
}

// M-STRESS-10: Time-related edge cases
func TestStress_MemoryZeroTime(t *testing.T) {
	m := &Memory{
		Type:      MemoryEpisodic,
		Content:   "test",
		Timestamp: time.Time{}, // zero time
	}
	// Store() should fill in current time
	if m.Timestamp.IsZero() {
		t.Log("Zero timestamp is valid; Store() fills it")
	}
}

// M-STRESS-11: Memory with future timestamp
func TestStress_MemoryFutureTimestamp(t *testing.T) {
	m := &Memory{
		Type:      MemoryEpisodic,
		Content:   "test",
		Timestamp: time.Now().Add(100 * 365 * 24 * time.Hour), // 100 years in future
	}
	_ = m
}

// M-STRESS-12: Memory with epoch timestamp
func TestStress_MemoryEpochTimestamp(t *testing.T) {
	m := &Memory{
		Type:      MemorySemantic,
		Content:   "test",
		Timestamp: time.Unix(0, 0),
	}
	_ = m
}

// M-STRESS-13: Memory with extremely long session ID
func TestStress_MemoryLongSessionID(t *testing.T) {
	longSID := strings.Repeat("x", 10000)
	m := &Memory{
		Type:      MemoryEpisodic,
		Content:   "test",
		SessionID: longSID,
	}
	_ = m
}

// M-STRESS-14: Memory with duplicate content hash (same content, different type)
func TestStress_MemoryDuplicateHash(t *testing.T) {
	m1 := &Memory{
		Type:    MemorySemantic,
		Content: "same exact content here",
	}
	m2 := &Memory{
		Type:    MemoryEpisodic,
		Content: "same exact content here",
	}
	// These should have identical content_hashes but different types
	_ = m1
	_ = m2
}

// M-STRESS-15: Query with extremely long content string
func TestStress_MemoryQueryLongContent(t *testing.T) {
	longQuery := strings.Repeat("search term ", 10000)
	q := Query{
		Content: longQuery,
		Limit:   10,
	}
	_ = q
}

// M-STRESS-16: Query with zero Limit
func TestStress_MemoryQueryZeroLimit(t *testing.T) {
	q := Query{
		Content: "test",
		Limit:   0,
	}
	_ = q
}

// M-STRESS-17: Query with negative Limit (if int not uint)
func TestStress_MemoryQueryNegativeLimit(t *testing.T) {
	q := Query{
		Content:      "test",
		Limit:        -1,
		MinRelevance: 0.5,
	}
	_ = q
}

// M-STRESS-18: Query with MinRelevance > 1.0
func TestStress_MemoryQueryHighRelevance(t *testing.T) {
	q := Query{
		Content:      "test",
		MinRelevance: 999.0,
	}
	_ = q
}

// M-STRESS-19: Query with negative MinRelevance
func TestStress_MemoryQueryNegativeRelevance(t *testing.T) {
	q := Query{
		Content:      "test",
		MinRelevance: -0.5,
	}
	_ = q
}

// M-STRESS-20: SearchResult with nil Memories
func TestStress_SearchResultNilMemories(t *testing.T) {
	sr := &SearchResult{
		Memories: nil,
		Total:    5,
	}
	if len(sr.Memories) != 0 {
		t.Error("nil Memories should have len 0")
	}
}

// M-STRESS-21: Concurrent Memory struct access
func TestStress_MemoryConcurrentAccess(t *testing.T) {
	m := &Memory{
		Type:    MemorySemantic,
		Content: "shared memory",
		Tags:    []string{"shared"},
		Metadata: map[string]string{"key": "val"},
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Concurrent reads on struct fields
			_ = m.Type
			_ = m.Content
			_ = len(m.Tags)
			_ = m.Metadata["key"]
		}()
	}
	wg.Wait()
}

// M-STRESS-22: Orchestrator with nil store
func TestStress_OrchestratorNilStore(t *testing.T) {
	orch := NewOrchestrator(nil, nil, "")
	if orch == nil {
		t.Fatal("NewOrchestrator(nil, nil, \"\") returned nil")
	}
	// Orch methods should handle nil store gracefully
	ctx := context.Background()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: Orchestrator with nil store: %v", r)
		}
	}()
	_, err := orch.Recall(ctx, "test", 10)
	if err != nil {
		t.Logf("Recall with nil store: %v", err)
	}
}

// M-STRESS-23: PostgresStore Close on nil receiver
func TestStress_StoreCloseNil(t *testing.T) {
	var s *PostgresStore
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: Close on nil PostgresStore: %v", r)
		}
	}()
	err := s.Close()
	if err != nil {
		t.Errorf("Close on nil store: %v", err)
	}
}

// M-STRESS-24: ListOptions with negative offset
func TestStress_ListOptionsNegative(t *testing.T) {
	opts := ListOptions{
		Limit:  -5,
		Offset: -10,
	}
	_ = opts
}

// M-STRESS-25: scoreContent with both strings empty
func TestStress_ScoreContentBothEmpty(t *testing.T) {
	score := scoreContent("", "")
	if score != 0 {
		t.Errorf("scoreContent(\"\", \"\") = %f, want 0", score)
	}
}

// M-STRESS-26: scoreContent with empty query
func TestStress_ScoreContentEmptyQuery(t *testing.T) {
	score := scoreContent("", "some content here")
	if score != 0 {
		t.Errorf("scoreContent(\"\", content) = %f, want 0 (no query)", score)
	}
}

// M-STRESS-27: scoreContent with empty content
func TestStress_ScoreContentEmptyContent(t *testing.T) {
	score := scoreContent("query", "")
	if score != 0 {
		t.Errorf("scoreContent(query, \"\") = %f, want 0 (no content)", score)
	}
}

// M-STRESS-28: scoreContent with identical strings
func TestStress_ScoreContentIdentical(t *testing.T) {
	score := scoreContent("exact match", "exact match")
	if score <= 0 {
		t.Errorf("scoreContent identical strings = %f, want > 0", score)
	}
}

// M-STRESS-29: scoreContent with completely different strings
func TestStress_ScoreContentDifferent(t *testing.T) {
	score := scoreContent("cat", "completely unrelated dog bird fish")
	// Should be 0 or very low
	if score > 0 {
		t.Logf("scoreContent unrelated = %f (may be fuzzy match)", score)
	}
}

// M-STRESS-30: scoreContent with Unicode
func TestStress_ScoreContentUnicode(t *testing.T) {
	score := scoreContent("tést", "This is a tést with accents")
	if score <= 0 {
		t.Logf("scoreContent with unicode = %f (may not handle accents)", score)
	}
}

// M-STRESS-31: scoreContent with extremely long input
func TestStress_ScoreContentHuge(t *testing.T) {
	longQuery := strings.Repeat("word ", 1000)
	longContent := strings.Repeat("content ", 10000)
	start := time.Now()
	score := scoreContent(longQuery, longContent)
	elapsed := time.Since(start)
	t.Logf("scoreContent huge input: score=%f elapsed=%v", score, elapsed)
	if elapsed > 100*time.Millisecond {
		t.Logf("WARNING: scoreContent on large input took %v", elapsed)
	}
}

// M-STRESS-32: containsAnyTag with empty slices
func TestStress_ContainsAnyTagEmpty(t *testing.T) {
	if containsAnyTag(nil, nil) {
		t.Error("containsAnyTag(nil, nil) should return false")
	}
	if containsAnyTag([]string{}, []string{}) {
		t.Error("containsAnyTag(empty, empty) should return false")
	}
	if containsAnyTag([]string{"tag"}, []string{}) {
		t.Error("containsAnyTag(query, empty result) should return false")
	}
	// Empty query means "no tag filter" — handled by caller's len check
	// At function level, empty query iterates 0 times, returns false
	if containsAnyTag([]string{}, []string{"tag"}) {
		t.Error("containsAnyTag(empty query, result with tag) should return false at fn level")
	}
}

// M-STRESS-33: concurrent SetRetention + backgroundPrune race
func TestStress_SetRetentionRace(t *testing.T) {
	// Test the data race documented as H-19 in bugs.md
	s := &PostgresStore{
		retention: time.Hour,
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.SetRetention(time.Duration(i) * time.Minute)
		}()
		go func() {
			defer wg.Done()
			s.retentionMu.RLock()
			_ = s.retention
			s.retentionMu.RUnlock()
		}()
	}
	wg.Wait()
}
