package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type BadgerStore struct {
	db *badger.DB
}

func NewBadgerStore(dir string) (*BadgerStore, error) {
	opts := badger.DefaultOptions(dir).
		WithNumVersionsToKeep(1).
		WithNumGoroutines(8).
		WithLoggingLevel(badger.ERROR)

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("session: opening badger db: %w", err)
	}

	log.Info().Str("dir", dir).Msg("session store opened")
	return &BadgerStore{db: db}, nil
}

func (s *BadgerStore) Close() error {
	log.Info().Msg("session store closing")
	return s.db.Close()
}

func sessionKey(id string) []byte {
	return []byte("session:" + id)
}

func folderIndexKey(folder, id string) []byte {
	return []byte(fmt.Sprintf("idx:folder:%s:%s", hashFolder(folder), id))
}

func parentIndexKey(parentID, id string) []byte {
	return []byte(fmt.Sprintf("idx:parent:%s:%s", parentID, id))
}

func statusIndexKey(status, id string) []byte {
	return []byte(fmt.Sprintf("idx:status:%s:%s", status, id))
}

func hashFolder(folder string) string {
	h := sha256.Sum256([]byte(folder))
	return fmt.Sprintf("%x", h)
}

func (s *BadgerStore) Create(ctx context.Context, session *Session) error {
	if session.ID == "" {
		session.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	session.CreatedAt = now
	session.UpdatedAt = now
	if session.Status == "" {
		session.Status = "active"
	}

	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("session: marshaling session: %w", err)
	}

	err = s.db.Update(func(txn *badger.Txn) error {
		_, err := txn.Get(sessionKey(session.ID))
		if err == nil {
			return ErrExists
		}
		if err != badger.ErrKeyNotFound {
			return fmt.Errorf("session: checking existence: %w", err)
		}

		if err := txn.Set(sessionKey(session.ID), data); err != nil {
			return fmt.Errorf("session: writing session: %w", err)
		}
		if err := txn.Set(folderIndexKey(session.Folder, session.ID), nil); err != nil {
			return fmt.Errorf("session: writing folder index: %w", err)
		}
		if err := txn.Set(statusIndexKey(session.Status, session.ID), nil); err != nil {
			return fmt.Errorf("session: writing status index: %w", err)
		}
		if session.ParentID != "" {
			if err := txn.Set(parentIndexKey(session.ParentID, session.ID), nil); err != nil {
				return fmt.Errorf("session: writing parent index: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("session: create: %w", err)
	}

	log.Debug().Str("id", session.ID).Str("folder", session.Folder).Msg("session created")
	return nil
}

func (s *BadgerStore) Load(ctx context.Context, id string) (*Session, error) {
	var session Session

	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(sessionKey(id))
		if err == badger.ErrKeyNotFound {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("session: getting session: %w", err)
		}

		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &session)
		})
	})
	if err != nil {
		return nil, fmt.Errorf("session: load %s: %w", id, err)
	}

	return &session, nil
}

