package coordination

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Store struct{ db *sql.DB }

// NewStore borrows the pool. Initialization is transactional and fatal on error.
func NewStore(ctx context.Context, database *sql.DB) (*Store, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if database == nil {
		return nil, fmt.Errorf("coordination: database is required")
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':overkill:coordination', 0))`); err != nil {
		return nil, err
	}
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS coordination_peers (
			id TEXT PRIMARY KEY, project_id TEXT NOT NULL, name TEXT NOT NULL,
			role TEXT NOT NULL, workspace TEXT NOT NULL, capabilities JSONB NOT NULL DEFAULT '[]',
			enabled BOOLEAN NOT NULL DEFAULT true, UNIQUE(project_id, name))`,
		`ALTER TABLE coordination_peers ADD COLUMN IF NOT EXISTS endpoint TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS coordination_credentials (
			token_hash BYTEA PRIMARY KEY, peer_id TEXT NOT NULL REFERENCES coordination_peers(id),
			revoked BOOLEAN NOT NULL DEFAULT false)`,
		`ALTER TABLE coordination_credentials ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ`,
		`CREATE TABLE IF NOT EXISTS coordination_sessions (
			peer_id TEXT PRIMARY KEY REFERENCES coordination_peers(id), session_id TEXT NOT NULL UNIQUE)`,
		`CREATE TABLE IF NOT EXISTS coordination_leases (
			peer_id TEXT PRIMARY KEY REFERENCES coordination_peers(id), owner_id TEXT NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS coordination_cursors (
			project_id TEXT PRIMARY KEY, sequence BIGINT NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS coordination_messages (
			id TEXT PRIMARY KEY, project_id TEXT NOT NULL,
			from_peer TEXT NOT NULL REFERENCES coordination_peers(id),
			to_peer TEXT NOT NULL REFERENCES coordination_peers(id),
			kind TEXT NOT NULL, conversation_id TEXT NOT NULL, task_id TEXT NOT NULL DEFAULT '',
			in_reply_to TEXT NOT NULL DEFAULT '', payload JSONB NOT NULL,
			state TEXT NOT NULL DEFAULT 'accepted' CHECK (state IN ('accepted','started','blocked','completed','failed','cancelled')),
			revision BIGINT NOT NULL DEFAULT 1, idempotency_key TEXT NOT NULL, request_hash BYTEA NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deadline TIMESTAMPTZ NOT NULL, UNIQUE(from_peer, idempotency_key))`,
		`CREATE INDEX IF NOT EXISTS idx_coordination_inbox ON coordination_messages(to_peer, state, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS coordination_events (
			project_id TEXT NOT NULL, sequence BIGINT NOT NULL,
			message_id TEXT NOT NULL REFERENCES coordination_messages(id), revision BIGINT NOT NULL,
			type TEXT NOT NULL, payload JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY(project_id, sequence), UNIQUE(message_id, revision))`,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return nil, fmt.Errorf("coordination: migrate: %w", err)
		}
	}
	for _, ddl := range append(append(append([]string{}, callbackDDL...), presenceDDL...), append(repositoryDDL, transportDDL...)...) {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return nil, fmt.Errorf("coordination: callback migration: %w", err)
		}
	}
	if err := migrateContracts(ctx, tx); err != nil {
		return nil, err
	}
	for _, ddl := range append(append([]string{}, budgetDDL...), dependencyDDL...) {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return nil, fmt.Errorf("coordination: budget migration: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Store{db: database}, nil
}

var peerName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// RegisterPeer is an application/admin operation, never an agent-facing tool.
// The returned credential is shown once and must be kept outside model input.
func preparePeer(peer Peer) (Peer, error) {
	if err := ValidatePeerEndpoint(peer.Endpoint); err != nil {
		return Peer{}, err
	}
	if strings.TrimSpace(peer.ProjectID) == "" || len(peer.ProjectID) > 256 || !peerName.MatchString(peer.Name) || strings.TrimSpace(peer.Role) == "" || len(peer.Role) > 256 {
		return Peer{}, fmt.Errorf("coordination: valid project, peer name and role are required")
	}
	if !filepath.IsAbs(peer.Workspace) || len(peer.Capabilities) > 64 {
		return Peer{}, fmt.Errorf("coordination: absolute workspace and at most 64 capabilities required")
	}
	for _, capability := range peer.Capabilities {
		if len(capability) == 0 || len(capability) > 128 {
			return Peer{}, fmt.Errorf("coordination: capability must be 1..128 bytes")
		}
	}
	workspace, err := filepath.EvalSymlinks(peer.Workspace)
	if err != nil {
		return Peer{}, fmt.Errorf("coordination: workspace: %w", err)
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return Peer{}, err
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return Peer{}, fmt.Errorf("coordination: workspace must be an existing directory")
	}
	peer.ID, peer.Workspace, peer.Enabled = uuid.NewString(), workspace, true
	if peer.Capabilities == nil {
		peer.Capabilities = []string{}
	}
	return peer, nil
}

func (s *Store) RegisterPeer(ctx context.Context, peer Peer) (Peer, string, error) {
	if strings.HasPrefix(peer.ProjectID, "repository:") {
		return Peer{}, "", fmt.Errorf("coordination: repository scope requires explicit session registration")
	}
	peer, err := preparePeer(peer)
	if err != nil {
		return Peer{}, "", err
	}
	capabilities, err := json.Marshal(peer.Capabilities)
	if err != nil {
		return Peer{}, "", err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Peer{}, "", err
	}
	token := hex.EncodeToString(raw[:])
	hash := sha256.Sum256([]byte(token))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Peer{}, "", err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO coordination_peers(id, project_id, name, role, workspace, capabilities,endpoint) VALUES($1,$2,$3,$4,$5,$6,$7)`, peer.ID, peer.ProjectID, peer.Name, peer.Role, peer.Workspace, capabilities, peer.Endpoint)
	if err != nil {
		return Peer{}, "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO coordination_credentials(token_hash, peer_id) VALUES($1,$2)`, hash[:], peer.ID); err != nil {
		return Peer{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return Peer{}, "", err
	}
	return peer, token, nil
}

func (s *Store) Authenticate(ctx context.Context, token string) (Peer, error) {
	if len(token) != 64 {
		return Peer{}, ErrUnauthorized
	}
	hash := sha256.Sum256([]byte(token))
	return scanPeer(s.db.QueryRowContext(ctx, `SELECT p.id,p.project_id,p.name,p.role,p.workspace,p.capabilities,p.endpoint,p.enabled
		FROM coordination_peers p JOIN coordination_credentials c ON c.peer_id=p.id
		WHERE c.token_hash=$1 AND NOT c.revoked AND p.enabled AND (c.expires_at IS NULL OR c.expires_at>clock_timestamp())`, hash[:]))
}

type rowScanner interface{ Scan(...any) error }

func scanPeer(row rowScanner) (Peer, error) {
	var peer Peer
	var caps []byte
	if err := row.Scan(&peer.ID, &peer.ProjectID, &peer.Name, &peer.Role, &peer.Workspace, &caps, &peer.Endpoint, &peer.Enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Peer{}, ErrUnauthorized
		}
		return Peer{}, err
	}
	if err := json.Unmarshal(caps, &peer.Capabilities); err != nil {
		return Peer{}, err
	}
	return peer, nil
}

