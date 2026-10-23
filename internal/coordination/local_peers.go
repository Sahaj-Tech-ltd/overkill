package coordination

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

type SessionPeer struct {
	Presence RuntimePresence `json:"presence"`
	Peer
	SessionID  string          `json:"session_id"`
	Scope      string          `json:"scope"`
	Repository *RepositoryInfo `json:"repository,omitempty"`
}

// LocalProjectID groups explicitly registered local peers in one canonical
// workspace. Callers cannot choose a foreign project through model metadata.
func LocalProjectID(workspace string) string {
	hash := sha256.Sum256([]byte(workspace))
	return "workspace:" + hex.EncodeToString(hash[:])
}

// RegisterSessionPeer is an authenticated application operation. Peer creation
// and immutable session binding commit together, including concurrent retries.
// Local tools need no bearer credential; no unused external credential is made.
func (s *Store) RegisterSessionPeer(ctx context.Context, sessionID, name, role string) (Peer, error) {
	return s.registerSessionPeer(ctx, sessionID, name, role, false)
}

func (s *Store) registerSessionPeer(ctx context.Context, sessionID, name, role string, repository bool) (Peer, error) {
	if _, err := uuid.Parse(sessionID); err != nil {
		return Peer{}, fmt.Errorf("coordination: valid persisted session ID required")
	}
	if !peerName.MatchString(name) || strings.TrimSpace(role) == "" || len(role) > 256 {
		return Peer{}, fmt.Errorf("coordination: name must use lowercase letters, digits, hyphens or underscores; role must be 1..256 bytes")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Peer{}, err
	}
	defer tx.Rollback()
	var folder string
	if err := tx.QueryRowContext(ctx, `SELECT folder FROM sessions WHERE id=$1 FOR UPDATE`, sessionID).Scan(&folder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Peer{}, ErrNotFound
		}
		return Peer{}, err
	}
	peer, err := preparePeer(Peer{ProjectID: "local", Name: name, Role: role, Workspace: folder})
	if err != nil {
		return Peer{}, err
	}
	peer.ProjectID = LocalProjectID(peer.Workspace)
	var snapshot repositorySnapshot
	if repository {
		snapshot, err = inspectRepository(ctx, folder)
		if err != nil {
			return Peer{}, err
		}
		peer.Workspace = snapshot.Root
		peer.ProjectID = repositoryProjectID(snapshot.CommonDir)
		// A canonical path may be replaced with another repository. It must never
		// acquire the previous repository's project history simply by reusing a path.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':overkill:repository:' || $1,0))`, peer.ProjectID); err != nil {
			return Peer{}, err
		}
		var mismatched bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_repositories r JOIN coordination_peers p ON p.id=r.peer_id WHERE p.project_id=$1 AND (r.common_dir<>$2 OR r.common_identity<>$3))`, peer.ProjectID, snapshot.CommonDir, snapshot.commonIdentity).Scan(&mismatched); err != nil {
			return Peer{}, err
		}
		if mismatched {
			return Peer{}, fmt.Errorf("coordination: repository identity was replaced: %w", ErrConflict)
		}

	}
	existing, err := scanPeer(tx.QueryRowContext(ctx, `SELECT p.id,p.project_id,p.name,p.role,p.workspace,p.capabilities,p.endpoint,p.enabled FROM coordination_peers p JOIN coordination_sessions b ON b.peer_id=p.id WHERE b.session_id=$1`, sessionID))
	if err == nil {
		if existing.Enabled && existing.ProjectID == peer.ProjectID && existing.Name == peer.Name && existing.Role == peer.Role && existing.Workspace == peer.Workspace {
			if repository {
				var root, common, rootID, commonID string
				if err := tx.QueryRowContext(ctx, `SELECT root,common_dir,root_identity,common_identity FROM coordination_repositories WHERE peer_id=$1 AND session_id=$2`, existing.ID, sessionID).Scan(&root, &common, &rootID, &commonID); err != nil {
					return Peer{}, err
				}
				if root != snapshot.Root || common != snapshot.CommonDir || rootID != snapshot.rootIdentity || commonID != snapshot.commonIdentity {
					return Peer{}, ErrConflict
				}
			}
			return existing, nil
		}
		return Peer{}, ErrConflict
	}
	if !errors.Is(err, ErrUnauthorized) {
		return Peer{}, err
	}
	capabilities, err := json.Marshal(peer.Capabilities)
	if err != nil {
		return Peer{}, err
	}
	// A uniqueness race in another session is a conflict, never an implicit
	// transfer of a named agent or its correspondence to a new conversation.
	_, err = tx.ExecContext(ctx, `INSERT INTO coordination_peers(id,project_id,name,role,workspace,capabilities) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(project_id,name) DO NOTHING`, peer.ID, peer.ProjectID, peer.Name, peer.Role, peer.Workspace, capabilities)
	if err != nil {
		return Peer{}, err
	}
	var owns bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_peers WHERE id=$1)`, peer.ID).Scan(&owns); err != nil {
		return Peer{}, err
	}
	if !owns {
		return Peer{}, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO coordination_sessions(peer_id,session_id) VALUES($1,$2)`, peer.ID, sessionID); err != nil {
		return Peer{}, err
	}
	if repository {
		if _, err := tx.ExecContext(ctx, `INSERT INTO coordination_repositories(peer_id,session_id,root,common_dir,root_identity,common_identity) VALUES($1,$2,$3,$4,$5,$6)`, peer.ID, sessionID, snapshot.Root, snapshot.CommonDir, snapshot.rootIdentity, snapshot.commonIdentity); err != nil {
			return Peer{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Peer{}, err
	}
	return peer, nil
}

// LocalPeersForSession lists explicitly configured peers in the bound scope.
// Only opted-in repository sessions can inventory across linked worktrees;
// unregistered and default sessions retain the canonical workspace boundary.
func (s *Store) LocalPeersForSession(ctx context.Context, sessionID string) ([]SessionPeer, error) {
	var folder string
	if err := s.db.QueryRowContext(ctx, `SELECT folder FROM sessions WHERE id=$1`, sessionID).Scan(&folder); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !filepath.IsAbs(folder) {
		return nil, fmt.Errorf("coordination: session requires an absolute workspace")
	}
	folder, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return nil, err
	}
	projectID := LocalProjectID(folder)
	repositoryScope := false
	bound, boundErr := s.PeerForSession(ctx, sessionID)
	if boundErr != nil && !errors.Is(boundErr, ErrNotFound) {
		return nil, boundErr
	}
	if boundErr == nil && strings.HasPrefix(bound.ProjectID, "repository:") {
		projectID = bound.ProjectID
		repositoryScope = true
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,p.project_id,p.name,p.role,p.workspace,p.capabilities,p.endpoint,p.enabled,b.session_id FROM coordination_peers p JOIN coordination_sessions b ON b.peer_id=p.id JOIN sessions sess ON sess.id=b.session_id WHERE p.project_id=$1 AND ($3 OR p.workspace=$2) AND p.enabled ORDER BY p.name`, projectID, folder, repositoryScope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]SessionPeer, 0)
	for rows.Next() {
		var item SessionPeer
		var capabilities []byte
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.Name, &item.Role, &item.Workspace, &capabilities, &item.Endpoint, &item.Enabled, &item.SessionID); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(capabilities, &item.Capabilities); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range result {
		result[i].Scope = "workspace"
		if repositoryScope {
			info, err := s.repositoryInfoForPeer(ctx, result[i].Peer, result[i].SessionID)
			if err != nil {
				return nil, err
			}
			result[i].Scope = "repository"
			result[i].Repository = &info
		}
	}
	ids := make([]string, 0, len(result))
	for _, peer := range result {
		ids = append(ids, peer.ID)
	}
	presence, err := s.RuntimePresenceForPeers(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range result {
		result[i].Presence = presence[result[i].ID]
	}
	return result, nil
}

