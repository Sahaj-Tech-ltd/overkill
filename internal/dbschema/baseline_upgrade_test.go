package dbschema_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cron"
	"github.com/Sahaj-Tech-ltd/overkill/internal/daemon"
	appdb "github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/learning"
	"github.com/Sahaj-Tech-ltd/overkill/internal/memory"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type historicalDDL struct {
	Version int
	Commit  string
	Sources []struct {
		Path         string
		GitBlob      string `json:"git_blob"`
		SourceSHA256 string `json:"source_sha256"`
		Statements   []struct {
			StartLine        int `json:"start_line"`
			EndLine          int `json:"end_line"`
			SHA256           string
			ExpectedSQLState string `json:"expected_sqlstate"`
			SQL              string
		}
	}
}

func installSourceBaseline(t *testing.T, db *sql.DB) {
	t.Helper()
	raw, err := os.ReadFile("testdata/baseline-34b3fab.json")
	require.NoError(t, err)
	var fixture historicalDDL
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.Equal(t, "34b3fab9d33bff34e0b00ae7e120498e0e6a79ee", fixture.Commit)
	require.Len(t, fixture.Sources, 8)
	statements, expectedFailures := 0, 0
	for _, source := range fixture.Sources {
		require.Len(t, source.GitBlob, 40)
		require.Len(t, source.SourceSHA256, 64)
		for _, statement := range source.Statements {
			sum := sha256.Sum256([]byte(statement.SQL))
			require.Equal(t, statement.SHA256, hex.EncodeToString(sum[:]))
			require.Positive(t, statement.StartLine)
			require.GreaterOrEqual(t, statement.EndLine, statement.StartLine)
			statements++
			_, err = db.ExecContext(t.Context(), statement.SQL)
			if statement.ExpectedSQLState != "" {
				var pg *pq.Error
				require.ErrorAs(t, err, &pg)
				require.Equal(t, statement.ExpectedSQLState, string(pg.Code))
				expectedFailures++
			} else {
				require.NoError(t, err, "%s:%d", source.Path, statement.StartLine)
			}
		}
	}
	require.Equal(t, 50, statements)
	require.Equal(t, 1, expectedFailures, "historical cost constructor's invalid index is a partial-DDL fixture, not claimed healthy historical startup")
	_, err = db.ExecContext(t.Context(), `INSERT INTO sessions(id,title,folder,messages,metadata,token_count,created_at,updated_at) VALUES ('legacy-session','legacy title','/legacy','[{"role":"user","content":"old prompt"}]','{"origin":"baseline"}',42,'2020-01-02T03:04:05Z','2020-01-02T03:04:05Z');
 INSERT INTO session_router(channel,chat_key,thread,session_id,follow) VALUES ('fixture','old-chat','thread','legacy-session','');
 INSERT INTO goals(session_id,text,token_budget,tokens_used,time_budget_seconds,time_used_seconds,status,created_at,updated_at) VALUES ('legacy-session','legacy objective',100,7,60,2,'active','2020-01-02T03:04:05Z','2020-01-02T03:04:05Z');
 INSERT INTO cost_records(id,session_id,input_tokens,output_tokens,cached_tokens,cost_usd) VALUES ('legacy-cost','legacy-session',12,3,4,0.125);
 INSERT INTO corrections(context,wrong,correct,timestamp) VALUES ('legacy keyword','wrong','retained correction',1234);
 INSERT INTO cron_jobs(id,name,schedule,session_id,command,metadata) VALUES ('legacy-cron','legacy job','* * * * *','legacy-session','no execution','{"origin":"baseline"}');
 INSERT INTO cron_run_logs(job_id,output,success) VALUES ('legacy-cron','retained run output',true);
 INSERT INTO daemon_jobs(id,session_id,intent,status) VALUES ('legacy-daemon','legacy-session','retained daemon intent','queued');
 INSERT INTO memory_items(id,type,content,tags,session_id,metadata) VALUES ('legacy-memory','fact','retained memory','["legacy"]','legacy-session','{"origin":"baseline"}');
 INSERT INTO memory_entries(key,value) VALUES ('legacy-key','{"retained":true}');`)
	require.NoError(t, err)
}

