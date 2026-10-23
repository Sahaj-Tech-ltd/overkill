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
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	maxUpdateAPIBytes      = 1 << 20
	maxUpdateManifestBytes = 64 << 10
	maxUpdateBinaryBytes   = 512 << 20
)

var updateRepoPart = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

func validateUpdateRepo(repo string) error {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return errors.New("GitHub repository must be owner/name")
	}
	for _, part := range parts {
		if !updateRepoPart.MatchString(part) || part == "." || part == ".." {
			return errors.New("invalid GitHub repository")
		}
	}
	return nil
}
func trustedUpdateHTTPS(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || (u.Port() != "" && u.Port() != "443") {
		return nil, errors.New("update requires a trusted HTTPS release URL")
	}
	return u, nil
}
func newUpdateHTTPClient(assets bool) *http.Client {
	return &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if !assets {
			return errors.New("GitHub API redirects are not allowed")
		}
		if len(via) > 5 {
			return errors.New("too many release redirects")
		}
		u, err := trustedUpdateHTTPS(req.URL.String())
		if err != nil {
			return err
		}
		host := strings.ToLower(u.Hostname())
		if host != "release-assets.githubusercontent.com" && host != "objects.githubusercontent.com" {
			if host != "github.com" || len(via) == 0 || u.Path != via[0].URL.Path || u.RawQuery != "" {
				return errors.New("release redirect target is not allowed")
			}
		}
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
		return nil
	}}
}
func boundedUpdateBody(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("release response exceeds size limit")
	}
	return body, nil
}
func fetchLatestRelease(ctx context.Context, repo string) (*release, error) {
	return fetchLatestReleaseWithClient(ctx, repo, newUpdateHTTPClient(false))
}
func fetchLatestReleaseWithClient(ctx context.Context, repo string, client *http.Client) (*release, error) {
	if err := validateUpdateRepo(repo); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("no releases published yet")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}
	body, err := boundedUpdateBody(resp.Body, maxUpdateAPIBytes)
	if err != nil {
		return nil, err
	}
	var rel release
	if err = json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("parse release: %w", err)
	}
	if rel.TagName == "" || len(rel.TagName) > 256 || len(rel.Assets) > 128 {
		return nil, errors.New("invalid release metadata")
	}
	return &rel, nil
}
func updateAssetName(goos, goarch string) (string, error) {
	switch goos + "/" + goarch {
	case "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64":
		return "overkill-" + goos + "-" + goarch, nil
	case "windows/amd64":
		return "overkill-windows-amd64.exe", nil
	default:
		return "", fmt.Errorf("unsupported update target %s/%s", goos, goarch)
	}
}
func pickAsset(assets []releaseAsset) (*releaseAsset, error) {
	return pickAssetForPlatform(assets, runtime.GOOS, runtime.GOARCH)
}
func pickAssetForPlatform(assets []releaseAsset, goos, goarch string) (*releaseAsset, error) {
	name, err := updateAssetName(goos, goarch)
	if err != nil {
		return nil, err
	}
	var chosen *releaseAsset
	for i := range assets {
		if assets[i].Name == name {
			if chosen != nil {
				return nil, fmt.Errorf("duplicate release asset %s", name)
			}
			copy := assets[i]
			chosen = &copy
		}
	}
	if chosen == nil {
		return nil, fmt.Errorf("no exact release asset %s", name)
	}
	return chosen, nil
}
func validateReleaseAssetURL(raw, repo, tag, name string) error {
	u, err := trustedUpdateHTTPS(raw)
	if err != nil {
		return err
	}
	if err = validateUpdateRepo(repo); err != nil {
		return err
	}
	if tag == "" || len(tag) > 256 || strings.ContainsAny(tag, "\x00\r\n\\") {
		return errors.New("invalid release tag")
	}
	for _, r := range tag {
		if r < 32 || r == 127 {
			return errors.New("invalid release tag")
		}
	}
	for _, part := range strings.Split(tag, "/") {
		if part == "." || part == ".." || part == "" {
			return errors.New("invalid release tag")
		}
	}
	if strings.ToLower(u.Hostname()) != "github.com" || u.RawQuery != "" || u.Path != "/"+repo+"/releases/download/"+tag+"/"+name {
		return errors.New("asset URL does not match the selected GitHub repository, tag and basename")
	}
	return nil
}
func parseUpdateChecksum(body []byte, assetName string) (string, error) {
	seen := make(map[string]bool)
	expected := ""
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return "", errors.New("malformed checksum manifest")
		}
		hash, name := fields[0], strings.TrimPrefix(fields[1], "*")
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(hash) != 64 || len(decoded) != sha256.Size || name == "" || strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
			return "", errors.New("malformed checksum entry")
		}
		if seen[name] {
			return "", fmt.Errorf("duplicate checksum entry %s", name)
		}
		seen[name] = true
		if name == assetName {
			expected = strings.ToLower(hash)
		}
	}
	if expected == "" {
		return "", fmt.Errorf("required checksum missing for %s", assetName)
	}
	return expected, nil
}
func lookupExpectedSHA(ctx context.Context, repo string, rel *release, assetName string, client *http.Client) (string, error) {
	if rel == nil {
		return "", errors.New("release metadata missing")
	}
	var manifest *releaseAsset
	for i := range rel.Assets {
		if rel.Assets[i].Name == "checksums.txt" {
			if manifest != nil {
				return "", errors.New("duplicate checksums.txt asset")
			}
			manifest = &rel.Assets[i]
		}
	}
	if manifest == nil {
		return "", errors.New("required checksums.txt asset missing")
	}
	if err := validateReleaseAssetURL(manifest.BrowserDownloadURL, repo, rel.TagName, "checksums.txt"); err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifest.BrowserDownloadURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("required checksum download returned HTTP %d", resp.StatusCode)
	}
	body, err := boundedUpdateBody(resp.Body, maxUpdateManifestBytes)
	if err != nil {
		return "", err
	}
	return parseUpdateChecksum(body, assetName)
}
func downloadToWithHash(ctx context.Context, raw, dst string, expectedSize int, client *http.Client) (string, error) {
	if expectedSize < 1 || expectedSize > maxUpdateBinaryBytes {
		return "", errors.New("release binary has invalid size")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("binary download returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxUpdateBinaryBytes {
		return "", errors.New("release binary exceeds size limit")
	}
	file, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(dst)
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, int64(expectedSize)+1))
	if err != nil {
		return "", err
	}
	if n != int64(expectedSize) {
		return "", errors.New("downloaded binary size differs from release metadata")
	}
	if err = file.Sync(); err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	ok = true
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func rejectDesktopSelfUpdate(dst string) error {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(dst)), "/")
	for i := 0; i+3 < len(parts); i++ {
		if strings.HasSuffix(strings.ToLower(parts[i]), ".app") && parts[i+1] == "Contents" && parts[i+2] == "Resources" && parts[i+3] == "bin" {
			return errors.New("desktop updates require installing the DMG; bundled backend self-update and rollback are disabled")
		}
	}
	return nil
}

