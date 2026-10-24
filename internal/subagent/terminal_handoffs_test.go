package subagent

import (
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/terminal"
	"github.com/stretchr/testify/require"
)

func TestTerminalHandoffJournalBindsIntentScopeAndRuntime(t *testing.T) {
	s := communicationFixture(t)
	h := terminal.Handoff{ID: "handoff-one", TerminalID: "terminal-one", From: terminal.Owner{Session: s.scope, Worker: "front-id"}, To: terminal.Owner{Session: s.scope, Worker: "main"}, PreviousRevision: 1, ControlRevision: 2, Cursor: 123, State: "prepared", CreatedAt: time.Now().UTC()}
	require.NoError(t, s.RecordTerminalHandoff(t.Context(), h))
	changed := h
	changed.State = "applied"
	changed.TerminalID = "different"
	require.Error(t, s.RecordTerminalHandoff(t.Context(), changed))
	changed = h
	changed.To.Session = "foreign"
	require.Error(t, s.RecordTerminalHandoff(t.Context(), changed))
	h.State = "applied"
	require.NoError(t, s.RecordTerminalHandoff(t.Context(), h))
	require.NoError(t, s.RecordTerminalHandoff(t.Context(), h))
	records, err := s.TerminalHandoffs(t.Context())
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "applied", records[0].Receipt.State)
	require.True(t, records[0].CurrentRuntime)
	restarted, err := NewCommunicationStore(t.Context(), s.db, s.scope)
	require.NoError(t, err)
	records, err = restarted.TerminalHandoffs(t.Context())
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.False(t, records[0].CurrentRuntime, "history is not a process reattachment claim")
	require.Error(t, restarted.RecordTerminalHandoff(t.Context(), h), "new incarnation cannot finalize old ownership")
}

func TestHumanHandoffNoticeSharesTheAppliedReceiptTransaction(t *testing.T) {
	s := communicationFixture(t)
	h := terminal.Handoff{ID: "human-handoff", TerminalID: "terminal", From: terminal.Owner{Session: s.scope, Worker: "front-id"}, To: terminal.Owner{Session: s.scope, Worker: "main"}, PreviousRevision: 1, ControlRevision: 2, State: "prepared", Initiator: "human", CreatedAt: time.Now().UTC()}
	require.NoError(t, s.RecordTerminalHandoff(t.Context(), h))
	messages, err := s.Inbox(t.Context(), "front-id", 0)
	require.NoError(t, err)
	require.Empty(t, messages)
	h.State = "applied"
	require.NoError(t, s.RecordTerminalHandoff(t.Context(), h))
	require.NoError(t, s.RecordTerminalHandoff(t.Context(), h))
	messages, err = s.Inbox(t.Context(), "front-id", 0)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, ParentWorker, messages[0].From)
	require.Equal(t, "accepted", messages[0].State)
	require.Contains(t, messages[0].Content, "do not restart or replay")
	require.NoError(t, s.Admit(t.Context(), "front-id", func(batch []WorkerMessage) error { require.Len(t, batch, 1); return nil }))
	messages, err = s.Inbox(t.Context(), "front-id", 0)
	require.NoError(t, err)
	require.Equal(t, "admitted", messages[0].State)
}