func (s *BadgerStore) Save(ctx context.Context, session *Session) error {
	session.UpdatedAt = time.Now().UTC()

	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("session: marshaling session: %w", err)
	}

	err = s.db.Update(func(txn *badger.Txn) error {
		var oldStatus string
		var oldFolder string

		oldItem, err := txn.Get(sessionKey(session.ID))
		if err == badger.ErrKeyNotFound {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("session: loading old session: %w", err)
		}

		err = oldItem.Value(func(val []byte) error {
			var old Session
			if err := json.Unmarshal(val, &old); err != nil {
				return err
			}
			oldFolder = old.Folder
			oldStatus = old.Status
			return nil
		})
		if err != nil {
			return fmt.Errorf("session: unmarshaling old session: %w", err)
		}

		if err := txn.Set(sessionKey(session.ID), data); err != nil {
			return fmt.Errorf("session: writing session: %w", err)
		}

		if oldFolder != session.Folder {
			txn.Delete(folderIndexKey(oldFolder, session.ID))
			if err := txn.Set(folderIndexKey(session.Folder, session.ID), nil); err != nil {
				return fmt.Errorf("session: updating folder index: %w", err)
			}
		}

		if oldStatus != session.Status {
			txn.Delete(statusIndexKey(oldStatus, session.ID))
			if err := txn.Set(statusIndexKey(session.Status, session.ID), nil); err != nil {
				return fmt.Errorf("session: updating status index: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("session: save %s: %w", session.ID, err)
	}

	log.Debug().Str("id", session.ID).Msg("session saved")
	return nil
}

func (s *BadgerStore) Delete(ctx context.Context, id string) error {
	err := s.db.Update(func(txn *badger.Txn) error {
		item, err := txn.Get(sessionKey(id))
		if err == badger.ErrKeyNotFound {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("session: loading session for delete: %w", err)
		}

		var session Session
		err = item.Value(func(val []byte) error {
			return json.Unmarshal(val, &session)
		})
		if err != nil {
			return fmt.Errorf("session: unmarshaling session for delete: %w", err)
		}

		txn.Delete(sessionKey(id))
		txn.Delete(folderIndexKey(session.Folder, id))
		txn.Delete(statusIndexKey(session.Status, id))
		if session.ParentID != "" {
			txn.Delete(parentIndexKey(session.ParentID, id))
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("session: delete %s: %w", id, err)
	}

	log.Debug().Str("id", id).Msg("session deleted")
	return nil
}

func (s *BadgerStore) List(ctx context.Context, opts ListOptions) ([]*Session, error) {
	var ids []string

	err := s.db.View(func(txn *badger.Txn) error {
		switch {
		case opts.ParentID != "":
			prefix := []byte(fmt.Sprintf("idx:parent:%s:", opts.ParentID))
			return iteratePrefix(txn, prefix, func(id string) error {
				ids = append(ids, id)
				return nil
			})
		case opts.Folder != "":
			prefix := []byte(fmt.Sprintf("idx:folder:%s:", hashFolder(opts.Folder)))
			return iteratePrefix(txn, prefix, func(id string) error {
				ids = append(ids, id)
				return nil
			})
		case opts.Status != "":
			prefix := []byte(fmt.Sprintf("idx:status:%s:", opts.Status))
			return iteratePrefix(txn, prefix, func(id string) error {
				ids = append(ids, id)
				return nil
			})
		default:
			prefix := []byte("session:")
			return iteratePrefix(txn, prefix, func(id string) error {
				ids = append(ids, id)
				return nil
			})
		}
	})
	if err != nil {
		return nil, fmt.Errorf("session: list: %w", err)
	}

	sessions := make([]*Session, 0, len(ids))
	for _, id := range ids {
		sess, err := s.Load(ctx, id)
		if err != nil {
			if err == ErrNotFound || isWrapped(err, ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("session: list loading %s: %w", id, err)
		}
		sessions = append(sessions, sess)
	}

	sessions = filterSessions(sessions, opts)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	if opts.Offset > 0 {
		if opts.Offset >= len(sessions) {
			sessions = nil
		} else {
			sessions = sessions[opts.Offset:]
		}
	}

	if opts.Limit > 0 && len(sessions) > opts.Limit {
		sessions = sessions[:opts.Limit]
	}

	return sessions, nil
}

func iteratePrefix(txn *badger.Txn, prefix []byte, fn func(id string) error) error {
	it := txn.NewIterator(badger.DefaultIteratorOptions)
	defer it.Close()

	for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
		key := it.Item().Key()
		parts := splitKey(string(key))
		var id string
		if len(parts) >= 2 {
			id = parts[len(parts)-1]
		}
		if id == "" {
			continue
		}
		if err := fn(id); err != nil {
			return err
		}
	}
	return nil
}

func splitKey(key string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			parts = append(parts, key[start:i])
			start = i + 1
		}
	}
	parts = append(parts, key[start:])
	return parts
}

func filterSessions(sessions []*Session, opts ListOptions) []*Session {
	result := make([]*Session, 0, len(sessions))
	for _, s := range sessions {
		if opts.Folder != "" && s.Folder != opts.Folder {
			continue
		}
		if opts.Status != "" && s.Status != opts.Status {
			continue
		}
		if opts.ParentID != "" && s.ParentID != opts.ParentID {
			continue
		}
		if !opts.After.IsZero() && !s.UpdatedAt.After(opts.After) {
			continue
		}
		result = append(result, s)
	}
	return result
}

func isWrapped(err, target error) bool {
	return err != nil && target != nil && err.Error() != "" && target.Error() != "" &&
		len(err.Error()) >= len(target.Error()) &&
		err.Error()[len(err.Error())-len(target.Error()):] == target.Error()
}

var _ io.Closer = (*BadgerStore)(nil)
