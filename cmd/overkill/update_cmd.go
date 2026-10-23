// Package main — `overkill update` (master plan §7.6).
//
// Checks the GitHub releases API for a newer tag than `Version`, downloads
// the matching binary asset for the current GOOS/GOARCH, and atomic-renames
// it over the running executable. The download path is a sibling tempfile
// so a failed download never replaces the live binary.
//
// Repo coordinates are configured via env (OVERKILL_UPDATE_REPO=owner/name);
// fallback is "Sahaj-Tech-ltd/overkill". Set OVERKILL_UPDATE_REPO="" to disable.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const defaultUpdateRepo = "Sahaj-Tech-ltd/overkill"

var (
	updateCheckOnly bool
	updateRepo      string
	updateRollback  bool
	updateYes       bool
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Check for and install a newer overkill release (master plan §7.6)",
	RunE:  runUpdate,
}

func init() {
	updateCmd.Flags().BoolVar(&updateCheckOnly, "check", false, "report availability without installing")
	updateCmd.Flags().StringVar(&updateRepo, "repo", "", "GitHub repo (owner/name); falls back to OVERKILL_UPDATE_REPO or built-in default")
	updateCmd.Flags().BoolVar(&updateRollback, "rollback", false, "restore the previous binary from .bak")
	updateCmd.Flags().BoolVarP(&updateYes, "yes", "y", false, "skip confirmation prompt before install")
	rootCmd.AddCommand(updateCmd)
}

// runUpdate is the main entry point.
func runUpdate(cmd *cobra.Command, args []string) error {
	if updateRollback {
		return runUpdateRollback()
	}

	if !updateCheckOnly {
		executable, e := os.Executable()
		if e != nil {
			return e
		}
		resolved, e := filepath.EvalSymlinks(executable)
		if e != nil {
			return e
		}
		if e = rejectDesktopSelfUpdate(resolved); e != nil {
			return e
		}
	}
	repo := resolveUpdateRepo()
	if repo == "" {
		fmt.Printf("%supdates disabled (no repo configured)%s\n", colorYellow, colorReset)
		return nil
	}
	rel, err := fetchLatestRelease(cmd.Context(), repo)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if !isNewer(rel.TagName, Version) {
		fmt.Printf("%sup to date (running %s, latest %s)%s\n", colorGreen, Version, rel.TagName, colorReset)
		return nil
	}
	fmt.Printf("%snew release available: %s → %s%s\n", colorBlue, Version, rel.TagName, colorReset)

	if updateCheckOnly {
		return nil
	}

	asset, err := pickAsset(rel.Assets)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}

	if !updateYes {
		fmt.Printf("install %s? [y/N] ", asset.Name)
		yes, err := readInteractiveYes()
		if err != nil {
			return err
		}
		if !yes {
			fmt.Printf("%scancelled%s\n", colorYellow, colorReset)
			return nil
		}
	}

	dst, err := os.Executable()
	if err != nil {
		return fmt.Errorf("update: locating self: %w", err)
	}
	dst, err = filepath.EvalSymlinks(dst)
	if err != nil {
		return fmt.Errorf("update: resolving executable: %w", err)
	}

	if err := rejectDesktopSelfUpdate(dst); err != nil {
		return err
	}
	fmt.Printf("%sdownloading and verifying release…%s\n", colorBlue, colorReset)
	result, err := installUpdateRelease(cmd.Context(), repo, rel, dst, runtime.GOOS, runtime.GOARCH, newUpdateHTTPClient(true))
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	fmt.Printf("%s✓ checksum verified (sha256:%s…)%s\n", colorGreen, result.SHA256[:12], colorReset)
	if result.StagedPath != "" {
		fmt.Printf("%s✓ verified download saved to %s%s\n", colorGreen, result.StagedPath, colorReset)
		fmt.Printf("%son Windows the running binary is locked. Stop overkill, then manually replace %s with this verified file.%s\n", colorYellow, dst, colorReset)
		return nil
	}
	fmt.Printf("%s✓ updated to %s — previous saved as %s%s\n", colorGreen, rel.TagName, dst+".bak", colorReset)
	fmt.Printf("  run `overkill update --rollback` to undo\n")

	return nil
}

