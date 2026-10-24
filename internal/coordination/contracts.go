package coordination

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var (
	ErrOwnershipConflict  = errors.New("coordination: path owned by another contract")
	ErrDependencyCycle    = errors.New("coordination: contract dependency cycle")
	ErrContractValidation = errors.New("coordination: invalid handoff contract")
)

type ContractArtifact struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	MediaType string `json:"media_type,omitempty"`
	Digest    string `json:"digest,omitempty"`
}
type ContractDependency struct {
	ContractID string `json:"contract_id"`
	Revision   int64  `json:"revision"`
}
type CompatibilityChange struct {
	Kind        string `json:"kind"`
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
	Description string `json:"description"`
}

// ContractSpec is an interface agreement, not a task completion assertion.
// TaskID is a reference only: internal/tasks has no transactional dependency
// graph or PostgreSQL execution state to infer acceptance from.
type ContractSpec struct {
	Name                 string                `json:"name"`
	Endpoint             string                `json:"endpoint"`
	SchemaVersion        string                `json:"schema_version"`
	Schema               json.RawMessage       `json:"schema"`
	AcceptanceCriteria   []string              `json:"acceptance_criteria"`
	Artifacts            []ContractArtifact    `json:"artifacts"`
	Workspace            string                `json:"workspace"`
	Worktree             string                `json:"worktree,omitempty"`
	OwnedPaths           []string              `json:"owned_paths"`
	Dependencies         []ContractDependency  `json:"dependencies"`
	CompatibilityChanges []CompatibilityChange `json:"compatibility_changes"`
	TaskID               string                `json:"task_id,omitempty"`
}
type Contract struct {
	ID          string       `json:"id"`
	ProjectID   string       `json:"project_id"`
	OwnerPeerID string       `json:"owner_peer_id"`
	Revision    int64        `json:"revision"`
	Spec        ContractSpec `json:"spec"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}
type ContractAcknowledgement struct {
	ContractID     string    `json:"contract_id"`
	PeerID         string    `json:"peer_id"`
	Revision       int64     `json:"revision"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
}
type ContractBlocker struct {
	ContractID       string `json:"contract_id"`
	Reason           string `json:"reason"`
	ExpectedRevision int64  `json:"expected_revision,omitempty"`
	ActualRevision   int64  `json:"actual_revision,omitempty"`
}
type ContractReadiness struct {
	ContractID string            `json:"contract_id"`
	Revision   int64             `json:"revision"`
	PeerID     string            `json:"peer_id"`
	Ready      bool              `json:"ready"`
	Blockers   []ContractBlocker `json:"blockers"`
	Source     string            `json:"source"`
}

var contractDDL = []string{
	`CREATE TABLE IF NOT EXISTS coordination_workspace_processes(id TEXT PRIMARY KEY,project_id TEXT NOT NULL,peer_id TEXT NOT NULL REFERENCES coordination_peers(id),workspace TEXT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	`CREATE INDEX IF NOT EXISTS idx_coordination_workspace_processes ON coordination_workspace_processes(project_id)`,
	`CREATE TABLE IF NOT EXISTS coordination_contracts (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, owner_peer_id TEXT NOT NULL REFERENCES coordination_peers(id),
 revision BIGINT NOT NULL CHECK(revision>0), spec JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	`CREATE INDEX IF NOT EXISTS coordination_contract_project ON coordination_contracts(project_id,id)`,
	`CREATE TABLE IF NOT EXISTS coordination_contract_revisions (
 contract_id TEXT NOT NULL REFERENCES coordination_contracts(id) ON DELETE CASCADE,
 revision BIGINT NOT NULL, spec JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(contract_id,revision))`,
	`CREATE TABLE IF NOT EXISTS coordination_contract_paths (
 contract_id TEXT NOT NULL REFERENCES coordination_contracts(id) ON DELETE CASCADE,
 project_id TEXT NOT NULL, path TEXT NOT NULL, PRIMARY KEY(contract_id,path))`,
	`CREATE INDEX IF NOT EXISTS coordination_contract_paths_project ON coordination_contract_paths(project_id,path)`,
	`CREATE TABLE IF NOT EXISTS coordination_contract_dependencies (
 contract_id TEXT NOT NULL REFERENCES coordination_contracts(id) ON DELETE CASCADE,
 dependency_id TEXT NOT NULL REFERENCES coordination_contracts(id), revision BIGINT NOT NULL CHECK(revision>0),
 PRIMARY KEY(contract_id,dependency_id), CHECK(contract_id<>dependency_id))`,
	`CREATE TABLE IF NOT EXISTS coordination_contract_acknowledgements (
 contract_id TEXT NOT NULL, revision BIGINT NOT NULL, peer_id TEXT NOT NULL REFERENCES coordination_peers(id),
 acknowledged_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), PRIMARY KEY(contract_id,revision,peer_id),
 FOREIGN KEY(contract_id,revision) REFERENCES coordination_contract_revisions(contract_id,revision) ON DELETE CASCADE)`,
}

func migrateContracts(ctx context.Context, tx *sql.Tx) error {
	for _, ddl := range contractDDL {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("coordination: contract migration: %w", err)
		}
	}
	return nil
}

