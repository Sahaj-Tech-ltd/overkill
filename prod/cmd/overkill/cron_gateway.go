package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/cron"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
)

// cronIdleWindow is how long the agent must be idle before buffered
// cron output is flushed through the dispatcher. Set from config
// at startup; the const is the default.
var cronIdleWindow = 5 * time.Minute

// setCronIdleWindow overrides the idle window from config.
func setCronIdleWindow(sec int) {
	if sec > 0 {
		cronIdleWindow = time.Duration(sec) * time.Second
	}
}

// cronReply is a no-op gateway.Reply for cron-injected messages.
// Cron has no chat surface to render replies into; agent responses
// to cron prompts are dropped silently. When cron output needs to
// reach users, onFlush dispatches through actual gateway channels.
type cronReply struct{}

func (cronReply) PostInitial(_ context.Context, _ gateway.Inbound, _ string) (string, error) {
	return "cron", nil
}
func (cronReply) Update(_ context.Context, _, _ string) error {
	return nil
}
func (cronReply) Final(_ context.Context, _, _ string) error {
	return nil
}
func (cronReply) Error(_ context.Context, _ string, _ error) error {
	return nil
}
func (cronReply) StartTyping() func() {
	return func() {}
}

// gatewayOnFire returns an OnFire-compatible callback that runs a cron
// job's shell command and dispatches the output through the gateway
// dispatcher. If the agent is busy (recent activity within
// cronIdleWindow), output is buffered and flushed later when the agent
// becomes idle.
//
//   - disp: the gateway dispatcher used to handle the output as an
//     inbound message. When nil (standalone daemon), falls back to
//     shellOnFire for direct command execution.
//   - tracker: shared activity tracker; records user/agent activity.
//   - buf: output buffer for holding output while the agent is busy.
func gatewayOnFire(disp *gateway.Dispatcher, tracker *cron.ActivityTracker, buf *cron.OutputBuffer) func(j *cron.Job) error {
	// Standalone daemon mode — no dispatcher wired.
	// Fall back to shellOnFire for direct command execution.
	if disp == nil {
		return shellOnFire
	}

	return func(j *cron.Job) error {
		// Validate command safety.
		if j.Command == "" {
			return fmt.Errorf("cron: job %q has no command", j.Name)
		}
		if err := validateShellCommand(j.Command); err != nil {
			return fmt.Errorf("cron: %w", err)
		}

		// Track in the daemon ledger.
		t := daemonLedger.Begin("cron", j.Name)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		out, err := exec.CommandContext(ctx, "sh", "-c", j.Command).CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[cron %s] FAILED: %v\n%s\n", j.Name, err, string(out))
			daemonLedger.Fail(t.ID, err)
			return err
		}
		fmt.Printf("[cron %s] ok\n", j.Name)
		daemonLedger.Complete(t.ID, "ok")

		output := strings.TrimSpace(string(out))
		if output == "" {
			return nil // nothing to deliver
		}

		// Build inbound from output.
		channel := j.DeliveryTarget
		if channel == "" {
			channel = "cron"
		}
		in := gateway.Inbound{
			Channel: channel,
			ChatKey: "cron:" + j.ID,
			From:    "cron/" + j.Name,
			Text:    fmt.Sprintf("⏰ **cron: %s**\n```\n%s\n```", j.Name, output),
		}

		// Idle → dispatch immediately; busy → buffer for later.
		if tracker.IdleFor(cronIdleWindow) {
			disp.Handle(context.Background(), in, cronReply{})
		} else {
			buf.MaybeFire(j, output)
		}

		return nil
	}
}
