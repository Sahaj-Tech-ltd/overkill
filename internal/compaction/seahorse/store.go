package seahorse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

var ErrConflict = errors.New("seahorse: session revision changed")
var ErrNotFound = errors.New("seahorse: memory record not found")

// Source retains the complete provider message, including tool arguments,
// results and attachments. IDs are content hashes, scoped by the store session.
type Source struct {
	ID      string            `json:"id"`
	Message providers.Message `json:"message"`
}

// Node is immutable. Ordered source/parent IDs preserve chronology and ancestry;
// repeated identical messages retain repeated IDs in the ordered source list.
type Node struct {
	Summary   Summary  `json:"summary"`
	SourceIDs []string `json:"source_ids,omitempty"`
	Hash      string   `json:"hash"`
}
type Snapshot struct {
	Revision  int64               `json:"revision"`
	HeadID    string              `json:"head_id"`
	InputHash string              `json:"input_hash"`
	Output    []providers.Message `json:"output"`
}

// Store commits raw evidence, all new nodes and the session head atomically.
// Implementations must reject stale revisions and cross-session ancestry.
type Store interface {
	Load(context.Context, string) (Snapshot, error)
	Commit(context.Context, string, int64, []Source, []Node, Snapshot) error
	Node(context.Context, string, string) (Node, error)
	Source(context.Context, string, string) (Source, error)
}

func digest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func sourceFor(message providers.Message) Source {
	return Source{ID: digest(message), Message: message}
}
func sealNode(node Node) Node {
	node.Hash = ""
	node.Summary.SummaryID = ""
	node.Hash = digest(node)
	node.Summary.SummaryID = node.Hash
	return node
}
func validateNode(node Node) error {
	if node.Summary.Depth > 64 || node.Summary.Depth < 0 {
		return fmt.Errorf("seahorse: node depth exceeds safety bound")
	}
	original := node.Hash
	sealed := sealNode(node)
	if original == "" || sealed.Hash != original || node.Summary.SummaryID != original {
		return fmt.Errorf("seahorse: corrupt node hash")
	}
	if node.Summary.Kind == KindLeaf && (len(node.SourceIDs) == 0 || len(node.Summary.ParentIDs) != 0 || node.Summary.Depth != 0) {
		return fmt.Errorf("seahorse: invalid leaf lineage")
	}
	if node.Summary.Kind == KindCondensed && (len(node.SourceIDs) != 0 || len(node.Summary.ParentIDs) < 2 || node.Summary.Depth < 1) {
		return fmt.Errorf("seahorse: invalid condensed lineage")
	}
	if node.Summary.Kind != KindLeaf && node.Summary.Kind != KindCondensed {
		return fmt.Errorf("seahorse: invalid node kind")
	}
	return nil
}

func digestBytes(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
