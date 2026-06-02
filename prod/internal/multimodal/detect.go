// Package multimodal — MIME + extension detection.
//
// Two signals feed Lookup: the file extension (cheap, deterministic,
// reliable in agent workflows) AND the magic-byte sniff via
// http.DetectContentType. We return BOTH so extractors can match on
// whichever they prefer.
//
// Extension wins when present + recognised. For files without an
// extension OR with a misleading one (".dat" containing PDF bytes),
// the sniff fills in. Some formats (DOCX, XLSX) need both signals
// because http.DetectContentType only sees "application/zip" — the
// underlying ZIP structure of OOXML files.
package multimodal

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Detect returns (mime, extension) for path. The extension keeps its
// leading dot, lowercased. Mime is the http.DetectContentType result
// (always non-empty; falls back to "application/octet-stream").
//
// File-not-found returns a clear error rather than masking it with a
// fallback — the tool layer surfaces this so the user knows their
// path was wrong, not their content was opaque.
func Detect(path string) (mime, ext string, err error) {
	ext = strings.ToLower(filepath.Ext(path))

	// Read the first 512 bytes for sniffing — http.DetectContentType's
	// documented sample window. Reading more is wasted I/O; reading
	// less doesn't catch ZIP's local-file-header signature.
	f, err := os.Open(path)
	if err != nil {
		return "", ext, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return "", ext, fmt.Errorf("read %s: %w", path, err)
	}
	mime = http.DetectContentType(buf[:n])

	// OOXML correction: DOCX/XLSX/PPTX are ZIP files. The sniffer
	// reports "application/zip"; the extension tells us which
	// office format it actually is. Other office-like formats fall
	// out the same way.
	if mime == "application/zip" {
		switch ext {
		case ".docx":
			mime = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		case ".xlsx":
			mime = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		case ".pptx":
			mime = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
		}
	}

	return mime, ext, nil
}

// IsTextLike reports whether mime suggests human-readable text. Used
// by the text-fallback extractor to decide whether to dump bytes as
// content vs report "binary, sniffed mime=...". We accept text/* AND
// known structured-text MIME types (json, xml, yaml, etc.).
func IsTextLike(mime string) bool {
	mime = strings.ToLower(mime)
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	switch mime {
	case "application/json", "application/xml", "application/yaml",
		"application/x-yaml", "application/javascript", "application/x-sh":
		return true
	}
	return false
}
