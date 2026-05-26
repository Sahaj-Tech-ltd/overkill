// Package tools — bookmark_create / bookmark_list / bookmark_recall
// (master plan §7.4 message bookmarking / reply-context).
//
// Reuses internal/tags + internal/journal: a bookmark is just a tag on
// a journal entry ID, prefixed `bookmark:` to distinguish it from
// file-path tags. Recall looks up the entry by ID and returns its
// content so the agent can pull it back into context.
//
// Why piggyback on tags instead of a dedicated table: the §7.4 plan
// uses "tag this message" as the trigger phrase. The data shape is
// identical (label → ID + note). Adding a parallel system would just
// be two places to look for the same answer.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tags"
)

const bookmarkTagPrefix = "bookmark:"

// JournalReader is the minimal surface bookmark_recall needs from a
// real *journal.FlightRecorder.
type JournalReader interface {
	GetFlight(id string) (*journal.Entry, error)
}

// ---- create ----

// BookmarkCreateTool tags a journal entry ID with a user label so it
// can be recalled later. ID is typically obtained via journal_search.
type BookmarkCreateTool struct {
	tags *tags.Manager
}

func NewBookmarkCreateTool(t *tags.Manager) *BookmarkCreateTool {
	return &BookmarkCreateTool{tags: t}
}

func (t *BookmarkCreateTool) Name() string { return "bookmark_create" }

type bookmarkCreateInput struct {
	// ID is the journal entry ID (from journal_search). Required.
	ID string `json:"id"`
	// Label is a short user-visible name for the bookmark
	// ("payment-bug-repro", "auth-discussion"). Required.
	Label string `json:"label"`
	// Note is an optional one-liner explaining what this bookmark is
	// for. Stored in the tag's note field.
	Note string `json:"note,omitempty"`
}

func (t *BookmarkCreateTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.tags == nil {
		return errorJSON("bookmarks not configured (tags manager missing)"), nil
	}
	var req bookmarkCreateInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("bookmark_create: %w", err)
	}
	if req.ID == "" {
		return errorJSON("id is required (journal entry ID to bookmark)"), nil
	}
	if req.Label == "" {
		return errorJSON("label is required"), nil
	}
	tag := bookmarkTagPrefix + sanitizeLabel(req.Label)
	if err := t.tags.Tag(req.ID, tag, req.Note); err != nil {
		return errorJSON(err.Error()), nil
	}
	body, _ := json.Marshal(map[string]any{
		"id":    req.ID,
		"label": req.Label,
		"tag":   tag,
	})
	return body, nil
}

// ---- list ----

type BookmarkListTool struct {
	tags *tags.Manager
}

func NewBookmarkListTool(t *tags.Manager) *BookmarkListTool {
	return &BookmarkListTool{tags: t}
}

func (t *BookmarkListTool) Name() string { return "bookmark_list" }

func (t *BookmarkListTool) Execute(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	if t.tags == nil {
		return errorJSON("bookmarks not configured"), nil
	}
	all := t.tags.List()
	out := make([]map[string]string, 0, len(all))
	for _, tg := range all {
		if !strings.HasPrefix(tg.Tag, bookmarkTagPrefix) {
			continue
		}
		out = append(out, map[string]string{
			"label":     strings.TrimPrefix(tg.Tag, bookmarkTagPrefix),
			"entry_id":  tg.Path,
			"note":      tg.Note,
		})
	}
	body, _ := json.Marshal(map[string]any{
		"bookmarks": out,
		"count":     len(out),
	})
	return body, nil
}

// ---- recall ----

type BookmarkRecallTool struct {
	tags    *tags.Manager
	journal JournalReader
}

func NewBookmarkRecallTool(t *tags.Manager, j JournalReader) *BookmarkRecallTool {
	return &BookmarkRecallTool{tags: t, journal: j}
}

func (t *BookmarkRecallTool) Name() string { return "bookmark_recall" }

type bookmarkRecallInput struct {
	// Label is the bookmark name to recall. Required.
	Label string `json:"label"`
}

func (t *BookmarkRecallTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.tags == nil {
		return errorJSON("bookmarks not configured"), nil
	}
	if t.journal == nil {
		return errorJSON("journal reader not configured"), nil
	}
	var req bookmarkRecallInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("bookmark_recall: %w", err)
	}
	if req.Label == "" {
		return errorJSON("label is required"), nil
	}
	tag := bookmarkTagPrefix + sanitizeLabel(req.Label)
	matches := t.tags.ByTag(tag)
	if len(matches) == 0 {
		return errorJSON(fmt.Sprintf("no bookmark labelled %q", req.Label)), nil
	}
	// Most-recently-tagged wins when there are duplicates.
	pick := matches[len(matches)-1]
	entry, err := t.journal.GetFlight(pick.Path)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	if entry == nil {
		return errorJSON(fmt.Sprintf("bookmark %q points at journal entry %s but it's missing", req.Label, pick.Path)), nil
	}
	body, _ := json.Marshal(map[string]any{
		"label":     req.Label,
		"entry":     entry,
		"note":      pick.Note,
	})
	return body, nil
}

// sanitizeLabel normalises labels for storage. Lowercase, hyphenated.
// Keeps the bookmark namespace human-readable.
func sanitizeLabel(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, " ", "-")
	return s
}
