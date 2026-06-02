package daemon

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	connStr := os.Getenv("PG_TEST_URL")
	if connStr == "" {
		connStr = os.Getenv("DATABASE_URL")
	}
	if connStr == "" {
		t.Skip("skipping: set PG_TEST_URL or DATABASE_URL")
	}
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestJobStore_CreateGet_roundtrip(t *testing.T) {
	store, _ := NewJobStore(openTestDB(t))
	ctx := context.Background()

	// Arrange
	job := NewJob("write a haiku", "discord", "key123", "remote")

	// Act
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, job.ID)

	// Assert
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != job.ID {
		t.Errorf("ID = %q, want %q", got.ID, job.ID)
	}
	if got.Intent != job.Intent {
		t.Errorf("Intent = %q, want %q", got.Intent, job.Intent)
	}
	if got.Status != JobQueued {
		t.Errorf("Status = %q, want %q", got.Status, JobQueued)
	}
	if got.Profile != "remote" {
		t.Errorf("Profile = %q, want remote", got.Profile)
	}
}

func TestJobStore_Create_requiresID(t *testing.T) {
	store, _ := NewJobStore(openTestDB(t))
	job := Job{Intent: "no id"}
	if err := store.Create(context.Background(), job); err == nil {
		t.Error("expected error for job with empty ID")
	}
}

func TestJobStore_Get_notFound(t *testing.T) {
	store, _ := NewJobStore(openTestDB(t))
	_, err := store.Get(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for missing job")
	}
}

func TestWorker_submit_transitions_running_then_completed(t *testing.T) {
	store, _ := NewJobStore(openTestDB(t))
	ctx := context.Background()

	done := make(chan struct{})
	run := func(_ context.Context, j Job) error {
		defer close(done)
		return nil
	}

	worker := NewWorker(store, run, 2)
	worker.Start(ctx)
	defer worker.Stop()

	// Arrange
	job := NewJob("summarise logs", "", "", "default")
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Act
	if err := worker.Submit(job); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for job to complete")
	}

	// Assert — poll briefly since UpdateStatus is async after run returns
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, err := store.Get(ctx, job.ID)
		if err != nil {
			t.Fatalf("Get after run: %v", err)
		}
		if got.Status == JobCompleted {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := store.Get(ctx, job.ID)
	t.Errorf("Status = %q after completion, want %q", got.Status, JobCompleted)
}

func TestWorker_failed_job_records_error(t *testing.T) {
	store, _ := NewJobStore(openTestDB(t))
	ctx := context.Background()

	done := make(chan struct{})
	run := func(_ context.Context, j Job) error {
		defer close(done)
		return errors.New("boom")
	}

	worker := NewWorker(store, run, 1)
	worker.Start(ctx)
	defer worker.Stop()

	job := NewJob("crash me", "", "", "remote")
	_ = store.Create(ctx, job)
	_ = worker.Submit(job)

	<-done
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, _ := store.Get(ctx, job.ID)
		if got.Status == JobFailed {
			if got.Error == "" {
				t.Error("failed job has empty Error field")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, _ := store.Get(ctx, job.ID)
	t.Errorf("Status = %q, want %q", got.Status, JobFailed)
}

func TestJobStore_Cancel_queued_succeeds(t *testing.T) {
	store, _ := NewJobStore(openTestDB(t))
	ctx := context.Background()

	// Arrange
	job := NewJob("do something", "slack", "", "remote")
	_ = store.Create(ctx, job)

	// Act
	err := store.Cancel(ctx, job.ID)

	// Assert
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	got, _ := store.Get(ctx, job.ID)
	if got.Status != JobCancelled {
		t.Errorf("Status = %q, want %q", got.Status, JobCancelled)
	}
}

func TestJobStore_Cancel_completed_returns_error(t *testing.T) {
	store, _ := NewJobStore(openTestDB(t))
	ctx := context.Background()

	// Arrange: create and manually mark completed
	job := NewJob("done job", "", "", "default")
	_ = store.Create(ctx, job)
	_ = store.UpdateStatus(ctx, job.ID, JobCompleted, "")

	// Act
	err := store.Cancel(ctx, job.ID)

	// Assert
	if err == nil {
		t.Error("expected error when cancelling a completed job")
	}
}

func TestJobStore_Cancel_failed_returns_error(t *testing.T) {
	store, _ := NewJobStore(openTestDB(t))
	ctx := context.Background()

	job := NewJob("failed job", "", "", "default")
	_ = store.Create(ctx, job)
	_ = store.UpdateStatus(ctx, job.ID, JobFailed, "something went wrong")

	if err := store.Cancel(ctx, job.ID); err == nil {
		t.Error("expected error when cancelling a failed job")
	}
}

func TestJobStore_List_sorted_descending(t *testing.T) {
	store, _ := NewJobStore(openTestDB(t))
	ctx := context.Background()

	// Arrange: three jobs created with deliberate time gaps
	jobs := []Job{
		NewJob("first", "", "", "remote"),
		NewJob("second", "", "", "remote"),
		NewJob("third", "", "", "remote"),
	}
	// Stagger timestamps so the order is deterministic
	for i := range jobs {
		jobs[i].CreatedAt = time.Now().UTC().Add(time.Duration(i) * time.Millisecond)
		jobs[i].UpdatedAt = jobs[i].CreatedAt
		_ = store.Create(ctx, jobs[i])
	}

	// Act
	got, err := store.List(ctx)

	// Assert
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List returned %d jobs, want 3", len(got))
	}
	// Newest (index 2 in jobs slice) should be first
	if got[0].Intent != "third" {
		t.Errorf("first listed job intent = %q, want third (newest)", got[0].Intent)
	}
	if got[2].Intent != "first" {
		t.Errorf("last listed job intent = %q, want first (oldest)", got[2].Intent)
	}
	// Verify strictly descending order
	for i := 1; i < len(got); i++ {
		if got[i].CreatedAt.After(got[i-1].CreatedAt) {
			t.Errorf("jobs[%d].CreatedAt (%v) > jobs[%d].CreatedAt (%v): not descending",
				i, got[i].CreatedAt, i-1, got[i-1].CreatedAt)
		}
	}
}
