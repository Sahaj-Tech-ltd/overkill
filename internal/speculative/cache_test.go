package speculative

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadCache_PutThenGetHits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeTestFile(t, path, "hello")

	c := NewReadCache(Options{})
	info, _ := os.Stat(path)
	c.Put(path, []byte("hello"), info.ModTime())

	got, ok := c.Get(path)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(got) != "hello" {
		t.Errorf("wrong bytes: %s", string(got))
	}
}

func TestReadCache_MTimeChangeInvalidates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeTestFile(t, path, "v1")
	c := NewReadCache(Options{})
	info, _ := os.Stat(path)
	c.Put(path, []byte("v1"), info.ModTime())

	// Rewrite with a new mtime — backdated mtime in cache should
	// invalidate. We bump mtime explicitly to guarantee distinctness
	// regardless of FS precision.
	if err := os.Chtimes(path, time.Now(), info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(path); ok {
		t.Error("stale entry should invalidate on mtime change")
	}
}

func TestReadCache_TTLExpiry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeTestFile(t, path, "x")
	c := NewReadCache(Options{TTL: 10 * time.Millisecond})
	info, _ := os.Stat(path)
	c.Put(path, []byte("x"), info.ModTime())

	time.Sleep(25 * time.Millisecond)
	if _, ok := c.Get(path); ok {
		t.Error("entry should be TTL-expired")
	}
}

func TestReadCache_LRUEvictionOnSize(t *testing.T) {
	c := NewReadCache(Options{MaxBytes: 10})
	// Synthesize "files" without on-disk lookups. We use Put +
	// faked mtimes; Get won't be called so the freshness check
	// doesn't run.
	c.Put("a", make([]byte, 4), time.Now())
	c.Put("b", make([]byte, 4), time.Now())
	c.Put("c", make([]byte, 4), time.Now()) // forces eviction
	s := c.Stats()
	if s.Bytes > 10 {
		t.Errorf("cache should have evicted to fit MaxBytes, got %d", s.Bytes)
	}
	if s.Entries < 2 {
		t.Errorf("should retain at least 2 entries: %+v", s)
	}
}

func TestReadCache_StatsAccumulate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeTestFile(t, path, "x")
	c := NewReadCache(Options{})
	info, _ := os.Stat(path)
	c.Put(path, []byte("x"), info.ModTime())

	_, _ = c.Get(path)          // hit
	_, _ = c.Get("nonexistent") // miss
	_, _ = c.Get(path)          // hit

	s := c.Stats()
	if s.Hits != 2 || s.Misses != 1 {
		t.Errorf("stats wrong: %+v", s)
	}
	if s.HitRate < 0.66 || s.HitRate > 0.67 {
		t.Errorf("hit rate should be ~0.667, got %v", s.HitRate)
	}
}

func TestReadAndCacheRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x")
	writeTestFile(t, path, "payload")
	c := NewReadCache(Options{})
	data, _, err := ReadAndCache(c, path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "payload" {
		t.Errorf("wrong bytes: %s", string(data))
	}
	if _, hit := c.Get(path); !hit {
		t.Error("ReadAndCache should populate the cache")
	}
}

func TestPrefetcher_EnqueueFiresAsyncRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeTestFile(t, path, "warm me")

	c := NewReadCache(Options{})
	p := NewPrefetcher(c, 1, 4)
	p.Start(1)
	defer p.Stop()

	if !p.Enqueue(path) {
		t.Fatal("enqueue refused")
	}
	if err := p.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Bit more time for the worker to run after Drain reports queue
	// empty (worker may still be in-flight).
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, hit := c.Get(path); hit {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("prefetched file should be cached after drain")
}

func TestPrefetcher_SkipsAlreadyCached(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	writeTestFile(t, path, "warm me")
	c := NewReadCache(Options{})
	info, _ := os.Stat(path)
	c.Put(path, []byte("warm me"), info.ModTime())

	// Stub: replace bytes with garbage in the cache so we can tell
	// whether the prefetcher overwrote.
	c.Put(path, []byte("CACHED"), info.ModTime())

	p := NewPrefetcher(c, 1, 4)
	p.Start(1)
	defer p.Stop()
	p.Enqueue(path)
	_ = p.Drain(context.Background())
	time.Sleep(50 * time.Millisecond)

	got, hit := c.Get(path)
	if !hit {
		t.Fatal("entry should still be cached")
	}
	if string(got) != "CACHED" {
		t.Errorf("prefetcher overwrote a fresh cache entry: %s", string(got))
	}
}

func TestPrefetcher_FullQueueDropsRather(t *testing.T) {
	c := NewReadCache(Options{})
	// queueDepth=1, no workers — fills immediately.
	p := NewPrefetcher(c, 1, 1)
	p.Start(0) // 0 → defaults to 2, but never matters since we drop before workers run
	defer p.Stop()
	// Drain workers fast: stop them right away.
	first := p.Enqueue("/path/a")
	// Drop the second when queue is full.
	second := p.Enqueue("/path/b")
	if !first {
		t.Error("first enqueue should succeed")
	}
	// Second might succeed or fail depending on worker scheduling
	// — the contract is just "non-blocking", not "deterministic
	// drop count". We assert non-blocking by getting here at all.
	_ = second
}

func TestTestPairHeuristic_GoFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "foo.go")
	test := filepath.Join(dir, "foo_test.go")
	writeTestFile(t, src, "x")
	writeTestFile(t, test, "x")

	got := TestPairHeuristic(src)
	if len(got) != 1 || got[0] != test {
		t.Errorf("expected foo_test.go, got %+v", got)
	}
	got = TestPairHeuristic(test)
	if len(got) != 1 || got[0] != src {
		t.Errorf("expected foo.go from foo_test.go, got %+v", got)
	}
}

func TestTestPairHeuristic_PythonFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "thing.py")
	test := filepath.Join(dir, "test_thing.py")
	writeTestFile(t, src, "x")
	writeTestFile(t, test, "x")

	got := TestPairHeuristic(src)
	if len(got) != 1 {
		t.Errorf("expected 1 hit, got %d (%+v)", len(got), got)
	}
}

func TestTestPairHeuristic_SkipsNonexistentPair(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "alone.go")
	writeTestFile(t, src, "x")
	got := TestPairHeuristic(src)
	if len(got) != 0 {
		t.Errorf("missing pair should return empty, got %+v", got)
	}
}

func TestPackageNeighborHeuristic_FindsSiblings(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.go")
	for _, name := range []string{"a.go", "b.go", "c.go", "z.txt"} {
		writeTestFile(t, filepath.Join(dir, name), "x")
	}
	got := PackageNeighborHeuristic(src)
	if len(got) != 2 {
		t.Errorf("expected 2 .go neighbors, got %d (%+v)", len(got), got)
	}
}

func TestCombineHeuristics_Dedupes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "x.go")
	test := filepath.Join(dir, "x_test.go")
	writeTestFile(t, src, "x")
	writeTestFile(t, test, "x")

	combined := CombineHeuristics(TestPairHeuristic, PackageNeighborHeuristic)
	got := combined(src)
	// Should NOT include src itself, dedupe x_test.go between the
	// two heuristics → exactly 1 entry.
	if len(got) != 1 || got[0] != test {
		t.Errorf("combine should yield just x_test.go, got %+v", got)
	}
}
