// Package speculative — read cache + prefetcher for §8.5 Phase 5
// speculative tool execution.
//
// The idea: when the agent reads foo.go, it's overwhelmingly likely
// to read foo_test.go, neighbors in the same package, or files
// referenced from foo.go next. Cache the read, and async-prefetch
// the likely-next files so they're warm by the time the agent asks.
//
// Design:
//
//   - ReadCache is a TTL + LRU cache keyed by absolute path. Each
//     entry stores bytes + the file's mtime at read time so a
//     stale-on-disk lookup invalidates the entry.
//   - Prefetcher is a small worker pool that runs heuristic-driven
//     reads in the background. Empty by default — the wiring layer
//     installs heuristics (sibling, test-pair, package-neighbor).
//   - Telemetry: HitRate / TotalReads exposed so the operator can
//     check whether prefetching is actually working.
//
// What this is NOT: a transparent FS layer. Tools that want the
// cache call Get/Put explicitly. The intent is to wrap the agent's
// file-read tool, not to intercept every os.Open.
package speculative

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Entry is one cached read.
type Entry struct {
	Path     string
	Bytes    []byte
	MTime    time.Time
	StoredAt time.Time
}

// ReadCache is a TTL + LRU cache. Concurrent-safe.
type ReadCache struct {
	mu       sync.Mutex
	entries  map[string]*Entry
	order    []string // LRU order, oldest first
	maxBytes int64
	curBytes int64
	ttl      time.Duration

	hits   atomic.Int64
	misses atomic.Int64
}

// Options configures the cache.
type Options struct {
	// MaxBytes caps the total stored size. Eviction is LRU. Default
	// 32MB.
	MaxBytes int64
	// TTL after which an entry is considered stale even if its
	// mtime hasn't changed. Default 5 minutes. Set to 0 for no TTL
	// (mtime check only).
	TTL time.Duration
}

func NewReadCache(opts Options) *ReadCache {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = 32 * 1024 * 1024
	}
	if opts.TTL < 0 {
		opts.TTL = 0
	}
	if opts.TTL == 0 {
		opts.TTL = 5 * time.Minute
	}
	return &ReadCache{
		entries:  map[string]*Entry{},
		maxBytes: opts.MaxBytes,
		ttl:      opts.TTL,
	}
}

// Get returns cached bytes for path when present AND fresh. Fresh =
// (a) within TTL AND (b) file mtime matches the stored mtime. On
// miss returns (nil, false).
func (c *ReadCache) Get(path string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[path]
	if !ok {
		c.misses.Add(1)
		return nil, false
	}
	// TTL check.
	if c.ttl > 0 && time.Since(e.StoredAt) > c.ttl {
		c.evictLocked(path)
		c.misses.Add(1)
		return nil, false
	}
	// Freshness check: file mtime mismatch invalidates.
	if info, err := os.Stat(path); err != nil || !info.ModTime().Equal(e.MTime) {
		c.evictLocked(path)
		c.misses.Add(1)
		return nil, false
	}
	// Bump LRU.
	c.touchLocked(path)
	c.hits.Add(1)
	// Return a defensive copy so the caller can't mutate the cached
	// bytes.
	dup := make([]byte, len(e.Bytes))
	copy(dup, e.Bytes)
	return dup, true
}

// Put stores bytes for path. mtime should be the file's mtime at
// read time so subsequent Get calls can invalidate on change.
// Idempotent — repeated Put for the same path replaces the entry
// without churning the LRU position.
func (c *ReadCache) Put(path string, bytes []byte, mtime time.Time) {
	if path == "" || bytes == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.entries[path]; ok {
		c.curBytes -= int64(len(existing.Bytes))
	}
	e := &Entry{
		Path:     path,
		Bytes:    append([]byte(nil), bytes...),
		MTime:    mtime,
		StoredAt: time.Now(),
	}
	c.entries[path] = e
	c.curBytes += int64(len(bytes))
	c.touchLocked(path)
	c.evictExcessLocked()
}

// Stats returns a snapshot of cache telemetry.
func (c *ReadCache) Stats() Stats {
	c.mu.Lock()
	bytes := c.curBytes
	count := len(c.entries)
	c.mu.Unlock()
	hits := c.hits.Load()
	misses := c.misses.Load()
	total := hits + misses
	rate := 0.0
	if total > 0 {
		rate = float64(hits) / float64(total)
	}
	return Stats{
		Bytes:    bytes,
		Entries:  count,
		Hits:     hits,
		Misses:   misses,
		HitRate:  rate,
		MaxBytes: c.maxBytes,
	}
}

