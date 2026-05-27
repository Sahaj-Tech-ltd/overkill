package learning

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/rs/zerolog/log"
)

// DefaultMaxCorrections is the maximum number of corrections to store
// before evicting the oldest (LRU).
const DefaultMaxCorrections = 1000

// Store persists corrections using BadgerDB and provides retrieval
// via keyword overlap matching.
type Store struct {
	mu       sync.RWMutex
	db       *badger.DB
	maxItems int
}

// NewStore opens or creates a BadgerDB-backed correction store at dir.
func NewStore(dir string, maxItems int) (*Store, error) {
	if maxItems <= 0 {
		maxItems = DefaultMaxCorrections
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("learning: creating corrections dir %s: %w", dir, err)
	}

	opts := badger.DefaultOptions(dir).
		WithNumVersionsToKeep(1).
		WithNumGoroutines(4).
		WithLoggingLevel(badger.ERROR)

	db, err := openWithTimeout(opts, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("learning: opening badger db: %w", err)
	}

	log.Info().Str("dir", dir).Int("max_items", maxItems).Msg("corrections store opened")

	return &Store{
		db:       db,
		maxItems: maxItems,
	}, nil
}

// Close closes the underlying BadgerDB.
func (s *Store) Close() error {
	log.Info().Msg("corrections store closing")
	return s.db.Close()
}

// DB returns the underlying BadgerDB, useful for direct access in tests.
func (s *Store) DB() *badger.DB {
	return s.db
}

func correctionKey(hash string) []byte {
	return []byte("correction:" + hash)
}

// metaKey stores the total count so we can check if eviction is needed
// without an expensive full scan.
const metaCountKey = "meta:count"

// Save stores a correction. If a correction with the same key already
// exists, it is overwritten (deduplication). Enforces maxItems by
// evicting the oldest correction when full.
func (s *Store) Save(c *Correction) error {
	if c == nil {
		return fmt.Errorf("learning: cannot save nil correction")
	}
	key := c.Key()
	data, err := c.Marshal()
	if err != nil {
		return fmt.Errorf("learning: marshaling correction: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Update(func(txn *badger.Txn) error {
		// Check if this key already exists — if so, it's an update (no count change)
		_, getErr := txn.Get(correctionKey(key))
		exists := getErr == nil

		if !exists {
			// Check current count and evict if at capacity
			count, err := s.getCount(txn)
			if err != nil {
				return fmt.Errorf("learning: reading count: %w", err)
			}
			if count >= s.maxItems {
				if err := s.evictOldest(txn, count); err != nil {
					return fmt.Errorf("learning: evicting oldest: %w", err)
				}
			}
			// Increment count (only for new entries)
			newCount := count + 1
			if count >= s.maxItems {
				newCount = s.maxItems // eviction removed one, we add one
			}
			if err := txn.Set([]byte(metaCountKey), encodeUint64(uint64(newCount))); err != nil {
				return fmt.Errorf("learning: updating count: %w", err)
			}
		}

		// Set the correction data
		if err := txn.Set(correctionKey(key), data); err != nil {
			return fmt.Errorf("learning: writing correction: %w", err)
		}

		return nil
	})
}

// FindCorrections returns up to topK corrections most relevant to the query.
// It scans all stored corrections and scores them via keyword overlap (TF).
func (s *Store) FindCorrections(query string, topK int) ([]*Correction, error) {
	if topK <= 0 {
		topK = 3
	}

	queryTokens := tokenize(query)

	type scored struct {
		corr *Correction
		score float64
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var candidates []scored

	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		prefix := []byte("correction:")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			var c *Correction
			err := it.Item().Value(func(val []byte) error {
				var err error
				c, err = UnmarshalCorrection(val)
				return err
			})
			if err != nil {
				continue
			}
			score := c.MatchScore(queryTokens)
			if score > 0 {
				candidates = append(candidates, scored{corr: c, score: score})
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("learning: finding corrections: %w", err)
	}

	// Sort by score descending (simple bubble for small N; topK is typically 3)
	// Use insertion sort for the top-K
	for i := 1; i < len(candidates); i++ {
		j := i
		for j > 0 && candidates[j].score > candidates[j-1].score {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
			j--
		}
	}

	if len(candidates) > topK {
		candidates = candidates[:topK]
	}

	result := make([]*Correction, len(candidates))
	for i, sc := range candidates {
		result[i] = sc.corr
	}
	return result, nil
}

// Count returns the current number of stored corrections.
func (s *Store) Count() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.View(func(txn *badger.Txn) error {
		c, err := s.getCount(txn)
		if err != nil {
			return err
		}
		count = c
		return nil
	})
	return count, err
}

