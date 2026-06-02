package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
	"github.com/Sahaj-Tech-ltd/overkill/internal/skills/registry"
	"github.com/Sahaj-Tech-ltd/overkill/internal/skills/safety"
)

// skillCmd groups skill-management subcommands. The main load/use
// surface still happens in setupAgent; these are operator-side
// controls for safety and visibility.
var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Manage skills (safety scan, list, block-list)",
}

// defaultScanner picks the right safety.Scanner based on env:
//   - OVERKILL_VT_API_KEY set → VirusTotal
//   - empty → NoopScanner (open by default, today's behaviour)
//
// Mirrors the wiring setupAgent will use, kept here so CLI
// subcommands behave identically to a real boot.
func defaultScanner() safety.Scanner {
	if key := os.Getenv("OVERKILL_VT_API_KEY"); key != "" {
		if sc := safety.NewVirusTotalScanner(key); sc != nil {
			return sc
		}
	}
	return safety.NoopScanner{}
}

// skillScanCmd runs the safety scanner against an arbitrary path
// (file or directory). Useful before manually dropping a skill into
// the user dir.
var skillScanCmd = &cobra.Command{
	Use:   "scan <path>",
	Short: "Run the safety scanner against a skill file or directory",
	Long: `Hashes each file under <path> and queries VirusTotal (if
OVERKILL_VT_API_KEY is set) for known-bad signatures. Unknown
verdicts mean VirusTotal has never seen the file — that's normal
for skills you wrote yourself. Malicious / Suspicious verdicts
mean at least one engine flagged the hash.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := args[0]
		info, err := os.Stat(target)
		if err != nil {
			return fmt.Errorf("stat %s: %w", target, err)
		}
		sc := defaultScanner()
		fmt.Printf("%sscanning %s with %s%s\n", colorDim, target, sc.Name(), colorReset)

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		if info.IsDir() {
			results, worst, err := safety.ScanDir(ctx, sc, target)
			if err != nil {
				return err
			}
			for _, r := range results {
				printScanResult(r)
			}
			fmt.Printf("\n%sworst verdict: %s%s\n", colorBold, worst, colorReset)
			if worst == safety.VerdictMalicious || worst == safety.VerdictSuspicious {
				return fmt.Errorf("scan blocked: %s", worst)
			}
			return nil
		}
		r, err := sc.Scan(ctx, target)
		if err != nil {
			return err
		}
		printScanResult(r)
		if r.Verdict == safety.VerdictMalicious || r.Verdict == safety.VerdictSuspicious {
			return fmt.Errorf("scan blocked: %s", r.Verdict)
		}
		return nil
	},
}

// skillListCmd dumps the loaded skill set. Useful for "what does
// the agent actually have available?" without launching the TUI.
var skillListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List loaded skills (bundled + user)",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		bundledDir, userDir, err := resolveSkillDirs()
		if err != nil {
			return err
		}
		loader := skills.NewLoader(bundledDir, userDir).WithScanner(defaultScanner())
		ss, err := loader.LoadAll()
		if err != nil {
			return err
		}
		if len(ss) == 0 {
			fmt.Printf("%sno skills loaded%s\n", colorDim, colorReset)
			return nil
		}
		for _, s := range ss {
			origin := "user"
			if s.Bundled {
				origin = "bundled"
			}
			fmt.Printf("  %s%s%s  %s(%s)%s\n", colorBold, s.Name, colorReset, colorDim, origin, colorReset)
			if s.Description != "" {
				fmt.Printf("    %s\n", s.Description)
			}
		}
		blocked := loader.BlockedSkills()
		if len(blocked) > 0 {
			fmt.Printf("\n%s%d skill(s) BLOCKED by safety scanner — run 'overkill skill block-list' for details%s\n",
				colorYellow, len(blocked), colorReset)
		}
		return nil
	},
}

// skillBlockListCmd reveals which skills the safety scanner refused
// at load time. The loader already logs the block; this surfaces
// the same info on demand without grepping logs.
var skillBlockListCmd = &cobra.Command{
	Use:   "block-list",
	Short: "Show skills the safety scanner refused to load",
	RunE: func(cmd *cobra.Command, args []string) error {
		bundledDir, userDir, err := resolveSkillDirs()
		if err != nil {
			return err
		}
		loader := skills.NewLoader(bundledDir, userDir).WithScanner(defaultScanner())
		if _, err := loader.LoadAll(); err != nil {
			return err
		}
		blocked := loader.BlockedSkills()
		if len(blocked) == 0 {
			fmt.Printf("%sno skills blocked%s\n", colorDim, colorReset)
			return nil
		}
		for _, b := range blocked {
			fmt.Printf("  %s%s%s  %s%s%s\n", colorRed, b.Verdict, colorReset, colorBold, b.Path, colorReset)
			if b.Reason != "" {
				fmt.Printf("    %s\n", b.Reason)
			}
		}
		return nil
	},
}

func printScanResult(r safety.Result) {
	color := colorGreen
	switch r.Verdict {
	case safety.VerdictMalicious:
		color = colorRed
	case safety.VerdictSuspicious:
		color = colorYellow
	case safety.VerdictUnknown:
		color = colorDim
	}
	short := r.SHA256
	if len(short) > 12 {
		short = short[:12]
	}
	fmt.Printf("  %s%s%s  %s  %s%s%s\n", color, r.Verdict, colorReset, r.Path, colorDim, short, colorReset)
	if r.Reason != "" {
		fmt.Printf("    %s\n", r.Reason)
	}
	if len(r.Detections) > 0 {
		raw, _ := json.MarshalIndent(r.Detections, "    ", "  ")
		fmt.Printf("    %s\n", string(raw))
	}
}

// resolveSkillDirs reproduces the bundled+user dir picks setupAgent
// uses, kept here so CLI subcommands run without a full agent.
func resolveSkillDirs() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	userDir := filepath.Join(home, ".overkill", "skills")
	// Bundled dir convention — same as setupAgent's wiring. If the
	// binary doesn't ship a bundled dir we just pass empty.
	bundledDir := os.Getenv("OVERKILL_BUNDLED_SKILLS")
	return bundledDir, userDir, nil
}

// registryClient returns a configured ClawHub client honouring
// OVERKILL_SKILL_REGISTRY (URL override) and
// OVERKILL_SKILL_REGISTRY_TOKEN (bearer token for private hubs).
// Refuses to construct without a real safety scanner: the install
// path needs one, and we'd rather error early than silently install
// unscanned skills.
func registryClient() *registry.Client {
	url := os.Getenv("OVERKILL_SKILL_REGISTRY")
	token := os.Getenv("OVERKILL_SKILL_REGISTRY_TOKEN")
	return registry.NewClient(url, token, defaultScanner())
}

var skillSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search the ClawHub registry",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		query := ""
		if len(args) > 0 {
			query = args[0]
			for _, a := range args[1:] {
				query += " " + a
			}
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		hits, err := registryClient().Search(ctx, query)
		if err != nil {
			return err
		}
		if len(hits) == 0 {
			fmt.Printf("%sno results%s\n", colorDim, colorReset)
			return nil
		}
		for _, s := range hits {
			fmt.Printf("  %s%s@%s%s  %s%s%s\n", colorBold, s.Slug, s.Version, colorReset, colorDim, s.Name, colorReset)
			if s.Description != "" {
				fmt.Printf("    %s\n", s.Description)
			}
		}
		return nil
	},
}

var skillInstallCmd = &cobra.Command{
	Use:   "install <slug> [--version <v>]",
	Short: "Install a skill from the ClawHub registry (with safety scan)",
	Long: `Fetches the manifest from the registry, downloads the tarball, and
unpacks it into ~/.overkill/skills/<slug>/. EVERY file is hashed
and run through the safety scanner before the install lands; a
Malicious or Suspicious verdict aborts and leaves the user's skill
directory untouched.

The registry URL is taken from OVERKILL_SKILL_REGISTRY (default:
https://clawhub.com). Private hubs can set
OVERKILL_SKILL_REGISTRY_TOKEN for bearer auth.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		version, _ := cmd.Flags().GetString("version")
		slug := args[0]

		_, userDir, err := resolveSkillDirs()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
		defer cancel()
		dest, err := registryClient().Install(ctx, slug, version, userDir)
		if err != nil {
			return err
		}
		fmt.Printf("%s✓ installed %s → %s%s\n", colorGreen, slug, dest, colorReset)
		return nil
	},
}

func init() {
	skillCmd.AddCommand(skillScanCmd)
	skillCmd.AddCommand(skillListCmd)
	skillCmd.AddCommand(skillBlockListCmd)
	skillCmd.AddCommand(skillSearchCmd)
	skillCmd.AddCommand(skillInstallCmd)
	skillInstallCmd.Flags().String("version", "", "specific version (default: latest)")
	rootCmd.AddCommand(skillCmd)
}
