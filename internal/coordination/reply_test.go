package coordination

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplyAndRequestCompletionAreAtomic(t *testing.T) {
	store, frontend, backend, _ := fixture(t)
	ctx := t.Context()
	message, err := store.Send(ctx, frontend.ID, request("question"))
	require.NoError(t, err)
	_, err = store.Claim(ctx, backend.ID)
	require.NoError(t, err)
	_, err = store.db.ExecContext(ctx, `CREATE FUNCTION reject_completion() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.type='completed' THEN RAISE EXCEPTION 'fixture completion unavailable'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER reject_completion BEFORE INSERT ON coordination_events FOR EACH ROW EXECUTE FUNCTION reject_completion()`)
	require.NoError(t, err)
	_, err = store.Reply(ctx, backend.ID, message.ID, json.RawMessage(`{"ready":true}`), "answer")
	require.ErrorContains(t, err, "fixture completion unavailable")
	original, err := store.Read(ctx, frontend.ID, message.ID)
	require.NoError(t, err)
	require.Equal(t, Started, original.State)
	inbox, err := store.Inbox(ctx, frontend.ID, 10)
	require.NoError(t, err)
	require.Empty(t, inbox, "failed completion cannot publish an orphan answer")
	_, err = store.db.ExecContext(ctx, `DROP TRIGGER reject_completion ON coordination_events`)
	require.NoError(t, err)
	answer, err := store.Reply(ctx, backend.ID, message.ID, json.RawMessage(`{"ready":true}`), "answer")
	require.NoError(t, err)
	original, err = store.Read(ctx, frontend.ID, message.ID)
	require.NoError(t, err)
	require.Equal(t, Completed, original.State)
	retry, err := store.Reply(ctx, backend.ID, message.ID, json.RawMessage(`{"ready":true}`), "answer")
	require.NoError(t, err)
	require.Equal(t, answer.ID, retry.ID)
	events, err := store.Events(ctx, frontend.ID, 0, 20)
	require.NoError(t, err)
	require.Len(t, events, 4, "retry does not emit duplicate receipts or completion")
}

func TestCancelledRequestRejectsLateAnswer(t *testing.T) {
	store, frontend, backend, _ := fixture(t)
	ctx := t.Context()
	message, err := store.Send(ctx, frontend.ID, request("question"))
	require.NoError(t, err)
	_, err = store.Transition(ctx, frontend.ID, message.ID, message.Revision, "cancelled", json.RawMessage(`{}`))
	require.NoError(t, err)
	_, err = store.Reply(ctx, backend.ID, message.ID, json.RawMessage(`{"late":true}`), "answer")
	require.ErrorContains(t, err, "cancelled")
	inbox, err := store.Inbox(ctx, frontend.ID, 10)
	require.NoError(t, err)
	require.Empty(t, inbox)
}
