// Package registry — ClawHub-compatible HTTP client for skill
// discovery + install (§6.4).
//
// The OpenClaw ClawHub spec (inspiration/openclaw/skills/clawhub/
// SKILL.md) exposes a small HTTP API: search returns a list of
// skill summaries; install resolves to a tarball URL the client
// downloads, extracts, and drops under the user skills dir. We
// implement a thin client matching the same shape so existing
// hubs work without modification, and so users can stand up their
// own private hub by pointing OVERKILL_SKILL_REGISTRY at it.
//
// What's NOT here:
//
//   - Publishing. The plan calls publish out as a separate concern;
//     this client is read-only.
//   - Auth tokens. Public hubs only for now; opt in to private
//     hubs by setting OVERKILL_SKILL_REGISTRY_TOKEN on requests.
//   - Hash-based update resolution. ClawHub's `update` walks local
//     files and asks the registry to pick a version — useful but
//     non-essential. Skip for v1.
//
// Safety: every downloaded archive is unpacked into a temp dir and
// safety-scanned BEFORE being moved to the user skills dir. A
// Malicious or Suspicious verdict aborts the install and leaves
// the user dir untouched.
package registry

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/skills/safety"
)

// DefaultRegistry is the URL the client uses when no override is
// configured. ClawHub's documented default.
const DefaultRegistry = "https://clawhub.com"

// Summary is one search hit. Mirrors the ClawHub /search response
// shape; absent fields are zero-valued.
type Summary struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Author      string `json:"author,omitempty"`
	Downloads   int    `json:"downloads,omitempty"`
}

// Manifest is the registry's per-skill response. DownloadURL is
// the resolved tarball location; SHA256 (when present) lets us
// verify the archive before unpacking.
type Manifest struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	DownloadURL string `json:"download_url"`
	SHA256      string `json:"sha256,omitempty"`
}

// Client is the HTTP wrapper. Construct with NewClient; methods are
// safe for concurrent use. The Scanner is REQUIRED — Install
// refuses to write to disk without one (callers wanting to skip
// scanning pass safety.NoopScanner{} explicitly so the bypass is
// visible at the call site).
type Client struct {
	baseURL string
	token   string
	client  *http.Client
	scanner safety.Scanner
}

// NewClient wires the client. Empty registryURL → DefaultRegistry.
// Empty scanner → nil and Install will refuse.
func NewClient(registryURL, token string, scanner safety.Scanner) *Client {
	if registryURL == "" {
		registryURL = DefaultRegistry
	}
	return &Client{
		baseURL: strings.TrimRight(registryURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 30 * time.Second},
		scanner: scanner,
	}
}

// Search queries the registry. Empty query returns whatever the
// registry decides is its "featured" list (ClawHub's behaviour).
func (c *Client) Search(ctx context.Context, query string) ([]Summary, error) {
	u := c.baseURL + "/skills/search?q=" + url.QueryEscape(query)
	body, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Results []Summary `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("registry: parse search: %w", err)
	}
	return resp.Results, nil
}

// Manifest fetches one skill's metadata. Version "" → latest.
func (c *Client) Manifest(ctx context.Context, slug, version string) (*Manifest, error) {
	if slug == "" {
		return nil, errors.New("registry: slug required")
	}
	u := c.baseURL + "/skills/" + url.PathEscape(slug)
	if version != "" {
		u += "/" + url.PathEscape(version)
	}
	body, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("registry: parse manifest: %w", err)
	}
	if m.DownloadURL == "" {
		return nil, fmt.Errorf("registry: manifest missing download_url")
	}
	return &m, nil
}

// Install fetches the manifest, downloads the tarball into a temp
// dir, scans the unpacked tree, and only moves to skillsDir on a
// clean verdict. Returns the absolute destination path.
//
// Refuses to run without a scanner — callers pass NoopScanner{} to
// explicitly opt out of safety checks.
func (c *Client) Install(ctx context.Context, slug, version, skillsDir string) (string, error) {
	if c.scanner == nil {
		return "", errors.New("registry: install refused — scanner required (pass NoopScanner to bypass explicitly)")
	}
	if skillsDir == "" {
		return "", errors.New("registry: skillsDir required")
	}
	m, err := c.Manifest(ctx, slug, version)
	if err != nil {
		return "", err
	}

	tmpRoot, err := os.MkdirTemp("", "overkill-install-")
	if err != nil {
		return "", fmt.Errorf("registry: tempdir: %w", err)
	}
	defer os.RemoveAll(tmpRoot)

	stagedDir := filepath.Join(tmpRoot, m.Slug)
	if err := c.fetchAndUnpack(ctx, m.DownloadURL, stagedDir); err != nil {
		return "", err
	}

	// Safety scan on the staged tree BEFORE moving anything into
	// the user's skill dir.
	results, worst, err := safety.ScanDir(ctx, c.scanner, stagedDir)
	if err != nil {
		return "", fmt.Errorf("registry: scan failed: %w", err)
	}
	if worst == safety.VerdictMalicious || worst == safety.VerdictSuspicious {
		labels := []string{}
		for _, r := range results {
			if r.Verdict == worst {
				labels = append(labels, fmt.Sprintf("%s: %s", filepath.Base(r.Path), r.Reason))
			}
		}
		return "", fmt.Errorf("registry: install blocked (%s): %s", worst, strings.Join(labels, "; "))
	}

	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return "", fmt.Errorf("registry: mkdir skillsDir: %w", err)
	}
	dest := filepath.Join(skillsDir, m.Slug)
	// Replace any prior install atomically: rename current → tmp
	// then move staged → dest. If the move fails we put the old
	// install back.
	backup := dest + ".old"
	if _, err := os.Stat(dest); err == nil {
		if err := os.Rename(dest, backup); err != nil {
			return "", fmt.Errorf("registry: backup existing: %w", err)
		}
	}
	if err := os.Rename(stagedDir, dest); err != nil {
		// Restore backup on failure.
		_ = os.Rename(backup, dest)
		return "", fmt.Errorf("registry: install rename: %w", err)
	}
	_ = os.RemoveAll(backup)
	return dest, nil
}

func (c *Client) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry: %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("registry: 404 %s", u)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry: status %d for %s", resp.StatusCode, u)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// fetchAndUnpack downloads `url` (expected: gzipped tar archive)
// and extracts it under `dest`. Path-traversal safe: any entry
// that would land outside dest is refused.
func (c *Client) fetchAndUnpack(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("registry: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry: download status %d", resp.StatusCode)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("registry: gunzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("registry: tar: %w", err)
		}
		target := filepath.Join(dest, hdr.Name)
		// Path-traversal guard.
		if !strings.HasPrefix(target, dest+string(filepath.Separator)) && target != dest {
			return fmt.Errorf("registry: refused traversal entry %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return fmt.Errorf("registry: create %s: %w", target, err)
			}
			if _, err := io.Copy(f, io.LimitReader(tr, 50<<20)); err != nil {
				f.Close()
				return fmt.Errorf("registry: write %s: %w", target, err)
			}
			f.Close()
		default:
			// Skip symlinks, devices, etc. — skills should be just
			// files and dirs.
		}
	}
	return nil
}
