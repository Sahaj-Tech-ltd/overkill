package modules

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var httpClient = &http.Client{
	Timeout: 60 * time.Second,
}

// httpGet performs a GET request and returns the response.
// Caller must close resp.Body.
func httpGet(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "overkill-modules/1.0")
	req.Header.Set("Accept", "application/json, application/octet-stream")
	return httpClient.Do(req)
}

// Archive limits apply to the entire decoded stream, including tar headers and
// padding, as well as to each file and the total number of entries.
type archiveLimits struct {
	compressed, expanded, file int64
	entries                    int
}

var moduleArchiveLimits = archiveLimits{100 << 20, 256 << 20, 100 << 20, 10000}

// extractTarGz expects a fresh staging directory; callers publish it only after
// all entries and the gzip trailer have been validated.
func extractTarGz(r io.Reader, destDir string) error {
	return extractTarGzLimited(r, destDir, moduleArchiveLimits)
}

func extractTarGzLimited(r io.Reader, destDir string, limits archiveLimits) error {
	if limits.compressed < 1 || limits.expanded < 1 || limits.file < 1 || limits.entries < 1 {
		return fmt.Errorf("archive: invalid limits")
	}
	compressed := &io.LimitedReader{R: r, N: limits.compressed + 1}
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	expanded := &io.LimitedReader{R: gz, N: limits.expanded + 1}
	tr := tar.NewReader(expanded)
	root := ""
	seen := make(map[string]bool)
	entries, files := 0, 0
	var logicalBytes int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		entries++
		if entries > limits.entries {
			return fmt.Errorf("archive: too many entries (maximum %d)", limits.entries)
		}
		name := strings.TrimSuffix(h.Name, "/")
		if len(name) > 4096 || name == "" || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
			return fmt.Errorf("archive: unsafe path %q", h.Name)
		}
		parts := strings.Split(name, "/")
		for _, part := range parts {
			if part == "" || part == "." || part == ".." {
				return fmt.Errorf("archive: unsafe path %q", h.Name)
			}
		}
		if root == "" {
			root = parts[0]
		}
		if parts[0] != root {
			return fmt.Errorf("archive: multiple top-level roots")
		}
		if h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg {
			return fmt.Errorf("archive: unsupported entry type for %q", h.Name)
		}
		if len(parts) == 1 {
			if h.Typeflag != tar.TypeDir {
				return fmt.Errorf("archive: expected top-level directory")
			}
			continue
		}
		relative := filepath.Join(parts[1:]...)
		key := strings.ToLower(relative)
		if seen[key] {
			return fmt.Errorf("archive: duplicate path %q", relative)
		}
		seen[key] = true
		target := filepath.Join(destDir, relative)
		if h.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return fmt.Errorf("archive mkdir: %w", err)
			}
			continue
		}
		if h.Size < 0 || h.Size > limits.file {
			return fmt.Errorf("archive: file %q exceeds maximum %d bytes", relative, limits.file)
		}
		// archive/tar can synthesize sparse zero ranges: decoded tar bytes
		// alone do not bound logical bytes written to disk. Count advertised
		// logical sizes as well, before creating any oversized aggregate entry.
		if logicalBytes > limits.expanded-h.Size {
			return fmt.Errorf("archive: logical expansion exceeds maximum %d bytes", limits.expanded)
		}
		logicalBytes += h.Size
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return fmt.Errorf("archive mkdir: %w", err)
		}
		// No archive links or overwrite operations are permitted in the fresh tree.
		f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(h.Mode)&0o755)
		if err != nil {
			return fmt.Errorf("archive create: %w", err)
		}
		n, copyErr := io.CopyN(f, tr, h.Size)
		closeErr := f.Close()
		if copyErr != nil {
			return fmt.Errorf("archive write %q (%d/%d): %w", relative, n, h.Size, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("archive close: %w", closeErr)
		}
		files++
	}
	// Reading through gzip EOF detects truncated/checksum-bad trailers and
	// excess decoded padding, even after tar's end-of-archive zero blocks.
	if _, err := io.Copy(io.Discard, expanded); err != nil {
		return fmt.Errorf("gzip trailer: %w", err)
	}
	if compressed.N <= 0 {
		return fmt.Errorf("archive: compressed input exceeds maximum %d bytes", limits.compressed)
	}
	if expanded.N <= 0 {
		return fmt.Errorf("archive: expanded input exceeds maximum %d bytes", limits.expanded)
	}
	if files == 0 {
		return fmt.Errorf("archive: no regular files")
	}
	return nil
}
