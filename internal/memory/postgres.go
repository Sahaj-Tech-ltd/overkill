package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	_ "github.com/lib/pq"
)

// PostgresStore implements Store backed by PostgreSQL.
type PostgresStore struct {
	db          *sql.DB
	retention   time.Duration
	retentionMu sync.RWMutex
	pruneCancel context.CancelFunc
}

// NewPostgresStore returns a Store backed by the given *sql.DB.
// Starts a background goroutine that prunes old memories every hour
// when a retention duration is configured via SetRetention.
func NewPostgresStore(db *sql.DB) (*PostgresStore, error) {
	s := &PostgresStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("memory: migrate: %w", err)
	}
	// Start background pruner (no-op until retention is set).
	pruneCtx, pruneCancel := context.WithCancel(context.Background())
	s.pruneCancel = pruneCancel
	go s.backgroundPrune(pruneCtx)
	return s, nil
}

// SetRetention configures automatic pruning. Memories older than the
// configured duration are deleted by the background goroutine.
// Set to 0 to disable automatic pruning.
func (s *PostgresStore) SetRetention(d time.Duration) {
	s.retentionMu.Lock()
	s.retention = d
	s.retentionMu.Unlock()
}

// Prune deletes memories whose timestamp is before olderThan.
func (s *PostgresStore) Prune(ctx context.Context, olderThan time.Time) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM memory_items WHERE timestamp < $1`, olderThan,
	)
	if err != nil {
		return fmt.Errorf("memory: prune: %w", err)
	}
	n, _ := result.RowsAffected()
	if n > 0 {
		log.Printf("memory: pruned %d memories older than %s", n, olderThan.UTC().Format(time.RFC3339))
	}
	return nil
}

func (s *PostgresStore) backgroundPrune(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.retentionMu.RLock()
			ret := s.retention
			s.retentionMu.RUnlock()
			if ret <= 0 {
				continue
			}
			cutoff := time.Now().UTC().Add(-ret)
			if err := s.Prune(context.Background(), cutoff); err != nil {
				log.Printf("memory: background prune: %v", err)
			}
		}
	}
}

func (s *PostgresStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS memory_items (
			id          TEXT PRIMARY KEY,
			type        TEXT NOT NULL DEFAULT '',
			content     TEXT NOT NULL DEFAULT '',
			content_hash TEXT,
			tags        JSONB NOT NULL DEFAULT '[]',
			session_id  TEXT NOT NULL DEFAULT '',
			timestamp   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			metadata    JSONB NOT NULL DEFAULT '{}'
		)
	`)
	if err != nil {
		return err
	}
	// Ensure content_hash exists for existing tables (migration from pre-dedup).
	if _, err := s.db.Exec(`ALTER TABLE memory_items ADD COLUMN IF NOT EXISTS content_hash TEXT`); err != nil {
		return err
	}
	// Unique constraint on content hash to prevent duplicate entries.
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_items_content_hash ON memory_items (content_hash)`); err != nil {
		return err
	}
	for _, idx := range []string{
		`CREATE INDEX IF NOT EXISTS idx_memory_items_type ON memory_items (type)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_items_session ON memory_items (session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_items_timestamp ON memory_items (timestamp DESC)`,
	} {
		if _, err := s.db.Exec(idx); err != nil {
			return err
		}
	}
	return nil
}

// Store persists a Memory. Deduplicates by content hash (SHA-256):
// when identical content is stored again, the existing row's timestamp
// is bumped to the newer value instead of creating a duplicate row.
// The Store interface is unchanged — callers still pass a Memory and
// receive an error.
func (s *PostgresStore) Store(ctx context.Context, memory *Memory) error {
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

	contentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(memory.Content)))

	// json.Marshal never fails for []string or map[string]string:
	// all values are guaranteed to have valid JSON representations.
	// If this assumption is broken by a type change, the test suite
	// will catch it (Store tests exercise both fields).
	tagsJSON, err := json.Marshal(memory.Tags)
	if err != nil {
		return fmt.Errorf("postgres: marshal tags: %w", err)
	}
	metaJSON, err := json.Marshal(memory.Metadata)
	if err != nil {
		return fmt.Errorf("postgres: marshal metadata: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO memory_items (id, type, content, content_hash, tags, session_id, timestamp, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (content_hash) DO UPDATE SET
			type       = EXCLUDED.type,
			content    = EXCLUDED.content,
			tags       = EXCLUDED.tags,
			session_id = EXCLUDED.session_id,
			timestamp  = GREATEST(memory_items.timestamp, EXCLUDED.timestamp),
			metadata   = EXCLUDED.metadata
	`, memory.ID, string(memory.Type), memory.Content, contentHash, tagsJSON, memory.SessionID, memory.Timestamp, metaJSON)
	if err != nil {
		return fmt.Errorf("memory: store: %w", err)
	}
	return nil
}