func validContractText(s string, max int, required bool) bool {
	return utf8.ValidString(s) && len(s) <= max && !strings.ContainsRune(s, 0) && (!required || strings.TrimSpace(s) != "")
}
func normalizeContractPath(p string) (string, error) {
	if !validContractText(p, 1024, true) || strings.ContainsAny(p, "\\:*?[]") || path.IsAbs(p) || filepath.IsAbs(p) {
		return "", fmt.Errorf("%w: relative literal ownership path required", ErrContractValidation)
	}
	clean := path.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: path escapes workspace", ErrContractValidation)
	}
	return clean, nil
}

// NormalizeOwnedPath returns the portable project-relative reservation key.
// Conservative Unicode folding also reserves case-distinct Linux spellings;
// this prevents macOS case/normalization aliases from obtaining two owners.
func NormalizeOwnedPath(p string) (string, error) {
	clean, err := normalizeContractPath(p)
	if err != nil {
		return "", err
	}
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(clean))), nil
}

func rejectOwnershipSymlinks(workspace, relative string) error {
	current, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return fmt.Errorf("%w: workspace unavailable", ErrContractValidation)
	}
	if current != workspace {
		return fmt.Errorf("%w: workspace identity changed", ErrContractValidation)
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%w: workspace must remain a directory", ErrContractValidation)
	}
	if relative == "." {
		return nil
	}
	current = workspace
	for _, part := range strings.Split(relative, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: ownership paths cannot traverse symlinks", ErrContractValidation)
		}
	}
	return nil
}

func prepareContractSpec(peer Peer, spec ContractSpec) (ContractSpec, error) {
	bad := func() (ContractSpec, error) { return ContractSpec{}, ErrContractValidation }
	if !validContractText(spec.Name, 256, true) || !validContractText(spec.Endpoint, 2048, true) || !validContractText(spec.SchemaVersion, 128, true) || !validContractText(spec.TaskID, 256, false) || !validContractText(spec.Worktree, 1024, false) {
		return bad()
	}
	if spec.Workspace == "" {
		spec.Workspace = peer.Workspace
	}
	if !filepath.IsAbs(spec.Workspace) {
		return ContractSpec{}, fmt.Errorf("%w: workspace must be absolute", ErrContractValidation)
	}
	canonicalWorkspace, err := filepath.EvalSymlinks(spec.Workspace)
	if err != nil {
		return ContractSpec{}, fmt.Errorf("%w: workspace cannot be resolved", ErrContractValidation)
	}
	canonicalWorkspace, err = filepath.Abs(canonicalWorkspace)
	if err != nil {
		return ContractSpec{}, err
	}
	if canonicalWorkspace != peer.Workspace {
		return ContractSpec{}, fmt.Errorf("%w: workspace must match registered owner", ErrContractValidation)
	}
	spec.Workspace = peer.Workspace
	if len(spec.Schema) == 0 || len(spec.Schema) > 128*1024 || !json.Valid(spec.Schema) {
		return bad()
	}
	var schema map[string]json.RawMessage
	if json.Unmarshal(spec.Schema, &schema) != nil || schema == nil {
		return ContractSpec{}, fmt.Errorf("%w: schema must be a JSON object", ErrContractValidation)
	}
	if len(spec.AcceptanceCriteria) == 0 || len(spec.AcceptanceCriteria) > 64 || len(spec.Artifacts) > 64 || len(spec.OwnedPaths) > 128 || len(spec.Dependencies) > 128 || len(spec.CompatibilityChanges) > 64 {
		return bad()
	}
	for _, v := range spec.AcceptanceCriteria {
		if !validContractText(v, 4096, true) {
			return bad()
		}
	}
	seen := map[string]bool{}
	paths := make([]string, 0, len(spec.OwnedPaths))
	for _, p := range spec.OwnedPaths {
		p, err := normalizeContractPath(p)
		if err != nil {
			return ContractSpec{}, err
		}
		key, err := NormalizeOwnedPath(p)
		if err != nil {
			return ContractSpec{}, err
		}
		if err := rejectOwnershipSymlinks(peer.Workspace, p); err != nil {
			return ContractSpec{}, err
		}
		if seen[key] {
			return ContractSpec{}, fmt.Errorf("%w: duplicate ownership path", ErrContractValidation)
		}
		seen[key] = true
		paths = append(paths, p)
	}
	sort.Strings(paths)
	spec.OwnedPaths = paths
	for _, v := range spec.Artifacts {
		if !validContractText(v.Name, 256, true) || !validContractText(v.MediaType, 256, false) || !validContractText(v.Digest, 512, false) {
			return bad()
		}
		if _, err := normalizeContractPath(v.Path); err != nil {
			return ContractSpec{}, err
		}
	}
	seen = map[string]bool{}
	for _, dep := range spec.Dependencies {
		if _, err := uuid.Parse(dep.ContractID); err != nil || dep.Revision < 1 || seen[dep.ContractID] {
			return bad()
		}
		seen[dep.ContractID] = true
	}
	for _, change := range spec.CompatibilityChanges {
		if (change.Kind != "additive" && change.Kind != "breaking" && change.Kind != "compatible") || !validContractText(change.FromVersion, 128, true) || !validContractText(change.ToVersion, 128, true) || !validContractText(change.Description, 4096, true) {
			return bad()
		}
	}
	data, err := json.Marshal(spec)
	if err != nil || len(data) > 512*1024 {
		return bad()
	}
	return spec, nil
}