const messageColumns = `id,project_id,from_peer,to_peer,kind,conversation_id,task_id,in_reply_to,payload,state,revision,created_at,updated_at,deadline,parent_message_id,request_depth,execution_attempts,depends_on,required_contracts`

func scanMessage(row rowScanner) (Message, error) {
	var m Message
	var dependencies, contracts []byte
	err := row.Scan(&m.ID, &m.ProjectID, &m.From, &m.To, &m.Kind, &m.ConversationID, &m.TaskID, &m.InReplyTo, &m.Payload, &m.State, &m.Revision, &m.CreatedAt, &m.UpdatedAt, &m.Deadline, &m.ParentMessageID, &m.RequestDepth, &m.ExecutionAttempts, &dependencies, &contracts)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(dependencies, &m.DependsOn)
	}
	if err == nil {
		err = json.Unmarshal(contracts, &m.RequiredContracts)
	}
	return m, err
}

// Send persists acceptance before returning a handle. fromPeer comes from the
// authenticated application boundary, not a user-supplied payload field.
func (s *Store) Send(ctx context.Context, fromPeer string, input SendInput) (Message, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	message, err := s.sendTx(ctx, tx, fromPeer, input)
	if err != nil {
		if budgetRejection(err) || errors.Is(err, ErrRequestContracts) {
			if commitErr := commitPeerTransaction(ctx, tx, fromPeer); commitErr != nil {
				return Message{}, commitErr
			}
		}
		return Message{}, err
	}
	if err := commitPeerTransaction(ctx, tx, fromPeer); err != nil {
		return Message{}, err
	}
	return message, nil
}

