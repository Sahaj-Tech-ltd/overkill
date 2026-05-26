package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
)

// journalReplayCmd is `overkill journal replay <session>` — emits
// flight-recorder entries chronologically for debugging. Optional
// real-time pacing via --speed.
var journalReplayCmd = &cobra.Command{
	Use:   "replay <session-id>",
	Short: "Stream a session's flight-recorder entries chronologically",
	Long: `Walks the journaled session and prints one line per entry
with a glyph (→ user, ← reply, ⚙ tool call, ✓ tool result, ✗ error,
# system) and the elapsed offset from session start.

By default emits as fast as the terminal can render. --speed > 0
paces playback to recorded timing (1.0 = real-time, 2.0 = 2× faster).
--type filters to specific entry kinds; repeat the flag to allow
multiple.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		speed, _ := cmd.Flags().GetFloat64("speed")
		typesFlag, _ := cmd.Flags().GetStringSlice("type")
		snapshotOnly, _ := cmd.Flags().GetBool("snapshot")

		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		jdir := filepath.Join(home, ".overkill", "journal")
		rec := journal.NewFlightRecorder(jdir, args[0])
		opts := journal.ReplayOptions{Speed: speed}
		for _, t := range typesFlag {
			opts.Types = append(opts.Types, journal.EntryType(t))
		}

		if snapshotOnly {
			r := journal.NewReplayer(rec, args[0], opts)
			events, err := r.Snapshot()
			if err != nil {
				return err
			}
			for _, ev := range events {
				fmt.Println(journal.FormatReplayEvent(ev))
			}
			return nil
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
		defer cancel()
		out, errCh := rec.Replay(ctx, args[0], opts)
		for {
			select {
			case ev, ok := <-out:
				if !ok {
					return nil
				}
				fmt.Println(journal.FormatReplayEvent(ev))
			case err := <-errCh:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	},
}

func init() {
	journalReplayCmd.Flags().Float64("speed", 0, "real-time playback multiplier (0 = as fast as possible)")
	journalReplayCmd.Flags().StringSlice("type", nil, "filter to entry types (user_input, agent_reply, tool_call, tool_result, error, system)")
	journalReplayCmd.Flags().Bool("snapshot", false, "print the full session synchronously instead of streaming")
	overkillJournalCmd.AddCommand(journalReplayCmd)
}
