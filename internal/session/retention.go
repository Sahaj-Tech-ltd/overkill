package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"time"
	"unicode/utf8"
)

type atomicRetentionStore interface {
	enforceMaxSessions(context.Context, int) (int, error)
}
type retentionCandidate struct {
	ID        string
	UpdatedAt time.Time
}

// EnforceMax retains the newest max initially top-level sessions. Sub-sessions
// are excluded from the snapshot and never selected for pruning; deleting a
// root retains the store's existing family promotion policy. Nonpositive max
// means unlimited. Concurrent new roots are considered on a later pass.
//
// PostgreSQL performs selection/deletion atomically. Generic stores are scanned
// completely before deletion, with bounded pages and a private ID/version disk
// spool. Their interface cannot make concurrent writes atomic: changed or
// missing candidates are skipped and a later pass may still be necessary.
func EnforceMax(ctx context.Context, store Store, max int) (int, error) {
	if store == nil || max <= 0 {
		return 0, nil
	}
	if native, ok := store.(atomicRetentionStore); ok {
		return native.enforceMaxSessions(ctx, max)
	}
	// Scan before mutating. Deleting while OFFSET paging would skip records, and
	// root deletion may promote children which must not enter this snapshot.
	spool, err := os.CreateTemp("", "overkill-retention-*")
	if err != nil {
		return 0, fmt.Errorf("session retention snapshot: %w", err)
	}
	defer os.Remove(spool.Name())
	defer spool.Close()
	writer := bufio.NewWriter(spool)
	encoder := json.NewEncoder(writer)
	const pageSize = 64
	roots, offset := 0, 0
	var previousTime time.Time
	previousID := ""
	havePrevious := false
	for {
		if err = ctx.Err(); err != nil {
			return 0, err
		}
		page, e := store.List(ctx, ListOptions{OldestFirst: true, Limit: pageSize, Offset: offset})
		if e != nil {
			return 0, e
		}
		if len(page) > pageSize {
			return 0, fmt.Errorf("session retention: store ignored page bound")
		}
		for _, item := range page {
			if item == nil {
				continue
			}
			if item.ID == "" || !utf8.ValidString(item.ID) {
				return 0, fmt.Errorf("session retention: invalid candidate identity")
			}
			if havePrevious && (item.UpdatedAt.Before(previousTime) || item.UpdatedAt.Equal(previousTime) && item.ID <= previousID) {
				return 0, fmt.Errorf("session retention: nonmonotonic or duplicate pagination")
			}
			previousTime, previousID, havePrevious = item.UpdatedAt, item.ID, true
			if item.ParentID != "" {
				continue
			}
			if roots == math.MaxInt {
				return 0, fmt.Errorf("session retention count overflow")
			}
			roots++
			if err = encoder.Encode(retentionCandidate{ID: item.ID, UpdatedAt: item.UpdatedAt}); err != nil {
				return 0, err
			}
		}
		if len(page) < pageSize {
			break
		}
		if offset > math.MaxInt-len(page) {
			return 0, fmt.Errorf("session retention offset overflow")
		}
		offset += len(page)
	}
	if roots <= max {
		return 0, nil
	}
	if err = writer.Flush(); err != nil {
		return 0, err
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	decoder := json.NewDecoder(bufio.NewReader(spool))
	deleted := 0
	for range roots - max {
		if err = ctx.Err(); err != nil {
			return deleted, err
		}
		var candidate retentionCandidate
		if err = decoder.Decode(&candidate); err != nil {
			return deleted, err
		}
		current, e := store.Load(ctx, candidate.ID)
		if errors.Is(e, ErrNotFound) {
			continue
		}
		if e != nil {
			return deleted, e
		}
		if current == nil || current.ParentID != "" || !current.UpdatedAt.Equal(candidate.UpdatedAt) {
			continue
		}
		if e = store.Delete(ctx, candidate.ID); errors.Is(e, ErrNotFound) {
			continue
		} else if e != nil {
			return deleted, e
		}
		deleted++
	}
	return deleted, nil
}
