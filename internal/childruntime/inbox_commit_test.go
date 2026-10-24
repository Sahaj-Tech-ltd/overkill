package childruntime

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

type inboxCommitProvider struct{ scriptedProvider }

func (p *inboxCommitProvider) Stream(ctx context.Context, req providers.Request) (<-chan providers.Chunk, error) {
	response, err := p.Complete(ctx, req)
	if err != nil {
		return nil, err
	}
	ch := make(chan providers.Chunk, 1)
	ch <- providers.Chunk{Content: response.Content, Done: true, Usage: &providers.Usage{}}
	close(ch)
	return ch, nil
}

// A deferred constraint trigger fails at the real PostgreSQL commit, after
// the context callback has run. The same live agent is then retried: it must
// neither spend on the uncertain turn nor retain a duplicate actionable copy.
func TestInboxCommitFailureStopsRunAndStreamAndRetriesWithOneCopy(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint("stream=", streaming), func(t *testing.T) {
			db, _ := testpg.Open(t)
			store, err := subagent.NewCommunicationStore(t.Context(), db, "inbox-commit")
			require.NoError(t, err)
			require.NoError(t, store.RegisterBatch(t.Context(), []subagent.WorkerRecord{{ID: "backend", Name: "backend", Goal: "Send the schema"}}))
			message, err := store.Send(t.Context(), "backend", subagent.ParentWorker, "update", "schema-commit-fixture", "schema", "")
			require.NoError(t, err)
			_, err = db.ExecContext(t.Context(), `CREATE FUNCTION reject_inbox_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced inbox commit failure'; END $$`)
			require.NoError(t, err)
			_, err = db.ExecContext(t.Context(), `CREATE CONSTRAINT TRIGGER reject_inbox_commit AFTER UPDATE ON worker_messages DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_inbox_commit()`)
			require.NoError(t, err)
			provider := &inboxCommitProvider{scriptedProvider{complete: func(_ context.Context, req providers.Request, _ int) (providers.Response, error) {
				copies := 0
				for _, m := range req.Messages {
					copies += strings.Count(m.Content, "schema-commit-fixture")
				}
				require.Equal(t, 1, copies, "a failed admission cannot leak into a later provider request")
				return providers.Response{Content: "Inspected the update."}, nil
			}}}
			a := agent.New(agent.Config{Provider: provider, Model: "test", MaxTokens: 4096, MaxSteps: 2, Tools: tools.NewRegistry(), BoundaryInbox: WorkerInbox(store, subagent.ParentWorker)})
			defer a.Shutdown()
			run := func() error {
				if !streaming {
					_, err := a.Run(t.Context(), "Inspect backend updates")
					return err
				}
				events, err := a.Stream(t.Context(), "Inspect backend updates")
				if err != nil {
					return err
				}
				for event := range events {
					if event.Type == agent.EventError {
						err = event.Error
					}
				}
				return err
			}
			require.ErrorContains(t, run(), "forced inbox commit failure")
			require.Zero(t, provider.calls.Load(), "no model request after uncertain delivery commit")
			for _, m := range a.History() {
				require.NotContains(t, m.Content, "schema-commit-fixture")
			}
			inbox, err := store.Inbox(t.Context(), subagent.ParentWorker, 0)
			require.NoError(t, err)
			require.Len(t, inbox, 1)
			require.Equal(t, "accepted", inbox[0].State)
			_, err = db.ExecContext(t.Context(), `DROP TRIGGER reject_inbox_commit ON worker_messages`)
			require.NoError(t, err)
			require.NoError(t, run())
			require.Equal(t, int32(1), provider.calls.Load())
			inbox, err = store.Inbox(t.Context(), subagent.ParentWorker, 0)
			require.NoError(t, err)
			require.Equal(t, message.ID, inbox[0].ID)
			require.Equal(t, "admitted", inbox[0].State)
		})
	}
}
