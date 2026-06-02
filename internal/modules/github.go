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

// extractTarGz extracts a .tar.gz stream into destDir.
// Strips the top-level directory prefix from all paths.
func extractTarGz(r io.Reader, destDir string) error {
	// Prevent gzip bomb: cap compressed input at 100 MB.
	gzReader, err := gzip.NewReader(io.LimitReader(r, 100<<20))
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	var stripPrefix string

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}

		// Strip the top-level directory (GitHub tarballs wrap in <repo>-<sha>/).
		if stripPrefix == "" && header.Typeflag == tar.TypeDir {
			stripPrefix = header.Name
			continue
		}
		relPath := strings.TrimPrefix(header.Name, stripPrefix)
		if relPath == "" || relPath == header.Name {
			relPath = filepath.Base(header.Name)
		}

		target := filepath.Join(destDir, relPath)

		// Sanity check: prevent path traversal.
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, 0o750)
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0o750)
			// Mask mode to prevent SUID/SGID/world-writable.
			mode := os.FileMode(header.Mode) & 0o755
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return fmt.Errorf("create %s: %w", relPath, err)
			}
			// Cap per-file size at 100 MB.
			if _, err := io.Copy(f, io.LimitReader(tarReader, 100<<20)); err != nil {
				f.Close()
				return fmt.Errorf("write %s: %w", relPath, err)
			}
			f.Close()
		}
	}

	return nil
}
