package learning

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"

	_ "github.com/lib/pq"

	"github.com/rs/zerolog/log"
)

// DefaultMaxCorrections is the maximum number of corrections to store
// before evicting the oldest (LRU).
const DefaultMaxCorrections = 1000

// Store persists corrections using PostgreSQL and provides retrieval
// via LIKE keyword search.
type Store struct {
	mu       sync.RWMutex
	db       *sql.DB
	maxItems int
}

// NewStore opens or creates a PostgreSQL-backed correction store.
// connString should be a PostgreSQL connection string, e.g.
// "postgres://user:pass@localhost:5432/overkill?sslmode=disable".
func NewStore(connString string, maxItems int) (*Store, error) {
	if maxItems <= 0 {
		maxItems = DefaultMaxCorrections
	}

	db, err := sql.Open("postgres", connString)
	if err != nil {
		return nil, fmt.Errorf("learning: opening postgres db: %w", err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS corrections (
		id SERIAL PRIMARY KEY,
		context TEXT NOT NULL,
		wrong TEXT NOT NULL,
		correct TEXT NOT NULL,
		timestamp BIGINT NOT NULL,
		UNIQUE(context, wrong)
	)`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("learning: creating table: %w", err)
	}

	log.Info().Int("max_items", maxItems).Msg("corrections store opened")

	return &Store{
		db:       db,
		maxItems: maxItems,
	}, nil
}

// Close closes the underlying PostgreSQL database.
func (s *Store) Close() error {
	log.Info().Msg("corrections store closing")
	return s.db.Close()
}

// Save stores a correction. If a correction with the same (context, wrong)
// already exists, it is overwritten (UPSERT via ON CONFLICT).
// Enforces maxItems by evicting the oldest corrections when full.
func (s *Store) Save(c *Correction) error {
	if c == nil {
		return fmt.Errorf("learning: cannot save nil correction")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// ON CONFLICT ... DO UPDATE handles dedup via UNIQUE(context, wrong).
	_, err := s.db.Exec(
		`INSERT INTO corrections (context, wrong, correct, timestamp)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (context, wrong) DO UPDATE SET
			correct = EXCLUDED.correct,
			timestamp = EXCLUDED.timestamp`,
		c.Context, c.Wrong, c.Correct, c.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("learning: saving correction: %w", err)
	}

	// Enforce maxItems: delete oldest entries if over the limit.
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM corrections").Scan(&count); err != nil {
		return fmt.Errorf("learning: counting corrections: %w", err)
	}
	if count > s.maxItems {
		_, err = s.db.Exec(
			`DELETE FROM corrections WHERE id IN (
				SELECT id FROM corrections ORDER BY timestamp ASC LIMIT $1
			)`,
			count-s.maxItems,
		)
		if err != nil {
			return fmt.Errorf("learning: evicting oldest: %w", err)
		}
	}

	return nil
}

// escapeLike escapes SQL LIKE wildcard characters % and _ in the
// provided string so they are treated as literals.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// FindCorrections returns up to topK corrections matching the query.
// Matches are found via LIKE on the context and wrong columns against
// each token in the query, ordered by timestamp DESC (most recent first).
// Query tokens are escaped to prevent % and _ being treated as wildcards.
func (s *Store) FindCorrections(query string, topK int) ([]*Correction, error) {
	if topK <= 0 {
		topK = 3
	}

	queryTokens := tokenize(query)
	if len(queryTokens) == 0 {
		return nil, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Build WHERE clause: each token must appear in either context or wrong.
	var conditions []string
	var args []any
	for i, tok := range queryTokens {
		pattern := "%" + escapeLike(tok) + "%"
		conditions = append(conditions, fmt.Sprintf("(context LIKE $%d ESCAPE '\\' OR wrong LIKE $%d ESCAPE '\\')", i*2+1, i*2+2))
		args = append(args, pattern, pattern)
	}

	querySQL := fmt.Sprintf(
		"SELECT context, wrong, correct, timestamp FROM corrections WHERE %s ORDER BY timestamp DESC LIMIT $%d",
		strings.Join(conditions, " AND "),
		len(args)+1,
	)
	args = append(args, topK)

	rows, err := s.db.Query(querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("learning: finding corrections: %w", err)
	}
	defer rows.Close()

	var results []*Correction
	for rows.Next() {
		var c Correction
		if err := rows.Scan(&c.Context, &c.Wrong, &c.Correct, &c.Timestamp); err != nil {
			continue
		}
		results = append(results, &c)
	}

	return results, rows.Err()
}

// Count returns the current number of stored corrections.
func (s *Store) Count() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM corrections").Scan(&count)
	return count, err
}