// contractTransaction serializes every graph/ownership/readiness operation in a
// project. Different Store instances share the same database advisory lock.
// The peer row share-lock fences concurrent disable/revocation until commit.
func (s *Store) contractTransaction(ctx context.Context, peerID string) (*sql.Tx, Peer, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, Peer{}, err
	}
	peer, err := enabledPeer(ctx, tx, peerID)
	if err != nil {
		tx.Rollback()
		return nil, Peer{}, err
	}
	_, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':overkill:contracts:' || $1,0))`, peer.ProjectID)
	if err != nil {
		tx.Rollback()
		return nil, Peer{}, err
	}
	return tx, peer, nil
}

const contractColumns = `id,project_id,owner_peer_id,revision,spec,created_at,updated_at`

func scanContract(row rowScanner) (Contract, error) {
	var c Contract
	var data []byte
	if err := row.Scan(&c.ID, &c.ProjectID, &c.OwnerPeerID, &c.Revision, &data, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Contract{}, ErrNotFound
		}
		return Contract{}, err
	}
	if len(data) > 1024*1024 {
		return Contract{}, fmt.Errorf("coordination: stored contract exceeds bounded representation")
	}
	if err := json.Unmarshal(data, &c.Spec); err != nil {
		return Contract{}, err
	}
	return c, nil
}
func contractInProject(ctx context.Context, tx *sql.Tx, project, id string) (Contract, error) {
	return scanContract(tx.QueryRowContext(ctx, `SELECT `+contractColumns+` FROM coordination_contracts WHERE id=$1 AND project_id=$2`, id, project))
}
func (s *Store) ReadContract(ctx context.Context, peerID, id string) (Contract, error) {
	tx, p, err := s.contractTransaction(ctx, peerID)
	if err != nil {
		return Contract{}, err
	}
	defer tx.Rollback()
	c, err := contractInProject(ctx, tx, p.ProjectID, id)
	if err != nil {
		return Contract{}, err
	}
	return c, tx.Commit()
}
func (s *Store) ListContracts(ctx context.Context, peerID string) ([]Contract, error) {
	tx, p, err := s.contractTransaction(ctx, peerID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+contractColumns+` FROM coordination_contracts WHERE project_id=$1 ORDER BY created_at,id LIMIT 1001`, p.ProjectID)
	if err != nil {
		return nil, err
	}
	var result []Contract
	var resultBytes int
	for rows.Next() {
		c, err := scanContract(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		encoded, err := json.Marshal(c)
		if err != nil {
			rows.Close()
			return nil, err
		}
		resultBytes += len(encoded)
		if resultBytes > 1024*1024 {
			rows.Close()
			return nil, fmt.Errorf("coordination: contract listing exceeds 1 MiB; read known contract IDs individually")
		}
		result = append(result, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(result) > 1000 {
		return nil, fmt.Errorf("coordination: contract listing exceeds 1000; query scope must be narrowed")
	}
	if result == nil {
		result = []Contract{}
	}
	return result, tx.Commit()
}
func pathsOverlap(a, b string) bool {
	return a == "." || b == "." || a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
func checkContractPaths(ctx context.Context, tx *sql.Tx, project, id string, paths []string) error {
	rows, err := tx.QueryContext(ctx, `SELECT contract_id,path FROM coordination_contract_paths WHERE project_id=$1 AND contract_id<>$2 ORDER BY contract_id,path`, project, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var other, p string
		if err := rows.Scan(&other, &p); err != nil {
			return err
		}
		for _, want := range paths {
			key, err := NormalizeOwnedPath(want)
			if err != nil {
				return err
			}
			if pathsOverlap(key, p) {
				return fmt.Errorf("%w: %s overlaps %s (contract %s)", ErrOwnershipConflict, want, p, other)
			}
		}
	}
	return rows.Err()
}
func checkContractDependencies(ctx context.Context, tx *sql.Tx, project, id string, deps []ContractDependency) error {
	for _, dep := range deps {
		if dep.ContractID == id {
			return ErrDependencyCycle
		}
		upstream, err := contractInProject(ctx, tx, project, dep.ContractID)
		if err != nil {
			return err
		}
		if upstream.Revision != dep.Revision {
			return ErrConflict
		}
		var cycle bool
		err = tx.QueryRowContext(ctx, `WITH RECURSIVE reachable(id) AS (
 SELECT dependency_id FROM coordination_contract_dependencies WHERE contract_id=$1
 UNION SELECT d.dependency_id FROM coordination_contract_dependencies d JOIN reachable r ON d.contract_id=r.id)
 SELECT EXISTS(SELECT 1 FROM reachable WHERE id=$2)`, dep.ContractID, id).Scan(&cycle)
		if err != nil {
			return err
		}
		if cycle {
			return ErrDependencyCycle
		}
	}
	return nil
}
func persistContractGraph(ctx context.Context, tx *sql.Tx, c Contract) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM coordination_contract_paths WHERE contract_id=$1`, c.ID); err != nil {
		return err
	}
	for _, p := range c.Spec.OwnedPaths {
		key, err := NormalizeOwnedPath(p)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO coordination_contract_paths(contract_id,project_id,path) VALUES($1,$2,$3)`, c.ID, c.ProjectID, key); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM coordination_contract_dependencies WHERE contract_id=$1`, c.ID); err != nil {
		return err
	}
	for _, dep := range c.Spec.Dependencies {
		if _, err := tx.ExecContext(ctx, `INSERT INTO coordination_contract_dependencies(contract_id,dependency_id,revision) VALUES($1,$2,$3)`, c.ID, dep.ContractID, dep.Revision); err != nil {
			return err
		}
	}
	data, err := json.Marshal(c.Spec)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO coordination_contract_revisions(contract_id,revision,spec) VALUES($1,$2,$3)`, c.ID, c.Revision, data)
	return err
}
func (s *Store) CreateContract(ctx context.Context, peerID string, spec ContractSpec) (Contract, error) {
	tx, p, err := s.contractTransaction(ctx, peerID)
	if err != nil {
		return Contract{}, err
	}
	defer tx.Rollback()
	if err := rejectProcessContractMutation(ctx, tx, p.ProjectID); err != nil {
		return Contract{}, err
	}
	spec, err = prepareContractSpec(p, spec)
	if err != nil {
		return Contract{}, err
	}
	id := uuid.NewString()
	if err = checkContractPaths(ctx, tx, p.ProjectID, id, spec.OwnedPaths); err != nil {
		return Contract{}, err
	}
	if err = checkContractDependencies(ctx, tx, p.ProjectID, id, spec.Dependencies); err != nil {
		return Contract{}, err
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return Contract{}, err
	}
	c, err := scanContract(tx.QueryRowContext(ctx, `INSERT INTO coordination_contracts(id,project_id,owner_peer_id,revision,spec) VALUES($1,$2,$3,1,$4) RETURNING `+contractColumns, id, p.ProjectID, p.ID, data))
	if err != nil {
		return Contract{}, err
	}
	if err = persistContractGraph(ctx, tx, c); err != nil {
		return Contract{}, err
	}
	return c, tx.Commit()
}
func (s *Store) UpdateContract(ctx context.Context, peerID, id string, expectedRevision int64, spec ContractSpec) (Contract, error) {
	tx, p, err := s.contractTransaction(ctx, peerID)
	if err != nil {
		return Contract{}, err
	}
	defer tx.Rollback()
	if err := rejectProcessContractMutation(ctx, tx, p.ProjectID); err != nil {
		return Contract{}, err
	}
	old, err := contractInProject(ctx, tx, p.ProjectID, id)
	if err != nil {
		return Contract{}, err
	}
	if old.OwnerPeerID != p.ID {
		return Contract{}, ErrUnauthorized
	}
	if expectedRevision != old.Revision {
		return Contract{}, ErrConflict
	}
	spec, err = prepareContractSpec(p, spec)
	if err != nil {
		return Contract{}, err
	}
	schemaChanged := !jsonSemanticEqual(spec.Schema, old.Spec.Schema)
	endpointChanged := spec.Endpoint != old.Spec.Endpoint
	if (schemaChanged || endpointChanged) && spec.SchemaVersion == old.Spec.SchemaVersion {
		return Contract{}, fmt.Errorf("%w: endpoint/schema mutation requires a new schema_version", ErrContractValidation)
	}
	if endpointChanged || spec.SchemaVersion != old.Spec.SchemaVersion || schemaChanged {
		declared := false
		for _, change := range spec.CompatibilityChanges {
			if change.FromVersion == old.Spec.SchemaVersion && change.ToVersion == spec.SchemaVersion {
				declared = true
			}
		}
		if !declared {
			return Contract{}, fmt.Errorf("%w: schema change requires explicit compatibility change", ErrContractValidation)
		}
	}
	if err = checkContractPaths(ctx, tx, p.ProjectID, id, spec.OwnedPaths); err != nil {
		return Contract{}, err
	}
	if err = checkContractDependencies(ctx, tx, p.ProjectID, id, spec.Dependencies); err != nil {
		return Contract{}, err
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return Contract{}, err
	}
	c, err := scanContract(tx.QueryRowContext(ctx, `UPDATE coordination_contracts SET revision=revision+1,spec=$1,updated_at=NOW() WHERE id=$2 AND revision=$3 RETURNING `+contractColumns, data, id, expectedRevision))
	if err != nil {
		return Contract{}, err
	}
	if err = persistContractGraph(ctx, tx, c); err != nil {
		return Contract{}, err
	}
	return c, tx.Commit()
}
func jsonSemanticEqual(a, b json.RawMessage) bool {
	var x, y any
	left, right := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	left.UseNumber()
	right.UseNumber()
	if left.Decode(&x) != nil || right.Decode(&y) != nil {
		return false
	}
	xx, _ := json.Marshal(x)
	yy, _ := json.Marshal(y)
	return string(xx) == string(yy)
}
func (s *Store) DeleteContract(ctx context.Context, peerID, id string, expectedRevision int64) error {
	tx, p, err := s.contractTransaction(ctx, peerID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := rejectProcessContractMutation(ctx, tx, p.ProjectID); err != nil {
		return err
	}
	c, err := contractInProject(ctx, tx, p.ProjectID, id)
	if err != nil {
		return err
	}
	if c.OwnerPeerID != p.ID {
		return ErrUnauthorized
	}
	if c.Revision != expectedRevision {
		return ErrConflict
	}
	var depended bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_contract_dependencies WHERE dependency_id=$1)`, id).Scan(&depended); err != nil {
		return err
	}
	if depended {
		return fmt.Errorf("%w: contract still has dependents", ErrConflict)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM coordination_contracts WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) AcknowledgeContract(ctx context.Context, peerID, id string, expectedRevision int64) (ContractAcknowledgement, error) {
	tx, p, err := s.contractTransaction(ctx, peerID)
	if err != nil {
		return ContractAcknowledgement{}, err
	}
	defer tx.Rollback()
	c, err := contractInProject(ctx, tx, p.ProjectID, id)
	if err != nil {
		return ContractAcknowledgement{}, err
	}
	if c.Revision != expectedRevision {
		return ContractAcknowledgement{}, ErrConflict
	}
	var result ContractAcknowledgement
	err = tx.QueryRowContext(ctx, `INSERT INTO coordination_contract_acknowledgements(contract_id,revision,peer_id) VALUES($1,$2,$3)
 ON CONFLICT(contract_id,revision,peer_id) DO UPDATE SET acknowledged_at=coordination_contract_acknowledgements.acknowledged_at
 RETURNING contract_id,revision,peer_id,acknowledged_at`, id, expectedRevision, p.ID).Scan(&result.ContractID, &result.Revision, &result.PeerID, &result.AcknowledgedAt)
	if err != nil {
		return ContractAcknowledgement{}, err
	}
	return result, tx.Commit()
}
