package session

import (
	"time"

	"github.com/google/uuid"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type Session struct {
	ID         string              `json:"id"`
	Title      string              `json:"title"`
	Folder     string              `json:"folder"`
	ParentID   string              `json:"parent_id,omitempty"`
	CreatedAt  time.Time           `json:"created_at"`
	UpdatedAt  time.Time           `json:"updated_at"`
	Model      string              `json:"model"`
	Provider   string              `json:"provider"`
	TokenCount int64               `json:"token_count"`
	CostUSD    float64             `json:"cost_usd"`
	TurnCount  int                 `json:"turn_count"`
	Metadata   map[string]string   `json:"metadata,omitempty"`
	Status     string              `json:"status"`
	Messages   []providers.Message `json:"messages,omitempty"`

	// Children lists the session IDs that branched FROM this one
	// (Phase 1.5 #3 — tree-structured sessions). Maintained by Branch().
	// Empty for leaf sessions; never nil after first branch.
	Children []string `json:"children,omitempty"`

	// BranchedAtTurn records which turn-index in the parent's history
	// was the branch point. Only meaningful when ParentID is set.
	// Zero is a valid value (branched from the very start) so we
	// distinguish "branched at turn 0" from "not a branch" by the
	// ParentID field, not by this value.
	BranchedAtTurn int `json:"branched_at_turn,omitempty"`
}

func NewSession(folder string) *Session {
	now := time.Now().UTC()
	return &Session{
		ID:        uuid.New().String(),
		Folder:    folder,
		CreatedAt: now,
		UpdatedAt: now,
		Status:    "active",
		Metadata:  make(map[string]string),
	}
}

func (s *Session) IsSubSession() bool {
	return s.ParentID != ""
}