// Retrieve searches memories.
// Pushes type filtering and LIMIT into SQL so we don't pull the
// entire table into Go. Content scoring still happens in Go on the
// already-limited result set.
func (s *PostgresStore) Retrieve(ctx context.Context, query Query) (*SearchResult, error) {
	sqlQuery := `SELECT id, type, content, tags, session_id, timestamp, metadata
		FROM memory_items WHERE 1=1`
	var args []any
	argIdx := 1

	if len(query.Types) > 0 {
		placeholders := make([]string, len(query.Types))
		for i, t := range query.Types {
			placeholders[i] = fmt.Sprintf("$%d", argIdx)
			args = append(args, string(t))
			argIdx++
		}
		sqlQuery += " AND type IN (" + strings.Join(placeholders, ", ") + ")"
	}

	if query.SessionID != "" {
		sqlQuery += fmt.Sprintf(" AND session_id = $%d", argIdx)
		args = append(args, query.SessionID)
		argIdx++
	}

	sqlQuery += " ORDER BY timestamp DESC"

	// Fetch a generous overscan so content-scoring has enough candidates
	// but we still clip the worst-case row count.
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}
	overscan := limit * 10
	if overscan > 500 {
		overscan = 500
	}
	sqlQuery += fmt.Sprintf(" LIMIT $%d", argIdx)
	args = append(args, overscan)
	argIdx++

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: retrieve: %w", err)
	}
	defer rows.Close()

	var memories []Memory
	for rows.Next() {
		var m Memory
		var typeStr string
		var tagsJSON, metaJSON []byte
		if err := rows.Scan(&m.ID, &typeStr, &m.Content, &tagsJSON, &m.SessionID, &m.Timestamp, &metaJSON); err != nil {
			log.Printf("memory: scan error: %v", err)
			continue
		}
		m.Type = MemoryType(typeStr)
		if err := json.Unmarshal(tagsJSON, &m.Tags); err != nil {
			log.Printf("memory: unmarshal tags: %v", err)
		}
		if err := json.Unmarshal(metaJSON, &m.Metadata); err != nil {
			log.Printf("memory: unmarshal metadata: %v", err)
		}
		if m.Tags == nil {
			m.Tags = []string{}
		}
		if m.Metadata == nil {
			m.Metadata = make(map[string]string)
		}

		// Tags still need Go-side filtering (JSONB overlap queries are
		// possible but complex; keep the simple approach for tag matching).
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

	sort.Slice(memories, func(i, j int) bool {
		return memories[i].Relevance > memories[j].Relevance
	})

	total := len(memories)
	if limit < total {
		memories = memories[:limit]
	}

	return &SearchResult{
		Memories: memories,
		Total:    total,
	}, rows.Err()
}

// Delete removes a memory by ID.
func (s *PostgresStore) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM memory_items WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("memory: delete: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Get retrieves a single memory by ID.
func (s *PostgresStore) Get(ctx context.Context, id string) (*Memory, error) {
	var m Memory
	var typeStr string
	var tagsJSON, metaJSON []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT id, type, content, tags, session_id, timestamp, metadata
		FROM memory_items WHERE id = $1
	`, id).Scan(&m.ID, &typeStr, &m.Content, &tagsJSON, &m.SessionID, &m.Timestamp, &metaJSON)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("memory: get %s: %w", id, err)
	}
	m.Type = MemoryType(typeStr)
	if err := json.Unmarshal(tagsJSON, &m.Tags); err != nil {
		log.Printf("memory: unmarshal tags in Get(%s): %v", id, err)
	}
	if err := json.Unmarshal(metaJSON, &m.Metadata); err != nil {
		log.Printf("memory: unmarshal metadata in Get(%s): %v", id, err)
	}
	if m.Tags == nil {
		m.Tags = []string{}
	}
	if m.Metadata == nil {
		m.Metadata = make(map[string]string)
	}
	return &m, nil
}

// List returns memories with optional filtering.
func (s *PostgresStore) List(ctx context.Context, opts ListOptions) ([]Memory, error) {
	query := `SELECT id, type, content, tags, session_id, timestamp, metadata FROM memory_items WHERE 1=1`
	var args []any
	argIdx := 1

	if opts.SessionID != "" {
		query += fmt.Sprintf(" AND session_id = $%d", argIdx)
		args = append(args, opts.SessionID)
		argIdx++
	}
	if opts.Type != "" {
		query += fmt.Sprintf(" AND type = $%d", argIdx)
		args = append(args, string(opts.Type))
		argIdx++
	}

	query += " ORDER BY timestamp DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, opts.Limit)
		argIdx++
	}
	if opts.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, opts.Offset)
		argIdx++
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: list: %w", err)
	}
	defer rows.Close()

	var memories []Memory
	for rows.Next() {
		var m Memory
		var typeStr string
		var tagsJSON, metaJSON []byte
		if err := rows.Scan(&m.ID, &typeStr, &m.Content, &tagsJSON, &m.SessionID, &m.Timestamp, &metaJSON); err != nil {
			log.Printf("memory: scan error: %v", err)
			continue
		}
		m.Type = MemoryType(typeStr)
		if err := json.Unmarshal(tagsJSON, &m.Tags); err != nil {
			log.Printf("memory: unmarshal tags: %v", err)
		}
		if err := json.Unmarshal(metaJSON, &m.Metadata); err != nil {
			log.Printf("memory: unmarshal metadata: %v", err)
		}
		if m.Tags == nil {
			m.Tags = []string{}
		}
		if m.Metadata == nil {
			m.Metadata = make(map[string]string)
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// Close cancels the background prune goroutine. The caller owns the DB lifecycle.
func (s *PostgresStore) Close() error {
	if s == nil {
		return nil
	}
	if s.pruneCancel != nil {
		s.pruneCancel()
	}
	return nil
}
