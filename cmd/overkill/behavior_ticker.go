package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls/impossibleprobe"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls/monitor"
)

// behaviorTicker is the daemon-side periodic worker that runs Wall 4
// (paper #48). It scans today's journal entries for behavioral
// findings AND extracts failed-hypothesis records that the TUI may
// have missed (e.g. agent ran in a non-TUI context, or the realtime
// extraction path was unwired for a session).
//
// Cadence: 5 minutes. The detectors are pure-Go and cheap; the bulk
// of the cost is reading today's jsonl, which is bounded.
//
// Outputs:
//   - monitor findings → AlertPatternDetected rows in the AlertStore
//     so the next TUI boot surfaces them as toasts.
//   - failhypo records → appended to the on-disk store.
//
// All side-effects are best-effort; the goroutine never returns an
// error and never blocks the daemon shutdown path.
const behaviorTickInterval = 5 * time.Minute

func behaviorTickerStart(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		// First fire happens after one tick — we don't run at boot
		// because the daemon's start logs are already noisy.
		t := time.NewTicker(behaviorTickInterval)
		defer t.Stop()
		// Track the highest-resolution timestamp we've already emitted
		// findings for, so a long-running session doesn't get re-paged
		// on every tick. Per-process state is fine — daemon restart
		// re-pages once, which is acceptable.
		lastSeen := time.Now().Add(-behaviorTickInterval)

		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				lastSeen = behaviorTick(lastSeen)
			}
		}
	}()
	fmt.Printf("%s✓ behavior monitor ticker armed (every %s)%s\n", colorGreen, behaviorTickInterval, colorReset)
}

func behaviorTick(since time.Time) time.Time {
	home, err := os.UserHomeDir()
	if err != nil {
		return since
	}
	jdir := filepath.Join(home, ".overkill", "journal")
	r := journal.NewFlightRecorder(jdir, "")
	entries, err := r.ReadDay(time.Now())
	if err != nil || len(entries) == 0 {
		return since
	}

	// Filter to entries newer than `since` so we don't re-page the
	// user on every tick. We still want a tiny overlap window — if
	// a finding spans entries on both sides of the cutoff we'd lose
	// it otherwise — but ReadDay returns a flat slice and Scan is
	// designed to take whatever window we hand it.
	var window []journal.Entry
	maxTS := since
	for _, e := range entries {
		if e.Timestamp.After(since) {
			window = append(window, e)
			if e.Timestamp.After(maxTS) {
				maxTS = e.Timestamp
			}
		}
	}
	if len(window) == 0 {
		return since
	}

	// 1) Wall 4 behavioral scan → alert rows.
	findings := monitor.Scan(window)
	if len(findings) > 0 {
		writeMonitorAlerts(findings)
	}

	// 2) failed-hypothesis extraction over the same window.
	fhDir := filepath.Join(home, ".overkill", "failed_hypotheses")
	_ = os.MkdirAll(fhDir, 0o755)
	store := journal.NewFailedHypothesisStore(fhDir)
	for _, e := range window {
		for _, h := range journal.ExtractFailedHypotheses(e) {
			_ = store.Append(h)
		}
	}

	return maxTS
}

// writeMonitorAlerts persists each finding as an AlertPatternDetected
// row in ~/.overkill/alerts so the next TUI boot surfaces them. We
// reuse the AlertStore the TUI already reads — no new producer
// channel needed.
func writeMonitorAlerts(findings []monitor.Finding) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	alertDir := filepath.Join(home, ".overkill", "alerts")
	if err := os.MkdirAll(alertDir, 0o755); err != nil {
		return
	}
	store := journal.NewAlertStore(alertDir)
	_ = store.Load()
	for _, f := range findings {
		msg := fmt.Sprintf("[%s] %s", f.Category, f.Reason)
		if f.EntryID != "" {
			msg += fmt.Sprintf(" (entry %s)", f.EntryID)
		}
		_ = store.Create(journal.AlertPatternDetected, msg, f.SessionID)
	}
}

// impossibleProbeStart arms a periodic impossible-bench probe runner.
// Every 15 minutes, it runs DefaultProbes against a no-op responder
// (daemon has no agent session). When wired into a live agent context
// (e.g. via cron or alarm), the Responder is swapped for the agent.
func impossibleProbeStart(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(15 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				probes := impossibleprobe.DefaultProbes()
				for _, p := range probes {
					// No-op responder: daemon has no agent to probe.
					// When wired to cron/alarm, the responder is the
					// actual agent session.
					result, err := impossibleprobe.Run(ctx,
						impossibleprobe.ResponderFunc(func(ctx context.Context, prompt string) (string, error) {
							return "", fmt.Errorf("daemon: no agent responder available")
						}), p)
					if err != nil {
						fmt.Fprintf(os.Stderr, "impossibleprobe %s: %v\n", p.ID, err)
						continue
					}
					if result.Outcome != impossibleprobe.OutcomePassed {
						fmt.Fprintf(os.Stderr, "%simpossibleprobe %s: %s — %s%s\n",
							colorYellow, p.ID, result.Outcome, result.Reason, colorReset)
					}
				}
			}
		}
	}()
	fmt.Printf("%s✓ impossible-probe ticker armed (every 15m)%s\n", colorGreen, colorReset)
}
