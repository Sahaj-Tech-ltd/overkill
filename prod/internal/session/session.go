package session

import (
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type Session struct {
	mu         sync.Mutex          `json:"-"`
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

// AutoTitle sets Title from the first user message, truncated to 72
// characters. Only sets when Title is empty — no overwrites.
func (s *Session) AutoTitle(firstMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Title != "" || firstMsg == "" {
		return
	}
	// Trim to 72 chars at rune boundary, break at word boundary.
	// Use rune slicing to avoid splitting multi-byte UTF-8 sequences,
	// then byte-cap so the total with "..." stays ≤75 bytes.
	msg := strings.TrimSpace(firstMsg)
	runes := []rune(msg)
	const maxRunes = 72
	if len(runes) > maxRunes {
		// Take up to maxRunes runes, then shrink further if byte
		// length (with "...") would exceed 75.
		runes = runes[:maxRunes]
		msg = string(runes)
		// byte-cap: walk runes backward until len(msg)+3 ≤ 75
		for len(msg)+3 > 75 && len(runes) > 0 {
			runes = runes[:len(runes)-1]
			msg = string(runes)
		}
		if lastSpace := strings.LastIndexByte(msg, ' '); lastSpace > 40 {
			msg = msg[:lastSpace]
		}
		msg += "..."
	} else {
		msg = string(runes)
	}
	s.Title = msg
}

func (s *Session) IsSubSession() bool {
	return s.ParentID != ""
}
