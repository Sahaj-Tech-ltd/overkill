// Package tools — `understand_anything` tool (Batch I).
//
// One arg: path. Returns extracted text + metadata. The agent reads
// this on the next turn and can reason about ANY file — PDF, audio,
// office docs, code, configs, images, even unknown binaries (which
// come back as "binary file: <mime> <size> <first-bytes-hex>"
// instead of an error). The "no, I CAN handle that file" UX.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/multimodal"
)

// UnderstandTool wraps a multimodal.Registry. The agent calls
// Execute with {"path": "..."}.
type UnderstandTool struct {
	Registry *multimodal.Registry
	// Cwd is used to resolve relative paths. Empty allows absolute-
	// only paths.
	Cwd string
}

// NewUnderstandTool wires a registry + cwd.
func NewUnderstandTool(r *multimodal.Registry, cwd string) *UnderstandTool {
	return &UnderstandTool{Registry: r, Cwd: cwd}
}

func (u *UnderstandTool) Name() string { return "understand_anything" }

type understandInput struct {
	Path string `json:"path"`
	// Prompt steers the image/audio extractor when relevant.
	// Ignored by PDF/text/etc.
	Prompt string `json:"prompt,omitempty"`
}

type understandOutput struct {
	Text      string            `json:"text"`
	Extractor string            `json:"extractor"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

func (u *UnderstandTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	var req understandInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("understand_anything: parse: %w", err)
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" {
		return nil, fmt.Errorf("understand_anything: 'path' is required")
	}
	if u.Registry == nil {
		return nil, fmt.Errorf("understand_anything: registry not wired")
	}
	abs := req.Path
	if !filepath.IsAbs(abs) && u.Cwd != "" {
		abs = filepath.Join(u.Cwd, abs)
	}
	abs = filepath.Clean(abs)

	res, err := u.Registry.Extract(ctx, abs)
	if err != nil {
		// ErrMissingDependency includes install hints — surface
		// the message verbatim so the user sees "install pdftotext"
		// instead of a generic "failed".
		var dep *multimodal.ErrMissingDependency
		if errors.As(err, &dep) {
			return errorJSON(dep.Error()), nil
		}
		return nil, fmt.Errorf("understand_anything: extract %s: %w", abs, err)
	}

	out := understandOutput{
		Text:      res.Text,
		Extractor: res.Extractor,
		Metadata:  res.Metadata,
	}
	return json.Marshal(out)
}