// getCount reads the correction count from the meta key. Must be called
// within a transaction.
func (s *Store) getCount(txn *badger.Txn) (int, error) {
	item, err := txn.Get([]byte(metaCountKey))
	if err == badger.ErrKeyNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var count int
	err = item.Value(func(val []byte) error {
		count = int(decodeUint64(val))
		return nil
	})
	return count, err
}

// evictOldest removes the correction with the smallest timestamp (oldest).
// Must be called within a transaction. Does not update the count meta-key;
// the caller is responsible for that.
func (s *Store) evictOldest(txn *badger.Txn, currentCount int) error {
	prefix := []byte("correction:")
	it := txn.NewIterator(badger.DefaultIteratorOptions)
	defer it.Close()

	var oldestKey []byte
	var oldestTime int64 = 1<<63 - 1 // max int64

	for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
		c, err := UnmarshalCorrection(valFromItem(it.Item()))
		if err != nil {
			continue
		}
		if c.Timestamp < oldestTime {
			oldestTime = c.Timestamp
			oldestKey = it.Item().KeyCopy(nil)
		}
	}

	if oldestKey == nil {
		// Fallback: delete the first one we see
		it.Seek(prefix)
		if it.ValidForPrefix(prefix) {
			oldestKey = it.Item().KeyCopy(nil)
		}
	}
	if oldestKey != nil {
		return txn.Delete(oldestKey)
	}
	return nil
}

// valFromItem extracts the value from a badger Item.
func valFromItem(item *badger.Item) []byte {
	var val []byte
	_ = item.Value(func(v []byte) error {
		val = v
		return nil
	})
	return val
}

// encodeUint64 encodes a uint64 to a fixed-width big-endian byte slice.
func encodeUint64(v uint64) []byte {
	b := make([]byte, 8)
	b[0] = byte(v >> 56)
	b[1] = byte(v >> 48)
	b[2] = byte(v >> 40)
	b[3] = byte(v >> 32)
	b[4] = byte(v >> 24)
	b[5] = byte(v >> 16)
	b[6] = byte(v >> 8)
	b[7] = byte(v)
	return b
}

// decodeUint64 decodes a big-endian uint64 from a byte slice.
func decodeUint64(b []byte) uint64 {
	if len(b) < 8 {
		return 0
	}
	return uint64(b[0])<<56 |
		uint64(b[1])<<48 |
		uint64(b[2])<<40 |
		uint64(b[3])<<32 |
		uint64(b[4])<<24 |
		uint64(b[5])<<16 |
		uint64(b[6])<<8 |
		uint64(b[7])
}

// openWithTimeout wraps badger.Open with a deadline so a stale LOCK file
// or corrupt value log never hangs the caller indefinitely.
func openWithTimeout(opts badger.Options, timeout time.Duration) (*badger.DB, error) {
	type result struct {
		db  *badger.DB
		err error
	}
	ch := make(chan result, 1)
	go func() {
		db, err := badger.Open(opts)
		ch <- result{db, err}
	}()
	select {
	case r := <-ch:
		return r.db, r.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("badger.Open(%s) timed out after %v — possible stale LOCK file", filepath.Base(opts.Dir), timeout)
	}
}
