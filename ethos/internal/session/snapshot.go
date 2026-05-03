package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"time"

	"github.com/dgraph-io/badger/v4"
)

type snapshotData struct {
	Version int             `json:"version"`
	Created time.Time       `json:"created"`
	Entries []snapshotEntry `json:"entries"`
}

type snapshotEntry struct {
	Key   string `json:"key"`
	Value []byte `json:"value"`
}

type SnapshotManager struct {
	store        *BadgerStore
	dir          string
	maxSnapshots int
}

var snapshotPattern = regexp.MustCompile(`^snapshot-\d{4}-\d{2}-\d{2}-\d{6}-\d{4}\.json$`)

func NewSnapshotManager(store *BadgerStore, dir string, maxSnapshots int) *SnapshotManager {
	return &SnapshotManager{
		store:        store,
		dir:          dir,
		maxSnapshots: maxSnapshots,
	}
}

func (sm *SnapshotManager) CreateSnapshot(ctx context.Context) (string, error) {
	if err := os.MkdirAll(sm.dir, 0o755); err != nil {
		return "", fmt.Errorf("snapshot: creating dir: %w", err)
	}

	entries, err := sm.dumpAll()
	if err != nil {
		return "", fmt.Errorf("snapshot: dumping db: %w", err)
	}

	data := snapshotData{
		Version: 1,
		Created: time.Now().UTC(),
		Entries: entries,
	}

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", fmt.Errorf("snapshot: marshaling: %w", err)
	}

	filename := fmt.Sprintf("snapshot-%s-%04d.json", data.Created.Format("2006-01-02-150405"), data.Created.Nanosecond()/100000)
	path := sm.dir + "/" + filename

	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return "", fmt.Errorf("snapshot: writing file: %w", err)
	}

	if err := sm.PruneOld(); err != nil {
		return "", fmt.Errorf("snapshot: pruning: %w", err)
	}

	return path, nil
}

func (sm *SnapshotManager) RestoreFromSnapshot(ctx context.Context, snapshotPath string) error {
	raw, err := os.ReadFile(snapshotPath)
	if err != nil {
		return fmt.Errorf("snapshot: reading file: %w", err)
	}

	var data snapshotData
	if err := json.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("snapshot: unmarshaling: %w", err)
	}

	if data.Version != 1 {
		return fmt.Errorf("snapshot: unsupported version %d", data.Version)
	}

	batch := sm.store.db.NewWriteBatch()
	defer batch.Cancel()

	for _, entry := range data.Entries {
		if err := batch.Set([]byte(entry.Key), entry.Value); err != nil {
			return fmt.Errorf("snapshot: restoring key %s: %w", entry.Key, err)
		}
	}

	if err := batch.Flush(); err != nil {
		return fmt.Errorf("snapshot: flushing batch: %w", err)
	}

	return nil
}

func (sm *SnapshotManager) ListSnapshots() ([]string, error) {
	entries, err := os.ReadDir(sm.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("snapshot: reading dir: %w", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !snapshotPattern.MatchString(e.Name()) {
			continue
		}
		names = append(names, e.Name())
	}

	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	return names, nil
}

func (sm *SnapshotManager) PruneOld() error {
	names, err := sm.ListSnapshots()
	if err != nil {
		return err
	}

	if len(names) <= sm.maxSnapshots {
		return nil
	}

	for _, name := range names[sm.maxSnapshots:] {
		path := sm.dir + "/" + name
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("snapshot: removing %s: %w", name, err)
		}
	}

	return nil
}

func (sm *SnapshotManager) dumpAll() ([]snapshotEntry, error) {
	var entries []snapshotEntry

	err := sm.store.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = true
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			key := make([]byte, len(item.Key()))
			copy(key, item.Key())

			val, err := item.ValueCopy(nil)
			if err != nil {
				return fmt.Errorf("reading value for key %s: %w", string(key), err)
			}

			entries = append(entries, snapshotEntry{Key: string(key), Value: val})
		}
		return nil
	})

	return entries, err
}
