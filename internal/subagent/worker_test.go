package subagent

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWorker_RunCompletes(t *testing.T) {
	w := NewWorker(WorkerConfig{
		Goal:      "refactor auth module",
		MaxSteps:  10,
		Timeout:   5 * time.Second,
		TaskIndex: 0,
	})

	res, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != "completed" {
		t.Errorf("Status = %q, want %q", res.Status, "completed")
	}
	if res.TaskIndex != 0 {
		t.Errorf("TaskIndex = %d, want 0", res.TaskIndex)
	}
	if res.ExitReason != "completed" {
		t.Errorf("ExitReason = %q, want %q", res.ExitReason, "completed")
	}
	if !strings.Contains(res.Summary, "refactor auth module") {
		t.Errorf("Summary = %q, should contain goal", res.Summary)
	}
}

func TestWorker_RunTimesOut(t *testing.T) {
	// Use a 1ms timeout — shorter than the goroutine's 10ms simulated work.
	// This guarantees the timeout fires before the goroutine completes.
	w := NewWorker(WorkerConfig{
		Goal:      "long running task",
		MaxSteps:  100,
		Timeout:   1 * time.Millisecond,
		TaskIndex: 1,
	})

	res, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != "timeout" {
		t.Errorf("Status = %q, want %q", res.Status, "timeout")
	}
	if !strings.Contains(res.ExitReason, "timeout") {
		t.Errorf("ExitReason = %q, should contain 'timeout'", res.ExitReason)
	}
}

func TestWorker_RunCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before Run

	w := NewWorker(WorkerConfig{
		Goal:      "cancelled task",
		MaxSteps:  5,
		Timeout:   5 * time.Second,
		TaskIndex: 2,
	})

	res, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != "interrupted" {
		t.Errorf("Status = %q, want %q", res.Status, "interrupted")
	}
	if res.ExitReason != "interrupted" {
		t.Errorf("ExitReason = %q, want %q", res.ExitReason, "interrupted")
	}
}

func TestWorker_RunTracksDuration(t *testing.T) {
	w := NewWorker(WorkerConfig{
		Goal:      "track duration",
		MaxSteps:  5,
		Timeout:   5 * time.Second,
		TaskIndex: 3,
	})

	res, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The goroutine has a 10ms simulated delay, so DurationMs should be >= 10.
	if res.DurationMs < 10 {
		t.Errorf("DurationMs = %d, want >= 10", res.DurationMs)
	}
}

func TestWorker_ZeroAPICallDiagnostic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Pre-cancel to trigger the ctx.Done() path

	w := NewWorker(WorkerConfig{
		Goal:       "stuck task",
		MaxSteps:   5,
		Timeout:    50 * time.Millisecond,
		TaskIndex:  4,
		NoAPICalls: true,
	})

	res, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Error, "0 API calls") {
		t.Errorf("Error = %q, should contain '0 API calls'", res.Error)
	}
}

func TestWorker_ResultFiles(t *testing.T) {
	filesRead := []string{"/tmp/a.go", "/tmp/b.go"}
	filesWritten := []string{"/tmp/a.go", "/tmp/c.go"}

	w := NewWorker(WorkerConfig{
		Goal:         "file tracking task",
		MaxSteps:     5,
		Timeout:      5 * time.Second,
		TaskIndex:    5,
		FilesRead:    filesRead,
		FilesWritten: filesWritten,
	})

	res, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify FilesRead.
	if len(res.FilesRead) != len(filesRead) {
		t.Fatalf("FilesRead length = %d, want %d", len(res.FilesRead), len(filesRead))
	}
	for _, f := range filesRead {
		found := false
		for _, r := range res.FilesRead {
			if r == f {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("FilesRead missing %q", f)
		}
	}

	// Verify FilesWritten.
	if len(res.FilesWritten) != len(filesWritten) {
		t.Fatalf("FilesWritten length = %d, want %d", len(res.FilesWritten), len(filesWritten))
	}
	for _, f := range filesWritten {
		found := false
		for _, r := range res.FilesWritten {
			if r == f {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("FilesWritten missing %q", f)
		}
	}
}