type migrationOwners struct {
	sessions *session.PostgresStore
	goals    *agent.GoalStore
	costs    *cost.PostgresTracker
	learning *learning.Store
	cron     *cron.PostgresJobStore
	daemon   *daemon.JobStore
	memory   *memory.PostgresStore
}

func openMigrationOwners(t *testing.T, db *sql.DB) migrationOwners {
	t.Helper()
	require.NoError(t, appdb.Migrate(db))
	var stores migrationOwners
	var err error
	stores.sessions, err = session.NewPostgresStore(db)
	require.NoError(t, err)
	stores.goals, err = agent.NewGoalStore(db)
	require.NoError(t, err)
	stores.costs, err = cost.NewPostgresTracker(db, config.CostConfig{})
	require.NoError(t, err)
	stores.learning, err = learning.NewStore(db, 1000)
	require.NoError(t, err)
	stores.cron, err = cron.NewPostgresJobStore(db)
	require.NoError(t, err)
	stores.daemon, err = daemon.NewJobStore(db)
	require.NoError(t, err)
	stores.memory, err = memory.NewPostgresStore(db)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, stores.memory.Close()) })
	return stores
}
func assertHistoricalRows(t *testing.T, db *sql.DB, s migrationOwners) {
	t.Helper()
	old, err := s.sessions.Load(t.Context(), "legacy-session")
	require.NoError(t, err)
	require.Equal(t, "legacy title", old.Title)
	require.Equal(t, "/legacy", old.Folder)
	require.Equal(t, "2020-01-02T03:04:05Z", old.CreatedAt.UTC().Format(time.RFC3339))
	require.Equal(t, "2020-01-02T03:04:05Z", old.UpdatedAt.UTC().Format(time.RFC3339))
	require.EqualValues(t, 42, old.TokenCount)
	require.Equal(t, "old prompt", old.Messages[0].Content)
	require.Equal(t, "baseline", old.Metadata["origin"])
	route, err := s.sessions.Resolve(t.Context(), "fixture", "old-chat", "thread", "")
	require.NoError(t, err)
	require.Equal(t, old.ID, route)
	goal, err := s.goals.Get(t.Context(), old.ID)
	require.NoError(t, err)
	require.Equal(t, "legacy objective", goal.Text)
	require.Equal(t, 100, goal.TokenBudget)
	require.Equal(t, 7, goal.TokensUsed)
	require.Equal(t, int64(2_000_000_000), goal.TimeUsedNanoseconds)
	require.NotEmpty(t, goal.ID)
	recreated, err := agent.NewGoalStore(db)
	require.NoError(t, err)
	same, err := recreated.Get(t.Context(), old.ID)
	require.NoError(t, err)
	require.Equal(t, goal.ID, same.ID)
	require.Equal(t, goal.CreatedAt, same.CreatedAt)
	summary, err := s.costs.SessionCost(t.Context(), old.ID)
	require.NoError(t, err)
	require.Equal(t, .125, summary.TotalUSD)
	require.EqualValues(t, 12, summary.InputTokens)
	corrections, err := s.learning.FindCorrections("legacy keyword", 10)
	require.NoError(t, err)
	require.Len(t, corrections, 1)
	require.Equal(t, "retained correction", corrections[0].Correct)
	jobs, err := s.cron.LoadJobs(t.Context())
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "legacy-cron", jobs[0].ID)
	require.Equal(t, "baseline", jobs[0].Metadata["origin"])
	logs, err := s.cron.GetRunLogs(t.Context(), "legacy-cron", 10)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Equal(t, "retained run output", logs[0].Output)
	job, err := s.daemon.Get(t.Context(), "legacy-daemon")
	require.NoError(t, err)
	require.Equal(t, "retained daemon intent", job.Intent)
	fact, err := s.memory.Get(t.Context(), "legacy-memory")
	require.NoError(t, err)
	require.Equal(t, "retained memory", fact.Content)
	require.Equal(t, []string{"legacy"}, fact.Tags)
	var hash sql.NullString
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT content_hash FROM memory_items WHERE id='legacy-memory'`).Scan(&hash))
	require.False(t, hash.Valid, "no unsupported automatic legacy hash backfill")
	var state string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT value::text FROM memory_entries WHERE key='legacy-key'`).Scan(&state))
	require.JSONEq(t, `{"retained":true}`, state)
}
func exerciseMigratedOwners(t *testing.T, db *sql.DB, s migrationOwners) {
	t.Helper()
	ctx := t.Context()
	parent := session.NewSession("/post-upgrade")
	parent.Messages = []providers.Message{{Role: "user", Content: "one"}, {Role: "assistant", Content: "two"}}
	require.NoError(t, s.sessions.Create(ctx, parent))
	for _, title := range []string{"first save", "second save"} {
		parent.Title = title
		require.NoError(t, s.sessions.Save(ctx, parent))
		loaded, err := s.sessions.Load(ctx, parent.ID)
		require.NoError(t, err)
		require.Equal(t, title, loaded.Title)
		require.True(t, parent.UpdatedAt.Equal(loaded.UpdatedAt))
	}
	require.NoError(t, s.sessions.Bind(ctx, "fixture", "new-chat", "thread", parent.ID))
	route, err := s.sessions.Resolve(ctx, "fixture", "new-chat", "thread", "")
	require.NoError(t, err)
	require.Equal(t, parent.ID, route)
	child, err := s.sessions.Branch(ctx, parent.ID, 2)
	require.NoError(t, err)
	child.Messages = append(child.Messages, providers.Message{Role: "assistant", Content: "branch addition"})
	require.NoError(t, s.sessions.Save(ctx, child))
	merged, err := s.sessions.Merge(ctx, child.ID)
	require.NoError(t, err)
	require.Equal(t, parent.ID, merged.ID)
	require.Len(t, merged.Messages, 3)
	merged.Title = "saved merged parent"
	require.NoError(t, s.sessions.Save(ctx, merged))
	cloned, err := s.sessions.Clone(ctx, parent.ID)
	require.NoError(t, err)
	cloned.Title = "saved clone"
	require.NoError(t, s.sessions.Save(ctx, cloned))
	require.NoError(t, s.goals.SetWithBudgets(ctx, parent.ID, "new budget", 10, 30))
	tokens, seconds, exhausted, err := s.goals.UpdateUsageAtomic(ctx, parent.ID, 6, 2)
	require.NoError(t, err)
	require.Equal(t, 6, tokens)
	require.Equal(t, 2, seconds)
	require.False(t, exhausted)
	tokens, _, exhausted, err = s.goals.UpdateUsageAtomic(ctx, parent.ID, 5, 1)
	require.NoError(t, err)
	require.Equal(t, 11, tokens)
	require.True(t, exhausted)
	require.NoError(t, s.costs.Record(ctx, cost.Entry{ID: "post-upgrade-cost", SessionID: parent.ID, InputTokens: 1 << 33, OutputTokens: 3, CachedTokens: 2, CostUSD: .25}))
	summary, err := s.costs.SessionCost(ctx, parent.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1<<33, summary.InputTokens)
	require.Equal(t, .25, summary.TotalUSD)
	correction := learning.NewCorrection("new keyword", "old instruction", "new instruction")
	require.NoError(t, s.learning.Save(correction))
	correction.Correct = "updated instruction"
	require.NoError(t, s.learning.Save(correction))
	found, err := s.learning.FindCorrections("new keyword", 5)
	require.NoError(t, err)
	require.NotEmpty(t, found)
	require.Equal(t, "updated instruction", found[0].Correct)
	cronJob := &cron.Job{ID: "post-upgrade-cron", Name: "new job", Schedule: "* * * * *", SessionID: parent.ID, Status: cron.StatusActive, CreatedAt: time.Now().UTC()}
	require.NoError(t, s.cron.SaveJob(ctx, cronJob))
	require.NoError(t, s.cron.LogRun(ctx, cronJob.ID, "new output", "", true))
	runLogs, err := s.cron.GetRunLogs(ctx, cronJob.ID, 10)
	require.NoError(t, err)
	require.Len(t, runLogs, 1)
	require.Equal(t, "new output", runLogs[0].Output)
	daemonJob := daemon.NewJob("new daemon intent", "fixture", "chat", "")
	require.NoError(t, s.daemon.Create(ctx, daemonJob))
	require.NoError(t, s.daemon.UpdateStatus(ctx, daemonJob.ID, daemon.JobRunning, ""))
	require.NoError(t, s.daemon.Cancel(ctx, daemonJob.ID))
	cancelled, err := s.daemon.Get(ctx, daemonJob.ID)
	require.NoError(t, err)
	require.Equal(t, daemon.JobCancelled, cancelled.Status)
	require.NoError(t, s.memory.Store(ctx, &memory.Memory{ID: "new-memory", Content: "new deduplicated memory"}))
	require.NoError(t, s.memory.Store(ctx, &memory.Memory{ID: "duplicate-memory", Content: "new deduplicated memory"}))
	var memories int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM memory_items WHERE content='new deduplicated memory'`).Scan(&memories))
	require.Equal(t, 1, memories)
	// Reload each owner again after real writes, before retention intentionally
	// deletes older root sessions and their routing bindings.
	reopened := openMigrationOwners(t, db)
	loaded, err := reopened.sessions.Load(ctx, parent.ID)
	require.NoError(t, err)
	require.Len(t, loaded.Messages, 3)
	goal, err := reopened.goals.Get(ctx, parent.ID)
	require.NoError(t, err)
	require.Equal(t, 11, goal.TokensUsed)
	require.Equal(t, int64(3_000_000_000), goal.TimeUsedNanoseconds)
	for range 5 {
		require.NoError(t, s.sessions.Create(ctx, session.NewSession("/retention")))
	}
	all, err := s.sessions.List(ctx, session.ListOptions{OldestFirst: true})
	require.NoError(t, err)
	var roots []*session.Session
	for _, item := range all {
		if item.ParentID == "" {
			roots = append(roots, item)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].UpdatedAt.Equal(roots[j].UpdatedAt) {
			return roots[i].ID < roots[j].ID
		}
		return roots[i].UpdatedAt.Before(roots[j].UpdatedAt)
	})
	deleted, err := session.EnforceMax(ctx, s.sessions, 2)
	require.NoError(t, err)
	require.Equal(t, len(roots)-2, deleted)
	for i, root := range roots {
		_, err := s.sessions.Load(ctx, root.ID)
		if i < len(roots)-2 {
			require.ErrorIs(t, err, session.ErrNotFound)
		} else {
			require.NoError(t, err)
		}
	}
	for _, sub := range []*session.Session{child, cloned} {
		preserved, err := s.sessions.Load(ctx, sub.ID)
		require.NoError(t, err)
		require.Empty(t, preserved.ParentID, "existing delete policy promotes, never prunes initial sub-sessions")
	}
	route, err = s.sessions.Resolve(ctx, "fixture", "new-chat", "thread", "")
	require.NoError(t, err)
	require.Empty(t, route, "retention deletion cleans root routing bindings")
}
func TestAuditMigrationFreshAndSourceBaselineLifecycle(t *testing.T) {
	for _, historical := range []bool{false, true} {
		name := "fresh"
		if historical {
			name = "source_34b3fab"
		}
		t.Run(name, func(t *testing.T) {
			db, _ := testpg.Open(t)
			if historical {
				installSourceBaseline(t, db)
			}
			owners := openMigrationOwners(t, db)
			require.NoError(t, owners.memory.Close())
			owners = openMigrationOwners(t, db)
			if historical {
				assertHistoricalRows(t, db, owners)
			}
			exerciseMigratedOwners(t, db, owners)
		})
	}
}
