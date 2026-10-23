package seahorse

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// Expansion returns exact raw provider messages in source order. It is bounded
// and all-or-error: a partial expansion must not masquerade as complete evidence.
type Expansion struct {
	HeadID   string              `json:"head_id"`
	Nodes    []string            `json:"nodes"`
	Messages []providers.Message `json:"messages"`
}

func Expand(ctx context.Context, store Store, session, id string, maxMessages, maxBytes int) (Expansion, error) {
	var out Expansion
	out.HeadID = id
	if store == nil || session == "" || id == "" || maxMessages < 1 || maxMessages > 4096 || maxBytes < 1 || maxBytes > 4*1024*1024 {
		return Expansion{}, fmt.Errorf("seahorse: invalid expansion bounds")
	}
	visits, bytes := 0, 0
	ancestors := map[string]bool{}
	var walk func(string, int) error
	walk = func(id string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		visits++
		if visits > 1024 || depth > 64 || ancestors[id] {
			return fmt.Errorf("seahorse: expansion node/depth/cycle bound exceeded")
		}
		node, err := store.Node(ctx, session, id)
		if err != nil {
			return err
		}
		ancestors[id] = true
		defer delete(ancestors, id)
		out.Nodes = append(out.Nodes, id)
		for _, parent := range node.Summary.ParentIDs {
			if err = walk(parent, depth+1); err != nil {
				return err
			}
		}
		for _, sourceID := range node.SourceIDs {
			if len(out.Messages) >= maxMessages {
				return fmt.Errorf("seahorse: expansion message bound exceeded")
			}
			source, err := store.Source(ctx, session, sourceID)
			if err != nil {
				return err
			}
			raw, err := json.Marshal(source.Message)
			if err != nil {
				return err
			}
			bytes += len(raw)
			if bytes > maxBytes {
				return fmt.Errorf("seahorse: expansion byte bound exceeded")
			}
			out.Messages = append(out.Messages, source.Message)
		}
		return nil
	}
	if err := walk(id, 0); err != nil {
		return Expansion{}, err
	}
	return out, nil
}

type SourceHit struct {
	SourceID string `json:"source_id"`
	Role     string `json:"role"`
	Preview  string `json:"preview"`
}

// SearchSources searches only this session's immutable raw data. It does not
// treat summaries or model output as authority; Source retrieves the full record.
func (s *PostgresStore) SearchSources(ctx context.Context, session, query string, limit int) ([]SourceHit, error) {
	if session == "" || len(session) > 256 || strings.TrimSpace(query) == "" || len(query) > 1024 || limit < 1 || limit > 50 {
		return nil, fmt.Errorf("seahorse: invalid search bounds")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT source_id,COALESCE(payload->'message'->>'Role',''),left(COALESCE(payload->'message'->>'Content',''),1000) FROM seahorse_sources WHERE session_id=$1 AND to_tsvector('simple',payload::text) @@ plainto_tsquery('simple',$2) ORDER BY source_id LIMIT $3`, session, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := []SourceHit{}
	for rows.Next() {
		var hit SourceHit
		if err = rows.Scan(&hit.SourceID, &hit.Role, &hit.Preview); err != nil {
			return nil, err
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}
