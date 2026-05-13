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
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Check for and install a newer overkill release (master plan §7.6)",
	RunE:  runUpdate,
}

func init() {
	updateCmd.Flags().BoolVar(&updateCheckOnly, "check", false, "report availability without installing")
	updateCmd.Flags().StringVar(&updateRepo, "repo", "", "GitHub repo (owner/name); falls back to OVERKILL_UPDATE_REPO or built-in default")
	rootCmd.AddCommand(updateCmd)
}

// runUpdate is the main entry point.
func runUpdate(cmd *cobra.Command, args []string) error {
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
	dst, err := os.Executable()
	if err != nil {
		return fmt.Errorf("update: locating self: %w", err)
	}
	dst, _ = filepath.EvalSymlinks(dst)

	tmp := dst + ".new"
	if err := downloadTo(cmd.Context(), asset.BrowserDownloadURL, tmp); err != nil {
		return fmt.Errorf("update: download: %w", err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("update: chmod: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("update: replace: %w", err)
	}
	fmt.Printf("%s✓ updated to %s%s\n", colorGreen, rel.TagName, colorReset)
	return nil
}

// CheckUpdateAsync launches a non-blocking version check (master plan §7.6).
// Logs to stderr if a new release is available; never errors out. Used by
// the TUI on launch so users learn about updates without leaving the chat.
func CheckUpdateAsync() {
	if os.Getenv("ETHOS_NO_UPDATE_CHECK") != "" {
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
	if ctx == nil {
		ctx = context.Background()
	}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(dctx, http.MethodGet, url, nil)
	c := &http.Client{Timeout: 5 * time.Minute}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("download %d: %s", resp.StatusCode, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
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
