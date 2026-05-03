package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/google/uuid"
)

const (
	dataPrefix       = "memory:"
	sessionIdxPrefix = "memory_idx:session:"
	typeIdxPrefix    = "memory_idx:type:"
	tagIdxPrefix     = "memory_idx:tag:"
)

type BadgerStore struct {
	db *badger.DB
	mu sync.RWMutex
}

func NewBadgerStore(db *badger.DB) *BadgerStore {
	return &BadgerStore{db: db}
}

func memoryKey(id string) []byte {
	return []byte(dataPrefix + id)
}

func sessionIndexKey(sessionID, id string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s", sessionIdxPrefix, sessionID, id))
}

func typeIndexKey(memType MemoryType, ts int64, id string) []byte {
	return []byte(fmt.Sprintf("%s%s:%019d:%s", typeIdxPrefix, memType, ts, id))
}

func tagIndexKey(tag, id string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s", tagIdxPrefix, tag, id))
}

func (s *BadgerStore) Store(ctx context.Context, memory *Memory) error {
	if memory.ID == "" {
		memory.ID = uuid.New().String()
	}
	if memory.Timestamp.IsZero() {
		memory.Timestamp = timeNow()
	}
	if memory.Tags == nil {
		memory.Tags = []string{}
	}
	if memory.Metadata == nil {
		memory.Metadata = make(map[string]string)
	}

	data, err := json.Marshal(memory)
	if err != nil {
		return fmt.Errorf("memory: marshal: %w", err)
	}

	ts := memory.Timestamp.UnixNano()

	err = s.db.Update(func(txn *badger.Txn) error {
		if err := txn.Set(memoryKey(memory.ID), data); err != nil {
			return fmt.Errorf("memory: write data: %w", err)
		}

		if memory.SessionID != "" {
			if err := txn.Set(sessionIndexKey(memory.SessionID, memory.ID), nil); err != nil {
				return fmt.Errorf("memory: write session index: %w", err)
			}
		}

		if err := txn.Set(typeIndexKey(memory.Type, ts, memory.ID), nil); err != nil {
			return fmt.Errorf("memory: write type index: %w", err)
		}

		for _, tag := range memory.Tags {
			if err := txn.Set(tagIndexKey(tag, memory.ID), nil); err != nil {
				return fmt.Errorf("memory: write tag index: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("memory: store: %w", err)
	}

	return nil
}

func (s *BadgerStore) Retrieve(ctx context.Context, query Query) (*SearchResult, error) {
	var memories []Memory

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(dataPrefix)
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			var m Memory
			if err := it.Item().Value(func(val []byte) error {
				return json.Unmarshal(val, &m)
			}); err != nil {
				continue
			}

			if len(query.Types) > 0 && !containsType(query.Types, m.Type) {
				continue
			}

			if len(query.Tags) > 0 && !containsAnyTag(query.Tags, m.Tags) {
				continue
			}

			score := scoreContent(query.Content, m.Content)
			if query.Content != "" && score == 0 {
				continue
			}

			if query.MinRelevance > 0 && score < query.MinRelevance {
				continue
			}

			m.Relevance = score
			memories = append(memories, m)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("memory: retrieve: %w", err)
	}

	sort.Slice(memories, func(i, j int) bool {
		return memories[i].Relevance > memories[j].Relevance
	})

	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}

	total := len(memories)
	if limit < total {
		memories = memories[:limit]
	}

	return &SearchResult{
		Memories: memories,
		Total:    total,
	}, nil
}

func (s *BadgerStore) Delete(ctx context.Context, id string) error {
	err := s.db.Update(func(txn *badger.Txn) error {
		item, err := txn.Get(memoryKey(id))
		if err == badger.ErrKeyNotFound {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("memory: loading for delete: %w", err)
		}

		var m Memory
		if err := item.Value(func(val []byte) error {
			return json.Unmarshal(val, &m)
		}); err != nil {
			return fmt.Errorf("memory: unmarshal for delete: %w", err)
		}

		txn.Delete(memoryKey(id))

		if m.SessionID != "" {
			txn.Delete(sessionIndexKey(m.SessionID, id))
		}

		ts := m.Timestamp.UnixNano()
		txn.Delete(typeIndexKey(m.Type, ts, id))

		for _, tag := range m.Tags {
			txn.Delete(tagIndexKey(tag, id))
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("memory: delete: %w", err)
	}

	return nil
}

func (s *BadgerStore) Get(ctx context.Context, id string) (*Memory, error) {
	var m Memory

	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(memoryKey(id))
		if err == badger.ErrKeyNotFound {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("memory: get: %w", err)
		}

		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &m)
		})
	})
	if err != nil {
		return nil, fmt.Errorf("memory: get %s: %w", id, err)
	}

	return &m, nil
}

