// Package multimodal — PDF extractor via pdftotext (poppler-utils).
//
// Why pdftotext: pure-Go PDF text extraction libraries exist (e.g.
// rsc.io/pdf, ledongthuc/pdf, unidoc) but they're either incomplete
// (rsc reads structured PDFs but skips embedded fonts), heavy
// (unidoc is GPL+commercial), or both. pdftotext is the industry-
// standard reference implementation, installed by default on most
// Linux distros and one brew/apt away otherwise. The shell-out cost
// is dwarfed by the LLM round-trip cost downstream.
package multimodal

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// PDFExtractor extracts text + page count via pdftotext + pdfinfo.
type PDFExtractor struct {
	// Layout, when true, passes -layout to pdftotext to preserve
	// approximate column alignment. Default false — most PDFs read
	// better in raw mode for a model.
	Layout bool
	// MaxBytes caps the extracted text size so a 500-page document
	// doesn't blow the model's context budget. Default 256KB. The
	// metadata includes the original page count regardless of cap.
	MaxBytes int
	// Timeout caps the per-extract budget. Default 30s — enough for
	// most documents, short enough that a malformed PDF doesn't
	// hang the agent loop.
	Timeout time.Duration
}

// NewPDFExtractor returns an extractor with sensible defaults.
func NewPDFExtractor() *PDFExtractor {
	return &PDFExtractor{MaxBytes: 256 * 1024, Timeout: 30 * time.Second}
}

func (p *PDFExtractor) Name() string { return "pdftotext" }

func (p *PDFExtractor) Supports(mime, ext string) bool {
	return mime == "application/pdf" || ext == ".pdf"
}

func (p *PDFExtractor) Extract(ctx context.Context, path string) (Result, error) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		return Result{}, &ErrMissingDependency{
			Tool:      "pdftotext",
			InstallEx: "apt install poppler-utils  /  brew install poppler",
		}
	}

	to := p.Timeout
	if to <= 0 {
		to = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	// Page count first — cheap (pdfinfo just reads the header). If
	// it's missing we don't fail extraction; metadata just omits
	// the page count.
	pages := pdfPageCount(ctx, path)

	args := []string{}
	if p.Layout {
		args = append(args, "-layout")
	}
	args = append(args, path, "-")
	cmd := exec.CommandContext(ctx, "pdftotext", args...)
	out, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("pdftotext: %w", err)
	}

	text := string(out)
	cap := p.MaxBytes
	if cap <= 0 {
		cap = 256 * 1024
	}
	truncated := false
	if len(text) > cap {
		text = text[:cap]
		truncated = true
	}

	meta := map[string]string{}
	if pages > 0 {
		meta["pages"] = strconv.Itoa(pages)
	}
	if truncated {
		meta["truncated"] = "true"
		meta["original_bytes"] = strconv.Itoa(len(out))
	}

	return Result{
		Text:      strings.TrimSpace(text),
		Metadata:  meta,
		Extractor: p.Name(),
	}, nil
}

// pdfPageCount asks pdfinfo for the page count. Returns 0 on any
// failure — we tolerate a missing pdfinfo (poppler-utils ships both
// but some minimal installs split them).
func pdfPageCount(ctx context.Context, path string) int {
	if _, err := exec.LookPath("pdfinfo"); err != nil {
		return 0
	}
	out, err := exec.CommandContext(ctx, "pdfinfo", path).Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "Pages:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		n, _ := strconv.Atoi(fields[1])
		return n
	}
	return 0
}
