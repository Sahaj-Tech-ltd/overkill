// Package multimodal — "understand anything" extraction pipeline
// (master plan §7.5 / Batch I).
//
// The contract: hand the package a file path, get back text + a tiny
// metadata bag. PDFs come back as text + page count; audio comes back
// as transcript + duration; images come back as a vision-describer
// caption + dimensions; DOCX comes back via pandoc; plain text + code
// files come back verbatim. Never "I can't handle that" — at minimum
// the binary-fallback extractor reports MIME + size so the agent has
// a verbal handle on the file even when nothing extracts cleanly.
//
// Design:
//
//   - Extractor interface so adapters plug in (PDF, audio, DOCX,
//     image, text, binary-fallback). Each declares which MIME types
//     and extensions it claims.
//   - Registry walks extractors in order, returns the first that
//     Supports() the file. The binary-fallback is registered last
//     so unknown content STILL gets a useful response.
//   - Shell-out extractors (pdftotext, whisper, pandoc) check the
//     binary's presence up-front and return a clear "install X"
//     error rather than failing mid-extraction. Tool sees the error,
//     surfaces it to the user.
package multimodal

import (
	"context"
	"fmt"
	"strings"
)

// Result is what every extractor returns. Text is the primary
// payload — the model reads this directly. Metadata holds bounded
// key/value pairs (pages, duration, dimensions) so the agent can
// reason about scope without parsing prose.
type Result struct {
	Text     string
	Metadata map[string]string
	// Extractor names which adapter produced this result. Useful
	// for audit logs + for the agent to know whether to trust the
	// content depth ("pdftotext" vs "binary-fallback").
	Extractor string
}

// Extractor is one content-type handler. Multiple extractors can
// claim the same MIME (e.g. text/plain via text and via fallback);
// the router picks the most-specific first.
type Extractor interface {
	Name() string
	// Supports reports whether this extractor can extract a file
	// with this MIME and extension. Lowercase comparisons only —
	// callers normalize before calling.
	Supports(mime, ext string) bool
	// Extract reads the file and produces Result. Errors are
	// transport (file unreadable, shell tool missing) rather than
	// "couldn't extract well" — partial extractions return Result
	// with a metadata note like "extraction_quality=partial".
	Extract(ctx context.Context, path string) (Result, error)
}

// Registry is the ordered list of extractors. Order matters: more
// specific extractors register first so they win the Lookup.
type Registry struct {
	extractors []Extractor
}

// NewRegistry returns an empty registry. Wire built-ins via
// DefaultRegistry() or Register() manually.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register appends an extractor. Order matters — register specific
// before general.
func (r *Registry) Register(e Extractor) {
	r.extractors = append(r.extractors, e)
}

// Lookup returns the first registered extractor that Supports(mime,
// ext). Nil when none match — in practice the binary fallback claims
// everything so this only nils on an empty registry.
func (r *Registry) Lookup(mime, ext string) Extractor {
	mime = strings.ToLower(strings.TrimSpace(mime))
	ext = strings.ToLower(strings.TrimSpace(ext))
	for _, e := range r.extractors {
		if e.Supports(mime, ext) {
			return e
		}
	}
	return nil
}

// Extract is the top-level entry point. Resolves mime + ext from the
// path, picks an extractor, runs it. Returns ErrNoExtractor when the
// registry is empty (only possible with a custom registry — the
// default always has a binary fallback).
func (r *Registry) Extract(ctx context.Context, path string) (Result, error) {
	mime, ext, err := Detect(path)
	if err != nil {
		return Result{}, fmt.Errorf("multimodal: detect %s: %w", path, err)
	}
	ex := r.Lookup(mime, ext)
	if ex == nil {
		return Result{}, ErrNoExtractor
	}
	return ex.Extract(ctx, path)
}

// ErrNoExtractor is returned when no extractor in the registry
// claims a file. With the default registry this never fires (the
// binary fallback claims everything).
var ErrNoExtractor = fmt.Errorf("multimodal: no extractor for this file")

// ErrMissingDependency is returned when a shell-out extractor can't
// find its binary (e.g. pdftotext, whisper). The caller propagates
// this to the user with the install hint embedded in the message.
type ErrMissingDependency struct {
	Tool      string // "pdftotext", "whisper", "pandoc"
	InstallEx string // example install command for the common platform
}

func (e *ErrMissingDependency) Error() string {
	if e.InstallEx == "" {
		return fmt.Sprintf("multimodal: %s not found in PATH", e.Tool)
	}
	return fmt.Sprintf("multimodal: %s not found in PATH (install: %s)", e.Tool, e.InstallEx)
}
