package subagent

import (
	"context"
	"fmt"
	"time"
)

// WorkerConfig holds the configuration for a single child task worker.
type WorkerConfig struct {
	Goal       string
	Context    string
	MaxSteps   int
	Timeout    time.Duration
	TaskIndex  int
	NoAPICalls bool     // for 0-API-call diagnostic testing
	FilesRead    []string // pre-set file tracking
	FilesWritten []string // pre-set file tracking
}

// Worker runs a single child task in a goroutine with timeout and cancellation support.
type Worker struct {
	cfg WorkerConfig
}

// NewWorker creates a new Worker with the given configuration.
func NewWorker(cfg WorkerConfig) *Worker {
	return &Worker{cfg: cfg}
}

// Run executes the worker's task. It respects the parent context for cancellation
// and applies the configured timeout. The returned Result is never nil.
func (w *Worker) Run(ctx context.Context) (*Result, error) {
	// Apply defaults.
	maxSteps := w.cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 15
	}
	timeout := w.cfg.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}

	// maxSteps will be used when real agent integration replaces the simulated goroutine.
	_ = maxSteps

	start := time.Now()

	// Derive a child context with the configured timeout.
	childCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Buffered channel (size 1) so the goroutine never leaks.
	resultCh := make(chan *Result, 1)

	go func() {
		// Simulate agent work: a short delay representing LLM round-trips.
		// This allows the timeout/cancellation paths to be exercised in tests.
		// When real agent integration lands, this sleep is replaced by actual work.
		select {
		case <-childCtx.Done():
			return // parent will handle the timeout/cancel in its own select
		case <-time.After(10 * time.Millisecond):
		}

		resultCh <- &Result{
			TaskIndex:    w.cfg.TaskIndex,
			Status:       "completed",
			Summary:      fmt.Sprintf("Processed: %s", w.cfg.Goal),
			ExitReason:   "completed",
			TokensIn:     100,
			TokensOut:    50,
			CostUSD:      0.001,
			DurationMs:   time.Since(start).Milliseconds(),
			FilesRead:    w.cfg.FilesRead,
			FilesWritten: w.cfg.FilesWritten,
		}
	}()

	select {
	case res := <-resultCh:
		res.DurationMs = time.Since(start).Milliseconds()
		return res, nil

	case <-childCtx.Done():
		elapsed := time.Since(start).Milliseconds()

		status := "interrupted"
		exitReason := "interrupted"
		errMsg := ""

		if childCtx.Err() == context.DeadlineExceeded {
			status = "timeout"
			exitReason = "timeout"
		}

		if w.cfg.NoAPICalls {
			errMsg = "child timed out after 0 API calls (stuck in prompt construction or credential resolution)"
		}

		return &Result{
			TaskIndex:    w.cfg.TaskIndex,
			Status:       status,
			Summary:      fmt.Sprintf("Processed: %s", w.cfg.Goal),
			Error:        errMsg,
			ExitReason:   exitReason,
			DurationMs:   elapsed,
			FilesRead:    w.cfg.FilesRead,
			FilesWritten: w.cfg.FilesWritten,
		}, nil
	}
}