type installedUpdate struct{ SHA256, StagedPath string }

func installUpdateRelease(ctx context.Context, repo string, rel *release, dst, goos, goarch string, client *http.Client) (installedUpdate, error) {
	var result installedUpdate
	if err := rejectDesktopSelfUpdate(dst); err != nil {
		return result, err
	}
	if rel == nil {
		return result, errors.New("release metadata missing")
	}
	asset, err := pickAssetForPlatform(rel.Assets, goos, goarch)
	if err != nil {
		return result, err
	}
	if err = validateReleaseAssetURL(asset.BrowserDownloadURL, repo, rel.TagName, asset.Name); err != nil {
		return result, err
	}
	expected, err := lookupExpectedSHA(ctx, repo, rel, asset.Name, client)
	if err != nil {
		return result, err
	}
	temp, err := os.CreateTemp(filepath.Dir(dst), ".overkill-update-*")
	if err != nil {
		return result, err
	}
	tmp := temp.Name()
	if err = temp.Close(); err != nil {
		_ = os.Remove(tmp)
		return result, err
	}
	defer os.Remove(tmp)
	got, err := downloadToWithHash(ctx, asset.BrowserDownloadURL, tmp, asset.Size, client)
	if err != nil {
		return result, err
	}
	if got != expected {
		return result, errors.New("release binary SHA256 mismatch")
	}
	if err = os.Chmod(tmp, 0755); err != nil {
		return result, err
	}
	if goos == "windows" {
		staged := dst + ".new"
		if err = os.Rename(tmp, staged); err != nil {
			return result, err
		}
		return installedUpdate{SHA256: got, StagedPath: staged}, nil
	}
	if err = publishVerifiedUpdate(dst, tmp, os.Rename); err != nil {
		return result, err
	}
	return installedUpdate{SHA256: got}, nil
}

// Copy the old bytes into a durable backup before one atomic rename over the live path.
// A publication failure leaves the original executable at its live path.
func publishVerifiedUpdate(dst, tmp string, rename func(string, string) error) error {
	backup, err := os.CreateTemp(filepath.Dir(dst), ".overkill-backup-*")
	if err != nil {
		return err
	}
	name := backup.Name()
	defer os.Remove(name)
	source, err := os.Open(dst)
	if err != nil {
		_ = backup.Close()
		return err
	}
	info, err := source.Stat()
	if err == nil {
		_, err = io.Copy(backup, source)
	}
	_ = source.Close()
	if err == nil {
		err = backup.Chmod(info.Mode().Perm())
	}
	if err == nil {
		err = backup.Sync()
	}
	closeErr := backup.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = rename(name, dst+".bak"); err != nil {
		return fmt.Errorf("retain backup: %w", err)
	}
	if err = rename(tmp, dst); err != nil {
		return fmt.Errorf("atomic replacement failed; original executable retained: %w", err)
	}
	return nil
}
