package session

import (
	"time"

	"github.com/google/uuid"
)

type Session struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Folder     string            `json:"folder"`
	ParentID   string            `json:"parent_id,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Model      string            `json:"model"`
	Provider   string            `json:"provider"`
	TokenCount int64             `json:"token_count"`
	CostUSD    float64           `json:"cost_usd"`
	TurnCount  int               `json:"turn_count"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Status     string            `json:"status"`
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
