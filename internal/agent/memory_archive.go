package agent

import (
	"context"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// MemoryArchiver is the tiny interface the agent uses to push evicted
// conversation turns into cold storage (master plan §6.1 hot/cold
// paging). The real implementation embeds + stores via the Python
// bridge; tests inject a no-op.
//
// Best-effort semantics: errors are emitted as events, not surfaced as
// compaction failures. Losing one archival write is preferable to
// blocking the agent on a flaky vector store.
type MemoryArchiver interface {
	Archive(ctx context.Context, sessionID, role, content string) error
}

// SetMemoryArchiver wires the archiver consulted during compaction.
// Pass nil to disable. Per-message size filter applied internally so
// callers don't need to pre-filter.
func (a *Agent) SetMemoryArchiver(ar MemoryArchiver) {
	a.mu.Lock()
	a.memoryArchiver = ar
	a.mu.Unlock()
}

// archiveCompactedMessages writes each evicted message to cold storage
// so a future memory_search can retrieve the original detail. Skips:
//   - prior compaction summaries (avoid double-archiving)
//   - very short content (< 32 chars after trim — likely noise)
//   - tool messages (already retained as part of the LLM result history)
//   - empty / whitespace-only content
//
// Each Archive call gets a fresh 5s timeout derived from the agent's
// session context. Per-call failures are emitted as events but don't
// stop the loop — partial archival is better than none.
func (a *Agent) archiveCompactedMessages(history []providers.Message) {
	a.mu.RLock()
	ar := a.memoryArchiver
	parent := a.sessionCtx
	sid := a.sessionID
	a.mu.RUnlock()
	if ar == nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}

	for _, msg := range history {
		content := strings.TrimSpace(msg.Content)
		if len(content) < 32 {
			continue
		}
		if msg.Role == "tool" {
			continue
		}
		if strings.HasPrefix(content, "[compacted history]") {
			continue
		}

		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		err := ar.Archive(ctx, sid, msg.Role, content)
		cancel()
		if err != nil {
			a.emit("memory_archive_failed", map[string]any{
				"role":       msg.Role,
				"chars":      len(content),
				"error":      err.Error(),
				"session_id": sid,
			})
			continue
		}
		a.emit("memory_archived", map[string]any{
			"role":       msg.Role,
			"chars":      len(content),
			"session_id": sid,
		})
	}
}
