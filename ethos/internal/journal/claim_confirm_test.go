package journal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newQueue(t *testing.T) *CompressionQueue {
	t.Helper()
	return NewCompressionQueue(filepath.Join(t.TempDir(), "q"))
}

func TestQueue_EnqueueClaimConfirm(t *testing.T) {
	q := newQueue(t)
	job, err := q.Enqueue("obs-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != QueuePending {
		t.Errorf("expected pending, got %s", job.State)
	}

	claimed, err := q.Claim(1234, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != job.ID {
		t.Fatalf("expected to claim job %s, got %+v", job.ID, claimed)
	}
	if claimed.State != QueueClaimed || claimed.ClaimedBy != 1234 {
		t.Errorf("claim state wrong: %+v", claimed)
	}

	if err := q.Confirm(claimed.ID, 1234); err != nil {
		t.Fatal(err)
	}
	final, _ := q.Get(claimed.ID)
	if final.State != QueueConfirmed {
		t.Errorf("expected confirmed, got %s", final.State)
	}
}

func TestQueue_EnqueueIsIdempotent(t *testing.T) {
	q := newQueue(t)
	j1, _ := q.Enqueue("obs-1")
	j2, _ := q.Enqueue("obs-1")
	if j1.ID != j2.ID {
		t.Errorf("re-enqueue should return existing job, got %s vs %s", j1.ID, j2.ID)
	}
}

func TestQueue_ClaimSkipsLiveClaim(t *testing.T) {
	q := newQueue(t)
	_, _ = q.Enqueue("obs-1")
	first, _ := q.Claim(100, time.Hour)
	if first == nil {
		t.Fatal("first claim should succeed")
	}
	// Second worker tries immediately — should get nothing.
	second, _ := q.Claim(200, time.Hour)
	if second != nil {
		t.Errorf("second claim should fail while first is live, got %+v", second)
	}
}

func TestQueue_ClaimReusesExpiredJob(t *testing.T) {
	q := newQueue(t)
	_, _ = q.Enqueue("obs-1")
	first, _ := q.Claim(100, time.Nanosecond) // expires immediately
	if first == nil {
		t.Fatal("first claim should succeed")
	}
	time.Sleep(time.Millisecond)
	second, _ := q.Claim(200, time.Hour)
	if second == nil || second.ID != first.ID {
		t.Errorf("expired claim should be re-claimable, got %+v", second)
	}
	if second.ClaimedBy != 200 {
		t.Errorf("new claim should rebind worker, got %d", second.ClaimedBy)
	}
}

func TestQueue_RenewExtendsClaim(t *testing.T) {
	q := newQueue(t)
	_, _ = q.Enqueue("obs-1")
	c, _ := q.Claim(100, 10*time.Millisecond)
	if err := q.Renew(c.ID, 100, time.Hour); err != nil {
		t.Fatal(err)
	}
	job, _ := q.Get(c.ID)
	if !job.ClaimedUntil.After(time.Now().Add(30 * time.Minute)) {
		t.Errorf("renew should extend the deadline: %v", job.ClaimedUntil)
	}
}

func TestQueue_RenewRejectsForeignWorker(t *testing.T) {
	q := newQueue(t)
	_, _ = q.Enqueue("obs-1")
	c, _ := q.Claim(100, time.Hour)
	if err := q.Renew(c.ID, 999, time.Hour); err == nil {
		t.Error("renew with wrong worker pid should error")
	}
}

func TestQueue_FailBelowThresholdRetries(t *testing.T) {
	q := newQueue(t)
	_, _ = q.Enqueue("obs-1")
	c, _ := q.Claim(100, time.Hour)
	if err := q.Fail(c.ID, 100, errors.New("model timeout"), 3); err != nil {
		t.Fatal(err)
	}
	job, _ := q.Get(c.ID)
	if job.State != QueuePending {
		t.Errorf("expected re-pending after fail under threshold, got %s", job.State)
	}
	if job.LastError == "" {
		t.Error("LastError should be recorded")
	}
	// Should be re-claimable.
	again, _ := q.Claim(100, time.Hour)
	if again == nil || again.ID != c.ID {
		t.Errorf("failed-and-pending job should be re-claimable")
	}
	if again.Attempts != 2 {
		t.Errorf("attempt count should advance, got %d", again.Attempts)
	}
}

func TestQueue_FailAtThresholdMovesToFailed(t *testing.T) {
	q := newQueue(t)
	_, _ = q.Enqueue("obs-1")
	c, _ := q.Claim(100, time.Hour)
	// Force Attempts=3 so fail-with-max=3 transitions to QueueFailed.
	job, _ := q.Get(c.ID)
	job.Attempts = 3
	_ = q.saveLocked(job) // direct write — testing the transition

	if err := q.Fail(c.ID, 100, errors.New("bridge down"), 3); err != nil {
		t.Fatal(err)
	}
	final, _ := q.Get(c.ID)
	if final.State != QueueFailed {
		t.Errorf("expected failed at threshold, got %s", final.State)
	}
}

func TestQueue_PendingCountIgnoresClaimedAndConfirmed(t *testing.T) {
	q := newQueue(t)
	_, _ = q.Enqueue("a")
	_, _ = q.Enqueue("b")
	_, _ = q.Enqueue("c")
	c, _ := q.Claim(100, time.Hour)
	_ = q.Confirm(c.ID, 100)
	count, _ := q.PendingCount()
	if count != 2 {
		t.Errorf("expected 2 pending after one confirm, got %d", count)
	}
}

func TestQueue_AtomicSaveLeavesNoTemp(t *testing.T) {
	q := newQueue(t)
	job, _ := q.Enqueue("obs-1")
	tmp := filepath.Join(q.dir, job.ID+".json.tmp")
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("tmp file should not remain: %v", err)
	}
}
