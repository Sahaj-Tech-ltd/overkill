// Package multimodal — text / code / structured-text extractor.
//
// The straight-through path: file is text-like by MIME or extension,
// we read it and return contents verbatim. Capped at 256KB so a giant
// log file doesn't blow context.
//
// This is also the catch-all for ALL programming-language source —
// .go/.py/.rs/.ts/etc don't get a special MIME from
// http.DetectContentType (they sniff as text/plain) but the extension
// list keeps them on this happy path.
package multimodal

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// TextExtractor reads plain text + structured-text + code files.
type TextExtractor struct {
	// MaxBytes caps how much of the file we return. Default 256KB.
	MaxBytes int
}

// NewTextExtractor returns an extractor with sensible defaults.
func NewTextExtractor() *TextExtractor {
	return &TextExtractor{MaxBytes: 256 * 1024}
}

func (t *TextExtractor) Name() string { return "text" }

// textExts are extensions we treat as text even when MIME sniffing
// disagrees. Adding here = supported.
var textExts = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".rst": true,
	".log": true, ".csv": true, ".tsv": true,
	// Code
	".go": true, ".py": true, ".rs": true, ".ts": true, ".tsx": true,
	".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".c": true, ".h": true, ".cpp": true, ".cc": true, ".hpp": true,
	".java": true, ".kt": true, ".swift": true, ".rb": true, ".php": true,
	".sh": true, ".bash": true, ".zsh": true, ".fish": true,
	".lua": true, ".pl": true, ".r": true, ".jl": true, ".dart": true,
	// Config / structured
	".json": true, ".yaml": true, ".yml": true, ".toml": true,
	".xml": true, ".ini": true, ".cfg": true, ".conf": true,
	".env": true, ".gitignore": true, ".dockerignore": true,
	// Project files
	".sum": true, ".mod": true, ".lock": true,
}

func (t *TextExtractor) Supports(mime, ext string) bool {
	if IsTextLike(mime) {
		return true
	}
	return textExts[ext]
}

func (t *TextExtractor) Extract(ctx context.Context, path string) (Result, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Result{}, fmt.Errorf("text: stat %s: %w", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("text: read %s: %w", path, err)
	}
	cap := t.MaxBytes
	if cap <= 0 {
		cap = 256 * 1024
	}
	body := string(data)
	truncated := false
	if len(body) > cap {
		body = body[:cap]
		truncated = true
	}
	meta := map[string]string{
		"bytes": strconv.FormatInt(info.Size(), 10),
		"lines": strconv.Itoa(strings.Count(body, "\n") + 1),
	}
	if truncated {
		meta["truncated"] = "true"
	}
	return Result{
		Text:      body,
		Metadata:  meta,
		Extractor: t.Name(),
	}, nil
}
