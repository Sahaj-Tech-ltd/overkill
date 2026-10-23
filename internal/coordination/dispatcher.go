package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Executor reserves the same session admission used by interactive clients.
// Reservation must not execute work; a busy session leaves messages accepted.
type Executor interface {
	Reserve(context.Context, Binding) (Execution, error)
}

type Execution interface {
	Run(context.Context, Message) (json.RawMessage, error)
	Release()
}

type DispatchOptions struct {
	PollInterval time.Duration
	LeaseTTL     time.Duration
	Concurrency  int
	OnError      func(peerID string, err error)
}

// Dispatcher owns background executions until Run returns. Delivery remains
// queued while a session is busy, and interrupted work requires review.
type Dispatcher struct {
	Store    *Store
	Executor Executor
	Options  DispatchOptions
}

func (d *Dispatcher) Run(ctx context.Context) error {
	if d.Store == nil || d.Executor == nil {
		return fmt.Errorf("coordination: dispatcher store and executor required")
	}
	options := d.Options
	if options.PollInterval == 0 {
		options.PollInterval = 250 * time.Millisecond
	}
	if options.LeaseTTL == 0 {
		options.LeaseTTL = 15 * time.Second
	}
	if options.Concurrency == 0 {
		options.Concurrency = 4
	}
	if options.PollInterval < 10*time.Millisecond || options.PollInterval > time.Minute || options.LeaseTTL < 300*time.Millisecond || options.LeaseTTL > time.Minute || options.Concurrency < 1 || options.Concurrency > 64 {
		return fmt.Errorf("coordination: invalid dispatcher timing or concurrency")
	}
	ticker := time.NewTicker(options.PollInterval)
	defer ticker.Stop()
	finished := make(chan string, options.Concurrency)
	active := make(map[string]bool)
	var workers sync.WaitGroup
	var reportMu sync.Mutex
	lastError := make(map[string]string)
	report := func(peerID string, err error) {
		if ctx.Err() != nil || errors.Is(err, ErrBusy) || errors.Is(err, ErrNotFound) {
			return
		}
		reportMu.Lock()
		if err == nil {
			delete(lastError, peerID)
			reportMu.Unlock()
			return
		}
		message := boundedError(err)
		if lastError[peerID] == message {
			reportMu.Unlock()
			return
		}
		lastError[peerID] = message
		reportMu.Unlock()
		if options.OnError != nil {
			options.OnError(peerID, err)
		}
	}
	scan := func() {
		after := ""
		for len(active) < options.Concurrency && ctx.Err() == nil {
			bindings, err := d.Store.DispatchBindings(ctx, after, 100)
			if err != nil {
				report("", err)
				return
			}
			for _, binding := range bindings {
				after = binding.Peer.ID
				if binding.validationErr != nil {
					report(binding.Peer.ID, binding.validationErr)
					continue
				}
				if active[binding.Peer.ID] {
					continue
				}
				if len(active) >= options.Concurrency {
					return
				}
				active[binding.Peer.ID] = true
				workers.Go(func() {
					defer func() { finished <- binding.Peer.ID }()
					report(binding.Peer.ID, d.execute(ctx, binding, uuid.NewString(), options.LeaseTTL))
				})
			}
			if len(bindings) < 100 {
				return
			}
		}
	}
	scan()
	for {
		select {
		case <-ctx.Done():
			workers.Wait()
			return nil
		case peerID := <-finished:
			delete(active, peerID)
		case <-ticker.C:
			scan()
		}
	}
}

