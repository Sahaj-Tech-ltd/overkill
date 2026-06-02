// Package multimodal — DOCX / DOC / RTF / ODT extractor via pandoc.
//
// Pandoc is the universal document converter. It handles every
// office format we'd plausibly see (docx, doc, rtf, odt, html, etc.)
// with one CLI. Same shell-out rationale as PDF/audio: the alternative
// is parsing OOXML + ODF ourselves, both of which are complex
// formats we'd never get right without months of work.
//
// Pandoc isn't always installed; the extractor surfaces a clear
// "install pandoc" error rather than a generic exec failure.
package multimodal

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// PandocExtractor handles office documents via pandoc.
type PandocExtractor struct {
	Timeout time.Duration
}

// NewPandocExtractor returns an extractor with a 60s default timeout.
func NewPandocExtractor() *PandocExtractor {
	return &PandocExtractor{Timeout: 60 * time.Second}
}

func (e *PandocExtractor) Name() string { return "pandoc" }

func (e *PandocExtractor) Supports(mime, ext string) bool {
	switch mime {
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/msword",
		"application/rtf",
		"application/vnd.oasis.opendocument.text":
		return true
	}
	switch ext {
	case ".docx", ".doc", ".rtf", ".odt":
		return true
	}
	return false
}

func (e *PandocExtractor) Extract(ctx context.Context, path string) (Result, error) {
	if _, err := exec.LookPath("pandoc"); err != nil {
		return Result{}, &ErrMissingDependency{
			Tool:      "pandoc",
			InstallEx: "apt install pandoc  /  brew install pandoc",
		}
	}
	to := e.Timeout
	if to <= 0 {
		to = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	// -t plain strips formatting — the model gets prose, not the
	// pandoc-AST. --wrap=none keeps the original line breaks
	// rather than wrapping at 72 cols (which mangles narrow content
	// in the model's view).
	cmd := exec.CommandContext(ctx, "pandoc", path, "-t", "plain", "--wrap=none")
	out, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("pandoc: %w", err)
	}
	return Result{
		Text:      strings.TrimSpace(string(out)),
		Metadata:  map[string]string{"format": "office"},
		Extractor: e.Name(),
	}, nil
}