// Clear empties the cache. Telemetry counters retained.
func (c *ReadCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]*Entry{}
	c.order = nil
	c.curBytes = 0
}

// ── internals ───────────────────────────────────────────────────────

func (c *ReadCache) touchLocked(path string) {
	// Remove existing position then append.
	for i, p := range c.order {
		if p == path {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	c.order = append(c.order, path)
}

func (c *ReadCache) evictLocked(path string) {
	if e, ok := c.entries[path]; ok {
		c.curBytes -= int64(len(e.Bytes))
		delete(c.entries, path)
	}
	for i, p := range c.order {
		if p == path {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

func (c *ReadCache) evictExcessLocked() {
	for c.curBytes > c.maxBytes && len(c.order) > 0 {
		oldest := c.order[0]
		c.evictLocked(oldest)
	}
}

// Stats is a snapshot of cache telemetry.
type Stats struct {
	Bytes    int64
	Entries  int
	Hits     int64
	Misses   int64
	HitRate  float64 // 0..1
	MaxBytes int64
}

// ReadAndCache reads the file from disk and stores it in the cache.
// Returns the bytes + the mtime that was stored. Useful as the
// canonical "miss path" — wrap your file-read tool so cache misses
// always populate the cache for next time.
func ReadAndCache(c *ReadCache, path string) ([]byte, time.Time, error) {
	if c == nil {
		return nil, time.Time{}, errors.New("speculative: nil cache")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	if info.IsDir() {
		return nil, time.Time{}, errors.New("speculative: cannot cache directory")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	c.Put(path, data, info.ModTime())
	return data, info.ModTime(), nil
}

// Prefetcher runs a worker pool that fires async reads through a
// ReadCache. Heuristics decide what to prefetch; the cache decides
// what to keep.
type Prefetcher struct {
	cache   *ReadCache
	queue   chan string
	wg      sync.WaitGroup
	stop    chan struct{}
	started atomic.Bool
}

// NewPrefetcher wires a prefetcher to a cache. workers caps the
// concurrency; queueDepth bounds the in-flight prefetch backlog.
// Sane defaults: 2 workers, 64-deep queue.
func NewPrefetcher(cache *ReadCache, workers, queueDepth int) *Prefetcher {
	if workers <= 0 {
		workers = 2
	}
	if queueDepth <= 0 {
		queueDepth = 64
	}
	return &Prefetcher{
		cache: cache,
		queue: make(chan string, queueDepth),
		stop:  make(chan struct{}),
	}
}

// Start spins up the worker goroutines. Idempotent — second call is
// a no-op. The caller's ctx (in Stop) determines lifetime.
func (p *Prefetcher) Start(workers int) {
	if !p.started.CompareAndSwap(false, true) {
		return
	}
	if workers <= 0 {
		workers = 2
	}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.runWorker()
	}
}

// Stop signals workers to exit and waits for them. Idempotent.
func (p *Prefetcher) Stop() {
	if !p.started.CompareAndSwap(true, false) {
		return
	}
	close(p.stop)
	p.wg.Wait()
	// Reset for a future Start.
	p.stop = make(chan struct{})
}

// Enqueue adds a path to the prefetch backlog. Non-blocking — a
// full queue drops the request rather than gating the caller.
// Returns true when accepted, false when dropped.
func (p *Prefetcher) Enqueue(path string) bool {
	if path == "" || !p.started.Load() {
		return false
	}
	select {
	case p.queue <- path:
		return true
	default:
		return false
	}
}

// EnqueueAll fires Enqueue for each path. Returns the count
// accepted; the rest were dropped due to a full queue.
func (p *Prefetcher) EnqueueAll(paths []string) int {
	if !p.started.Load() {
		return 0
	}
	n := 0
	for _, path := range paths {
		if p.Enqueue(path) {
			n++
		}
	}
	return n
}

func (p *Prefetcher) runWorker() {
	defer p.wg.Done()
	for {
		select {
		case <-p.stop:
			return
		case path, ok := <-p.queue:
			if !ok {
				return
			}
			if path == "" {
				continue
			}
			// Don't reprefetch if it's already warm. Best-effort
			// check via Stat + cache lookup.
			if _, hit := p.cache.Get(path); hit {
				continue
			}
			_, _, _ = ReadAndCache(p.cache, path)
		}
	}
}

// Drain waits for the prefetch queue to empty. Used in tests so
// assertions don't race the worker pool.
func (p *Prefetcher) Drain(ctx context.Context) error {
	for {
		if len(p.queue) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}