func (s *BadgerStore) List(ctx context.Context, opts ListOptions) ([]Memory, error) {
	var ids []string

	err := s.db.View(func(txn *badger.Txn) error {
		switch {
		case opts.SessionID != "":
			prefix := []byte(fmt.Sprintf("%s%s:", sessionIdxPrefix, opts.SessionID))
			return iteratePrefix(txn, prefix, func(id string) error {
				ids = append(ids, id)
				return nil
			})
		case opts.Type != "":
			prefix := []byte(fmt.Sprintf("%s%s:", typeIdxPrefix, string(opts.Type)))
			return iteratePrefix(txn, prefix, func(id string) error {
				ids = append(ids, id)
				return nil
			})
		default:
			prefix := []byte(dataPrefix)
			return iteratePrefixData(txn, prefix, func(id string) error {
				ids = append(ids, id)
				return nil
			})
		}
	})
	if err != nil {
		return nil, fmt.Errorf("memory: list: %w", err)
	}

	memories := make([]Memory, 0, len(ids))
	for _, id := range ids {
		m, err := s.Get(ctx, id)
		if err != nil {
			if err == ErrNotFound {
				continue
			}
			return nil, fmt.Errorf("memory: list loading %s: %w", id, err)
		}
		memories = append(memories, *m)
	}

	sort.Slice(memories, func(i, j int) bool {
		return memories[i].Timestamp.After(memories[j].Timestamp)
	})

	if opts.Offset > 0 {
		if opts.Offset >= len(memories) {
			memories = nil
		} else {
			memories = memories[opts.Offset:]
		}
	}

	if opts.Limit > 0 && len(memories) > opts.Limit {
		memories = memories[:opts.Limit]
	}

	return memories, nil
}

func (s *BadgerStore) Close() error {
	return s.db.Close()
}

func iteratePrefix(txn *badger.Txn, prefix []byte, fn func(id string) error) error {
	opts := badger.DefaultIteratorOptions
	opts.Prefix = prefix
	opts.PrefetchValues = false
	it := txn.NewIterator(opts)
	defer it.Close()

	for it.Rewind(); it.Valid(); it.Next() {
		key := string(it.Item().Key())
		parts := strings.Split(key, ":")
		if len(parts) == 0 {
			continue
		}
		id := parts[len(parts)-1]
		if id == "" {
			continue
		}
		if err := fn(id); err != nil {
			return err
		}
	}
	return nil
}

func iteratePrefixData(txn *badger.Txn, prefix []byte, fn func(id string) error) error {
	opts := badger.DefaultIteratorOptions
	opts.Prefix = prefix
	opts.PrefetchValues = false
	it := txn.NewIterator(opts)
	defer it.Close()

	for it.Rewind(); it.Valid(); it.Next() {
		key := string(it.Item().Key())
		id := strings.TrimPrefix(key, dataPrefix)
		if id == "" {
			continue
		}
		if err := fn(id); err != nil {
			return err
		}
	}
	return nil
}

func scoreContent(query, content string) float64 {
	if query == "" {
		return 0
	}

	queryTerms := tokenize(query)
	contentLower := strings.ToLower(content)

	hits := 0
	for _, term := range queryTerms {
		if strings.Contains(contentLower, term) {
			hits++
		}
	}

	if len(queryTerms) == 0 {
		return 0
	}

	return float64(hits) / float64(len(queryTerms))
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	terms := strings.Fields(s)
	return terms
}

func containsType(types []MemoryType, t MemoryType) bool {
	for _, mt := range types {
		if mt == t {
			return true
		}
	}
	return false
}

var timeNow = func() time.Time {
	return time.Now().UTC()
}

func containsAnyTag(queryTags, memoryTags []string) bool {
	tagSet := make(map[string]struct{}, len(memoryTags))
	for _, t := range memoryTags {
		tagSet[t] = struct{}{}
	}
	for _, t := range queryTags {
		if _, ok := tagSet[t]; ok {
			return true
		}
	}
	return false
}
