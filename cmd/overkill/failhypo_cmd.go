package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
)

// failhypo surfaces the typed failed-hypothesis tracker (paper #48
// design input #5). Three commands: extract (run the regex pass over
// an existing session and persist findings), list (dump everything),
// search (substring match).
var failhypoCmd = &cobra.Command{
	Use:     "failhypo",
	Short:   "Typed failed-hypothesis tracker (paper #48 #5)",
	Aliases: []string{"failed-hypotheses", "fh"},
}

func failhypoDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".overkill", "failed_hypotheses"), nil
}

var failhypoExtractCmd = &cobra.Command{
	Use:   "extract [session-id]",
	Short: "Extract failed-hypothesis records from an existing session",
	Long: `Walks the flight-recorder entries for the given session (default:
"default"), runs the extraction regex over each agent_reply, and
appends every finding to ~/.overkill/failed_hypotheses/.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		jdir := filepath.Join(home, ".overkill", "journal")
		sid := "default"
		if len(args) == 1 {
			sid = args[0]
		}
		r := journal.NewFlightRecorder(jdir, sid)
		entries, err := r.ReadSession(sid)
		if err != nil {
			return fmt.Errorf("failhypo: read session: %w", err)
		}
		fhDir, err := failhypoDir()
		if err != nil {
			return err
		}
		store := journal.NewFailedHypothesisStore(fhDir)
		count := 0
		for _, e := range entries {
			for _, h := range journal.ExtractFailedHypotheses(e) {
				if err := store.Append(h); err != nil {
					return fmt.Errorf("failhypo: append: %w", err)
				}
				count++
			}
		}
		fmt.Printf("%s✓ extracted %d failed-hypothesis records from session %q%s\n", colorGreen, count, sid, colorReset)
		return nil
	},
}

var failhypoListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List every persisted failed-hypothesis record",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		fhDir, err := failhypoDir()
		if err != nil {
			return err
		}
		all, err := journal.NewFailedHypothesisStore(fhDir).All()
		if err != nil {
			return err
		}
		if len(all) == 0 {
			fmt.Printf("%sno failed-hypothesis records yet%s\n", colorDim, colorReset)
			return nil
		}
		for _, h := range all {
			printFailHypo(h)
		}
		return nil
	},
}

var failhypoSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search failed-hypothesis records (substring, case-insensitive)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fhDir, err := failhypoDir()
		if err != nil {
			return err
		}
		hits, err := journal.NewFailedHypothesisStore(fhDir).Search(args[0])
		if err != nil {
			return err
		}
		if len(hits) == 0 {
			fmt.Printf("%sno matches for %q — we haven't been down this road yet%s\n", colorDim, args[0], colorReset)
			return nil
		}
		fmt.Printf("%s%d prior failure(s) on %q:%s\n", colorBold, len(hits), args[0], colorReset)
		for _, h := range hits {
			printFailHypo(h)
		}
		return nil
	},
}

func printFailHypo(h journal.FailedHypothesis) {
	ts := h.Timestamp.Format("2006-01-02 15:04")
	subj := h.Subject
	if subj == "" {
		subj = "(no subject)"
	}
	fmt.Printf("  %s[%s] %s%s%s\n", colorDim, ts, colorBold, subj, colorReset)
	fmt.Printf("    tried:  %s\n", h.Hypothesis)
	fmt.Printf("    failed: %s\n", h.Reason)
}

func init() {
	failhypoCmd.AddCommand(failhypoExtractCmd)
	failhypoCmd.AddCommand(failhypoListCmd)
	failhypoCmd.AddCommand(failhypoSearchCmd)
	rootCmd.AddCommand(failhypoCmd)
}