type CorrespondencePage struct {
	Messages []Message `json:"messages"`
	BeforeID string    `json:"before_id,omitempty"`
	HasMore  bool      `json:"has_more"`
}

// Correspondence pages all durable states, newest first. The immutable
// creation-time/UUID cursor prevents status updates from moving items between
// pages. A cursor outside this peer's correspondence is rejected.
func (s *Store) Correspondence(ctx context.Context, peerID, beforeID string, limit int) (CorrespondencePage, error) {
	if err := s.validateRepositoryPeerID(ctx, peerID); err != nil {
		return CorrespondencePage{}, err
	}
	result := CorrespondencePage{Messages: make([]Message, 0)}
	if limit < 1 || limit > 200 {
		return result, fmt.Errorf("coordination: page limit must be 1..200")
	}
	var before Message
	var err error
	if beforeID != "" {
		before, err = s.Read(ctx, peerID, beforeID)
		if err != nil {
			return result, err
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages WHERE (from_peer=$1 OR to_peer=$1) AND EXISTS(SELECT 1 FROM coordination_peers WHERE id=$1 AND enabled) AND ($2='' OR (created_at,id)<($3,$2)) ORDER BY created_at DESC,id DESC LIMIT $4`, peerID, beforeID, before.CreatedAt, limit+1)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return result, err
		}
		result.Messages = append(result.Messages, message)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	if len(result.Messages) > limit {
		result.HasMore = true
		result.Messages = result.Messages[:limit]
		result.BeforeID = result.Messages[limit-1].ID
	}
	return result, nil
}
