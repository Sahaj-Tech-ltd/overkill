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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	dst, _ = filepath.EvalSymlinks(dst)

	tmp := dst + ".new"
	fmt.Printf("%sdownloading…%s\n", colorBlue, colorReset)
	gotSHA, err := downloadToWithHash(cmd.Context(), asset.BrowserDownloadURL, tmp)
	if err != nil {
		return fmt.Errorf("update: download: %w", err)
	}

	// Verify against the release's checksums.txt if present. A
	// checksum mismatch is fatal — we'd rather refuse the install
	// than swap in a tampered or corrupted binary. Absent checksums
	// fall through (install proceeds without verification) so a
	// release without the file still updates, just with weaker
	// integrity guarantees.
	if expected := lookupExpectedSHA(rel, asset.Name); expected != "" {
		if !strings.EqualFold(expected, gotSHA) {
			_ = os.Remove(tmp)
			return fmt.Errorf("update: checksum mismatch — got %s, want %s (artifact may be corrupted or tampered)", gotSHA, expected)
		}
		fmt.Printf("%s✓ checksum verified (sha256:%s…)%s\n", colorGreen, gotSHA[:12], colorReset)
	}

	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("update: chmod: %w", err)
	}

	// On Windows the running binary is locked — we can't rename over
	// it. Leave the new binary at <dst>.new and instruct the user
	// to swap manually after restarting.
	if runtime.GOOS == "windows" {
		fmt.Printf("%s✓ downloaded to %s%s\n", colorGreen, tmp, colorReset)
		fmt.Printf("%son Windows the running binary is locked. Stop overkill, then:%s\n", colorYellow, colorReset)
		fmt.Printf("    move %s %s\n", tmp, dst)
		return nil
	}

	// POSIX atomic swap with .bak retention so the user can roll
	// back if the new binary misbehaves. Order matters:
	//   1. Stash old → .bak (atomic rename, same dir).
	//   2. New → live position.
	//   3. If step 2 fails, restore .bak so the user isn't left
	//      without a working binary.
	backup := dst + ".bak"
	_ = os.Remove(backup) // wipe stale .bak from a prior install

	if err := os.Rename(dst, backup); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("update: stash backup: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		// Try to restore the original so the user has a working
		// binary. If THAT fails too, surface both errors — manual
		// recovery needed.
		if rerr := os.Rename(backup, dst); rerr != nil {
			return fmt.Errorf("update: rename failed (%w); rollback failed (%v) — recover with: mv %s %s", err, rerr, backup, dst)
		}
		_ = os.Remove(tmp)
		return fmt.Errorf("update: rename failed, rolled back to original: %w", err)
	}

	fmt.Printf("%s✓ updated to %s — previous saved as %s%s\n", colorGreen, rel.TagName, backup, colorReset)
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
	dst, _ = filepath.EvalSymlinks(dst)
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

// lookupExpectedSHA finds the SHA-256 for assetName in the release's
// checksums.txt asset. Returns "" when no checksums.txt is published,
// when fetching it fails, or when assetName isn't listed. Caller
// treats "" as "skip verification" — better than failing a legitimate
// install over a missing-but-not-required checksum file.
//
// Format: sha256sum text mode, "<hex>  <filename>" per line. Binary-
// mode lines start with "*"; we strip the prefix when matching.
func lookupExpectedSHA(rel *release, assetName string) string {
	if rel == nil {
		return ""
	}
	var checksumsURL string
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Name, "checksums.txt") {
			checksumsURL = a.BrowserDownloadURL
			break
		}
	}
	if checksumsURL == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, checksumsURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hash := fields[0]
		name := strings.TrimPrefix(fields[1], "*")
		if strings.EqualFold(name, assetName) {
			return strings.ToLower(hash)
		}
	}
	return ""
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

func fetchLatestRelease(ctx context.Context, repo string) (*release, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("no releases published yet")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GitHub API %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("parse release: %w", err)
	}
	return &r, nil
}

// pickAsset finds the asset whose name contains both GOOS and GOARCH. Falls
// back to a substring match on either when no exact match exists.
func pickAsset(assets []releaseAsset) (*releaseAsset, error) {
	osTag := runtime.GOOS
	archTag := runtime.GOARCH
	var byOS, byArch *releaseAsset
	for i := range assets {
		a := assets[i]
		name := strings.ToLower(a.Name)
		hasOS := strings.Contains(name, osTag)
		hasArch := strings.Contains(name, archTag)
		if hasOS && hasArch {
			return &a, nil
		}
		if hasOS && byOS == nil {
			byOS = &a
		}
		if hasArch && byArch == nil {
			byArch = &a
		}
	}
	if byOS != nil {
		return byOS, nil
	}
	if byArch != nil {
		return byArch, nil
	}
	if len(assets) == 1 {
		return &assets[0], nil
	}
	return nil, fmt.Errorf("no asset matches %s/%s in release", osTag, archTag)
}

func downloadTo(ctx context.Context, url, dst string) error {
	_, err := downloadToWithHash(ctx, url, dst)
	return err
}

// downloadToWithHash writes the response to dst AND computes its
// SHA-256 in a single pass via io.MultiWriter. Returns the lowercase
// hex hash so the caller can verify against a published checksums.txt
// without re-reading the temp file.
func downloadToWithHash(ctx context.Context, url, dst string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(dctx, http.MethodGet, url, nil)
	c := &http.Client{Timeout: 5 * time.Minute}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("download %d: %s", resp.StatusCode, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, hasher), resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(dst)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(dst)
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
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
