// Package multimodal — binary fallback extractor.
//
// The "never say can't handle" floor. When no specific extractor
// claims a file (unknown binary format, corrupted bytes,
// uncategorized custom format), this one returns a useful Result:
// MIME + size + first-bytes hex. The agent gets enough to reason
// about the file's existence even when full content extraction
// isn't possible.
//
// Register this LAST so specific extractors always win.
package multimodal

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
)

// BinaryFallback claims every file. Returns metadata-only Result.
type BinaryFallback struct{}

// NewBinaryFallback returns the fallback extractor.
func NewBinaryFallback() *BinaryFallback { return &BinaryFallback{} }

func (b *BinaryFallback) Name() string { return "binary-fallback" }

// Supports always returns true. This is the catch-all.
func (b *BinaryFallback) Supports(mime, ext string) bool { return true }

func (b *BinaryFallback) Extract(ctx context.Context, path string) (Result, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Result{}, fmt.Errorf("binary: stat %s: %w", path, err)
	}
	mime, _, _ := Detect(path) // ignore errors; we already opened the file

	// First 64 bytes as hex — enough for an agent to recognise
	// common signatures (PE, ELF, Mach-O, ZIP, etc.) and decide
	// whether to ask the user for a different file.
	head := []byte{}
	if f, err := os.Open(path); err == nil {
		buf := make([]byte, 64)
		n, _ := f.Read(buf)
		head = buf[:n]
		f.Close()
	}

	text := fmt.Sprintf(
		"[binary file: %s, %s] No text extraction available for this MIME type. "+
			"First-bytes signature: %s",
		mime,
		humanBytesSize(int(info.Size())),
		hex.EncodeToString(head),
	)
	return Result{
		Text: text,
		Metadata: map[string]string{
			"mime":  mime,
			"bytes": strconv.FormatInt(info.Size(), 10),
		},
		Extractor: b.Name(),
	}, nil
}