func (d *Dispatcher) execute(ctx context.Context, binding Binding, owner string, ttl time.Duration) (resultErr error) {
	lease, err := d.Store.AcquireLease(ctx, binding.Peer.ID, owner, ttl)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		resultErr = errors.Join(resultErr, d.Store.ReleaseLease(cleanup, lease))
	}()
	execution, err := reserveExecution(ctx, d.Executor, binding)
	if err != nil {
		return err
	}
	if execution == nil {
		return fmt.Errorf("coordination: executor returned no reservation")
	}
	defer func() { resultErr = errors.Join(resultErr, releaseExecution(execution)) }()
	m, err := d.Store.ClaimLease(ctx, lease)
	if err != nil {
		return err
	}
	deadline, stopDeadline := context.WithDeadline(ctx, m.Deadline)
	defer stopDeadline()
	runCtx, cancel := context.WithCancelCause(deadline)
	defer cancel(nil)
	// A local timer fences an executor even if its heartbeat stalls. Renewal
	// moves this timer only after the database confirms the current owner.
	leaseTimer := time.AfterFunc(time.Until(lease.ExpiresAt), func() { cancel(ErrLeaseLost) })
	defer leaseTimer.Stop()
	heartbeatCtx, stopHeartbeat := context.WithCancel(runCtx)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(ttl / 3)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				check, cancelCheck := context.WithTimeout(heartbeatCtx, ttl/3)
				renewed, err := d.Store.RenewLease(check, lease, ttl)
				if err == nil {
					current, readErr := d.Store.Read(check, lease.PeerID, m.ID)
					err = readErr
					if err == nil && current.State == Cancelled {
						err = context.Canceled
					}
				}
				cancelCheck()
				if err != nil {
					// Successful execution deliberately stops its heartbeat. A
					// query cancelled by that cleanup is not a failed agent run.
					// Parent cancellation already has its cause on runCtx.
					if heartbeatCtx.Err() != nil {
						return
					}
					cancel(err)
					return
				}
				leaseTimer.Reset(time.Until(renewed.ExpiresAt))
			}
		}
	}()
	payload, runErr := runExecution(withExecutionLineage(runCtx, m, lease), execution, m)
	stopHeartbeat()
	<-heartbeatDone
	leaseTimer.Stop()
	finalize, cancelFinalize := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelFinalize()
	current, err := d.Store.Read(finalize, lease.PeerID, m.ID)
	if err != nil {
		return err
	}
	if current.State.Terminal() {
		return nil
	} // Includes an explicit agent_reply or cancellation.
	if err := d.Store.ReconcileExecutionContracts(finalize, lease, m.ID); err != nil {
		return err // Contract reconciliation already persisted its blocked/failed outcome.
	}
	if cause := context.Cause(runCtx); cause != nil {
		runErr = errors.Join(runErr, cause)
	}
	if runErr == nil {
		_, err = d.Store.CompleteLease(finalize, lease, m.ID, payload)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrRequestContracts) {
			return err
		} // Preserve the durable gate decision.
		runErr = err
	}
	state := "failed"
	if ctx.Err() != nil || errors.Is(runErr, ErrUsageBudget) {
		state = "blocked"
	}
	// Lease loss intentionally leaves started work for the recovery path. A
	// stale owner cannot declare completion/failure after replacement begins.
	if errors.Is(runErr, ErrLeaseLost) {
		return runErr
	}
	detail, _ := json.Marshal(map[string]string{"error": boundedError(runErr), "reason": state})
	transitionErr := d.Store.failExecution(finalize, lease, m.ID, state, detail)
	return errors.Join(runErr, transitionErr)
}

func reserveExecution(ctx context.Context, executor Executor, binding Binding) (execution Execution, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("coordination: reservation panic: %v", recovered)
		}
	}()
	return executor.Reserve(ctx, binding)
}

func runExecution(ctx context.Context, execution Execution, message Message) (payload json.RawMessage, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("coordination: executor panic: %v", recovered)
		}
	}()
	return execution.Run(ctx, message)
}

func releaseExecution(execution Execution) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("coordination: reservation release panic: %v", recovered)
		}
	}()
	execution.Release()
	return nil
}

func boundedError(err error) string {
	message := err.Error()
	if len(message) > 4096 {
		message = message[:4096] + " [truncated]"
	}
	return message
}