// sendTx permits completion and its correlated answer to commit together.
func (s *Store) sendTx(ctx context.Context, tx *sql.Tx, fromPeer string, input SendInput) (Message, error) {
	switch input.Kind {
	case "request", "answer", "progress", "blocker", "contract_changed", "completion":
	default:
		return Message{}, fmt.Errorf("coordination: unsupported message kind %q", input.Kind)
	}
	if !peerName.MatchString(input.To) || len(input.Payload) == 0 || len(input.Payload) > 65536 || !json.Valid(input.Payload) || len(input.IdempotencyKey) == 0 || len(input.IdempotencyKey) > 128 {
		return Message{}, fmt.Errorf("coordination: recipient, JSON payload <=64KiB and idempotency key are required")
	}
	if input.ConversationID != "" {
		if _, err := uuid.Parse(input.ConversationID); err != nil {
			return Message{}, fmt.Errorf("coordination: invalid conversation ID")
		}
	}
	if len(input.TaskID) > 128 || len(input.InReplyTo) > 128 {
		return Message{}, fmt.Errorf("coordination: invalid correlation ID")
	}
	if err := validateRequiredContracts(input); err != nil {
		return Message{}, err
	}
	if err := validateDependencies(input); err != nil {
		return Message{}, err
	}
	// Hash the submitted envelope before filling generated IDs/default deadlines.
	// Retries with omitted defaults must still identify the same request.
	var envelope []byte
	var err error
	if limits, ok := ctx.Value(usageBudgetKey{}).(UsageLimits); ok {
		envelope, err = json.Marshal(struct {
			Input  SendInput
			Budget UsageLimits
		}{input, limits})
	} else {
		envelope, err = json.Marshal(input)
	}
	if err != nil {
		return Message{}, err
	}
	digest := sha256.Sum256(envelope)
	if lineage, ok := ctx.Value(executionContextKey{}).(executionLineage); ok {
		digest = sha256.Sum256(append(digest[:], []byte(lineage.messageID)...))
	}
	if err := lockAdmission(ctx, tx, fromPeer); err != nil {
		return Message{}, err
	}
	from, err := enabledPeer(ctx, tx, fromPeer)
	if err != nil {
		return Message{}, err
	}
	if err = checkScopedCredential(ctx, tx, fromPeer, true); err != nil {
		return Message{}, err
	}
	to, err := scanPeer(tx.QueryRowContext(ctx, `SELECT id,project_id,name,role,workspace,capabilities,endpoint,enabled FROM coordination_peers WHERE project_id=$1 AND name=$2 AND enabled FOR SHARE`, from.ProjectID, input.To))
	if err != nil {
		return Message{}, err
	}
	if err := validateRepositoryPeer(ctx, tx, to); err != nil {
		return Message{}, err
	}
	if from.ID == to.ID {
		return Message{}, fmt.Errorf("coordination: recipient must be another peer")
	}
	// Existing idempotent receipts remain retrievable after their deadline.
	var priorHash []byte
	prior := tx.QueryRowContext(ctx, `SELECT request_hash FROM coordination_messages WHERE from_peer=$1 AND idempotency_key=$2`, from.ID, input.IdempotencyKey).Scan(&priorHash)
	if prior == nil {
		if !equalHash(priorHash, digest[:]) {
			return Message{}, ErrIdempotency
		}
		return scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages WHERE from_peer=$1 AND idempotency_key=$2`, from.ID, input.IdempotencyKey))
	}
	if !errors.Is(prior, sql.ErrNoRows) {
		return Message{}, prior
	}
	if external, _ := ctx.Value(externalIngressKey{}).(bool); external && input.InReplyTo != "" && (input.Kind == "answer" || input.Kind == "completion") {
		if err := rejectExternalWorkerLease(ctx, tx, from.ID); err != nil {
			return Message{}, err
		}
	}
	var replyTo *Message
	if input.InReplyTo != "" {
		original, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages WHERE id=$1 AND project_id=$2 FOR UPDATE`, input.InReplyTo, from.ProjectID))
		if err != nil {
			return Message{}, err
		}
		if original.To != from.ID || original.From != to.ID {
			return Message{}, ErrUnauthorized
		}
		if input.ConversationID != "" && input.ConversationID != original.ConversationID {
			return Message{}, fmt.Errorf("coordination: reply conversation mismatch")
		}
		input.ConversationID = original.ConversationID
		replyTo = &original
	}
	parentID, depth, err := deriveLineage(ctx, tx, from, &input, replyTo)
	if err != nil {
		return Message{}, err
	}
	if input.ConversationID == "" {
		input.ConversationID = uuid.NewString()
	}
	if input.Deadline.IsZero() {
		input.Deadline = time.Now().UTC().Add(24 * time.Hour)
	}
	if err := inheritRequestContracts(ctx, tx, from, parentID, &input); err != nil {
		return Message{}, err
	}
	if replyTo != nil && replyTo.ID != parentID {
		if err := inheritRequestContracts(ctx, tx, from, replyTo.ID, &input); err != nil {
			return Message{}, err
		}
	}
	if len(input.RequiredContracts) > 0 {
		if err := lockContractProject(ctx, tx, from.ProjectID); err != nil {
			return Message{}, err
		}
		for _, pin := range input.RequiredContracts {
			c, e := contractInProject(ctx, tx, from.ProjectID, pin.ContractID)
			if e != nil {
				return Message{}, e
			}
			if c.Revision != pin.Revision {
				return Message{}, ErrConflict
			}
		}
	}
	if err := authorizeDependencies(ctx, tx, from, input.DependsOn); err != nil {
		return Message{}, err
	}
	if replyTo != nil && (input.Kind == "answer" || input.Kind == "completion") && (replyTo.State == Cancelled || replyTo.State == Failed) {
		return Message{}, fmt.Errorf("coordination: cannot answer %s work", replyTo.State)
	}
	if replyTo != nil && !replyTo.State.Terminal() && (input.Kind == "answer" || input.Kind == "completion") {
		if err := enforceRequestContracts(ctx, tx, *replyTo); err != nil {
			return Message{}, err
		}
	}
	if replyTo != nil && (input.Kind == "answer" || input.Kind == "completion") {
		var answered bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_messages WHERE in_reply_to=$1 AND kind IN ('answer','completion'))`, replyTo.ID).Scan(&answered); err != nil {
			return Message{}, err
		}
		if answered {
			return Message{}, ErrConflict
		}
	}
	if !input.Deadline.After(time.Now()) {
		return Message{}, fmt.Errorf("coordination: deadline has elapsed")
	}
	// Establish task ownership before allowing a new root to join an existing
	// task. Executing children inherit the authenticated parent's task instead.
	if err := ensureUsage(ctx, tx, from.ProjectID, inputUsageScope(input), input.TaskID, from.ID, UsageLimits{}); err != nil {
		return Message{}, err
	}
	if parentID == "" {
		var owner string
		if err := tx.QueryRowContext(ctx, `SELECT owner_peer FROM coordination_task_usage WHERE project_id=$1 AND scope=$2`, from.ProjectID, inputUsageScope(input)).Scan(&owner); err != nil {
			return Message{}, err
		}
		if owner != from.ID {
			return Message{}, ErrUnauthorized
		}
	}
	if limits, ok := ctx.Value(usageBudgetKey{}).(UsageLimits); ok {
		if parentID != "" || input.Kind != "request" {
			return Message{}, fmt.Errorf("coordination: budgets belong to root requests")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE coordination_task_usage SET token_limit=$3,cost_limit_nano=$4 WHERE project_id=$1 AND scope=$2 AND NOT EXISTS(SELECT 1 FROM coordination_messages WHERE project_id=$1 AND CASE WHEN task_id<>'' THEN 'task:'||task_id ELSE 'conversation:'||conversation_id END=$2)`, from.ProjectID, inputUsageScope(input), limits.Tokens, int64(math.Round(limits.CostUSD*1e9))); err != nil {
			return Message{}, err
		}
		current, err := scanUsage(tx.QueryRowContext(ctx, `SELECT `+usageColumns+` FROM coordination_task_usage WHERE project_id=$1 AND scope=$2 FOR UPDATE`, from.ProjectID, inputUsageScope(input)))
		if err != nil {
			return Message{}, err
		}
		if current.Limits.Tokens != limits.Tokens || math.Round(current.Limits.CostUSD*1e9) != math.Round(limits.CostUSD*1e9) {
			return Message{}, ErrConflict
		}
	}
	if err := checkAdmission(ctx, tx, from, to, input, parentID, depth, replyTo); err != nil {
		return Message{}, err
	}
	m, err := scanMessage(tx.QueryRowContext(ctx, `INSERT INTO coordination_messages(id,project_id,from_peer,to_peer,kind,conversation_id,task_id,in_reply_to,payload,idempotency_key,request_hash,deadline,parent_message_id,request_depth,depends_on,required_contracts)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT(from_peer,idempotency_key) DO NOTHING RETURNING `+messageColumns,
		uuid.NewString(), from.ProjectID, from.ID, to.ID, input.Kind, input.ConversationID, input.TaskID, input.InReplyTo, []byte(input.Payload), input.IdempotencyKey, digest[:], input.Deadline, parentID, depth, dependenciesJSON(input.DependsOn), requiredContractsJSON(input.RequiredContracts)))
	if errors.Is(err, ErrNotFound) {
		// A concurrent retry won the unique key. Observe its committed envelope.
		if err := tx.QueryRowContext(ctx, `SELECT request_hash FROM coordination_messages WHERE from_peer=$1 AND idempotency_key=$2`, from.ID, input.IdempotencyKey).Scan(&priorHash); err != nil {
			return Message{}, err
		}
		if !equalHash(priorHash, digest[:]) {
			return Message{}, ErrIdempotency
		}
		return scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages WHERE from_peer=$1 AND idempotency_key=$2`, from.ID, input.IdempotencyKey))
	}
	if err != nil {
		return Message{}, err
	}
	if err := appendEvent(ctx, tx, m, "accepted", json.RawMessage(`{}`)); err != nil {
		return Message{}, err
	}
	if replyTo != nil && (input.Kind == "answer" || input.Kind == "completion") && !replyTo.State.Terminal() {
		completed, err := scanMessage(tx.QueryRowContext(ctx, `UPDATE coordination_messages SET state='completed', revision=revision+1, updated_at=clock_timestamp() WHERE id=$1 RETURNING `+messageColumns, replyTo.ID))
		if err != nil {
			return Message{}, err
		}
		payload, _ := json.Marshal(map[string]string{"reply_id": m.ID})
		if err := appendEvent(ctx, tx, completed, "completed", payload); err != nil {
			return Message{}, err
		}
	}
	return m, nil
}

func equalHash(a, b []byte) bool { return bytes.Equal(a, b) }

func appendEvent(ctx context.Context, tx *sql.Tx, m Message, kind string, payload json.RawMessage) error {
	// This row lock serializes event commits per project. A plain BIGSERIAL
	// could commit out of order and make cursor-based replay miss an event.
	var seq int64
	err := tx.QueryRowContext(ctx, `INSERT INTO coordination_cursors(project_id,sequence) VALUES($1,1)
		ON CONFLICT(project_id) DO UPDATE SET sequence=coordination_cursors.sequence+1 RETURNING sequence`, m.ProjectID).Scan(&seq)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO coordination_events(project_id,sequence,message_id,revision,type,payload) VALUES($1,$2,$3,$4,$5,$6)`, m.ProjectID, seq, m.ID, m.Revision, kind, []byte(payload))
	return err
}
