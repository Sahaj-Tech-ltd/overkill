package coordination

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type contractClaim struct{ path, owner string }

func projectClaims(ctx context.Context, tx *sql.Tx, project string) ([]contractClaim, error) {
	rows, err := tx.QueryContext(ctx, `SELECT p.path,c.owner_peer_id FROM coordination_contract_paths p JOIN coordination_contracts c ON c.id=p.contract_id WHERE p.project_id=$1`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var claims []contractClaim
	for rows.Next() {
		var c contractClaim
		if err := rows.Scan(&c.path, &c.owner); err != nil {
			return nil, err
		}
		claims = append(claims, c)
	}
	return claims, rows.Err()
}
func ownedPathAccess(ctx context.Context, tx *sql.Tx, p Peer, paths []string) error {
	if len(paths) == 0 || len(paths) > 128 {
		return fmt.Errorf("%w: guard requires 1..128 paths", ErrContractValidation)
	}
	keys := make([]string, 0, len(paths))
	for _, file := range paths {
		clean, err := normalizeContractPath(file)
		if err != nil {
			return err
		}
		if err := rejectOwnershipSymlinks(p.Workspace, clean); err != nil {
			return err
		}
		key, err := NormalizeOwnedPath(clean)
		if err != nil {
			return err
		}
		keys = append(keys, key)
	}
	claims, err := projectClaims(ctx, tx, p.ProjectID)
	if err != nil {
		return err
	}
	// No claims is an explicit legacy mode, not an implicit reservation.
	if len(claims) == 0 {
		return nil
	}
	for _, key := range keys {
		owned := false
		for _, claim := range claims {
			if claim.owner == p.ID && (claim.path == "." || claim.path == key || hasPathPrefix(key, claim.path)) {
				owned = true
			}
			if claim.owner != p.ID && pathsOverlap(key, claim.path) {
				return fmt.Errorf("%w: %s overlaps another peer's reservation", ErrOwnershipConflict, key)
			}
		}
		if !owned {
			return fmt.Errorf("%w: %s is unclaimed; create an ownership contract before writing", ErrOwnershipConflict, key)
		}
	}
	return nil
}
func hasPathPrefix(child, parent string) bool {
	return len(child) > len(parent) && child[:len(parent)] == parent && child[len(parent)] == '/'
}

// WithOwnedPaths holds the same project advisory lock as contract mutation
// until fn returns. Callback side effects are not rolled back by PostgreSQL;
// the lock prevents claim changes during this synchronous operation. Callbacks
// must not recursively call contract APIs on another database connection.
func (s *Store) WithOwnedPaths(ctx context.Context, peerID string, paths []string, fn func(context.Context) error) error {
	tx, p, err := s.contractTransaction(ctx, peerID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validateExecutionContracts(ctx, tx, p); err != nil {
		return err
	}
	if err := ownedPathAccess(ctx, tx, p, paths); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("%w: callback required", ErrContractValidation)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := fn(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

// WithUnrestrictedWorkspace permits opaque effectful tools when every foreign
// project reservation is in a physically disjoint registered workspace. Same
// or nested roots remain blocked, including disabled owners' reservations.
// This application-level guard holds the project lock through fn; it does not
// sandbox arbitrary commands or prevent filesystem changes outside that lock.
func (s *Store) WithUnrestrictedWorkspace(ctx context.Context, peerID string, fn func(context.Context) error) error {
	tx, p, err := s.contractTransaction(ctx, peerID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validateExecutionContracts(ctx, tx, p); err != nil {
		return err
	}
	if err := rejectOwnershipSymlinks(p.Workspace, "."); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT owner.workspace FROM coordination_contract_paths paths
 JOIN coordination_contracts contract ON contract.id=paths.contract_id
 JOIN coordination_peers owner ON owner.id=contract.owner_peer_id
 WHERE paths.project_id=$1 AND owner.id<>$2`, p.ProjectID, p.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var workspace string
		if err := rows.Scan(&workspace); err != nil {
			return err
		}
		if err := rejectOwnershipSymlinks(workspace, "."); err != nil {
			return err
		}
		if workspaceRootsOverlap(p.Workspace, workspace) {
			return fmt.Errorf("%w: opaque mutation requires a workspace disjoint from other peers' reservations", ErrOwnershipConflict)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("%w: callback required", ErrContractValidation)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := fn(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

// Fold conservatively so a case-sensitive host cannot approve roots which
// alias on a case-insensitive or Unicode-normalizing filesystem.
func workspaceRootsOverlap(a, b string) bool {
	key := func(root string) string {
		return strings.TrimRight(norm.NFC.String(cases.Fold().String(norm.NFC.String(filepath.ToSlash(root)))), "/")
	}
	a, b = key(a), key(b)
	return a == "" || b == "" || a == b || hasPathPrefix(a, b) || hasPathPrefix(b, a)
}
func (s *Store) CheckOwnedPaths(ctx context.Context, peerID string, paths []string) error {
	return s.WithOwnedPaths(ctx, peerID, paths, func(context.Context) error { return nil })
}