// runUpdateRollback restores the .bak backup over the live binary.
// No-op when no .bak is present so the command is safe to repeat.
func runUpdateRollback() error {
	dst, err := os.Executable()
	if err != nil {
		return fmt.Errorf("rollback: locating self: %w", err)
	}
	dst, err = filepath.EvalSymlinks(dst)
	if err != nil {
		return fmt.Errorf("update: resolving executable: %w", err)
	}
	return rollbackUpdateAt(dst)
}

func rollbackUpdateAt(dst string) error {
	if err := rejectDesktopSelfUpdate(dst); err != nil {
		return err
	}
	backup := dst + ".bak"
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		fmt.Printf("%snothing to roll back to (no %s present)%s\n", colorYellow, backup, colorReset)
		return nil
	}
	if err := os.Rename(backup, dst); err != nil {
		return fmt.Errorf("rollback: rename: %w", err)
	}
	fmt.Printf("%s✓ rolled back to previous binary%s\n", colorGreen, colorReset)
	return nil
}

// readInteractiveYes reads one line from stdin and returns true on
// "y" or "yes" (case-insensitive). Blank input is "no" — installing
// is the riskier choice so the default has to be the safe one.
func readInteractiveYes() (bool, error) {
	buf := make([]byte, 8)
	n, err := os.Stdin.Read(buf)
	if err != nil {
		return false, fmt.Errorf("update: read confirmation: %w", err)
	}
	ans := strings.ToLower(strings.TrimSpace(string(buf[:n])))
	return ans == "y" || ans == "yes", nil
}

// CheckUpdateAsync launches a non-blocking version check (master plan §7.6).
// Logs to stderr if a new release is available; never errors out. Used by
// the TUI on launch so users learn about updates without leaving the chat.
func CheckUpdateAsync() {
	if os.Getenv("OVERKILL_NO_UPDATE_CHECK") != "" {
		return
	}
	go func() {
		defer func() { _ = recover() }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		repo := resolveUpdateRepo()
		if repo == "" {
			return
		}
		rel, err := fetchLatestRelease(ctx, repo)
		if err != nil || rel == nil {
			return
		}
		if isNewer(rel.TagName, Version) {
			fmt.Fprintf(os.Stderr, "%s[update] new release available: %s → %s — run `overkill update`%s\n",
				colorBlue, Version, rel.TagName, colorReset)
		}
	}()
}

func resolveUpdateRepo() string {
	if updateRepo != "" {
		return updateRepo
	}
	if v, ok := os.LookupEnv("OVERKILL_UPDATE_REPO"); ok {
		return v
	}
	return defaultUpdateRepo
}

// release matches the GitHub releases API payload (only fields we use).
type release struct {
	TagName string         `json:"tag_name"`
	HTMLURL string         `json:"html_url"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int    `json:"size"`
}

// isNewer reports whether tag > current. Both arguments are normalized
// (leading "v" stripped, dev/dirty suffixes ignored). Empty current is
// treated as outdated.
func isNewer(tag, current string) bool {
	t := normalizeVersion(tag)
	c := normalizeVersion(current)
	if c == "" || c == "0.0.0" {
		return true
	}
	return semverCompare(t, c) > 0
}

func normalizeVersion(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "+-"); i > 0 {
		s = s[:i]
	}
	return s
}

// semverCompare returns -1, 0, +1. Tolerates non-semver tags by treating
// missing components as 0.
func semverCompare(a, b string) int {
	pa := versionParts(a)
	pb := versionParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] > pb[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

func versionParts(s string) [3]int {
	var out [3]int
	parts := strings.Split(s, ".")
	for i := 0; i < 3 && i < len(parts); i++ {
		n := 0
		for _, c := range parts[i] {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}
