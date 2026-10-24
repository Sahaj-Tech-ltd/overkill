package terminal

import (
	"context"
	"fmt"
	"time"
)

// Reservation outlives the launching RPC. Failure to renew authority closes
// the process; release follows confirmed owned-process and output cleanup.
type Reservation interface {
	Check(context.Context) error
	Release(context.Context) error
}

func (m *Manager) ReservationsEnabled() bool { return m.options.Reserve != nil }

func (e *entry) validateReservation(ctx context.Context) error {
	if e.reservation == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := e.reservation.Check(ctx); err != nil {
		e.close()
		return fmt.Errorf("terminal: workspace authority lost; closing process: %w", err)
	}
	return nil
}

func (e *entry) watchReservation() {
	if e.reservation == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-e.done:
			return
		case <-ticker.C:
			if e.validateReservation(context.Background()) != nil {
				return
			}
		}
	}
}

func releaseReservation(reservation Reservation) error {
	if reservation == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return reservation.Release(ctx)
}
