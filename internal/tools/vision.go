// Package tools — vision_describe runs a vision model over a webpage,
// a file on disk, or the current browser viewport. The agent itself is
// text-only; this tool is the seam where pixels become prose.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/browser"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
)

// VisionDescribeTool wraps a vision.Describer and (optionally) the
// browser manager so the agent can describe web pages without a
// separate "screenshot, then describe" two-step.
type VisionDescribeTool struct {
	Describer vision.Describer
	Mgr       *browser.Manager  // optional; required for url/screenshot modes
	Policy    BrowserHostPolicy // re-uses the same SSRF policy as browser tools
	// RootDir bounds the `file:` mode so `vision_describe` can't read
	// arbitrary paths like /etc/shadow. Empty disables the check
	// (legacy behaviour); production callers should always set it.
	RootDir string
}

// NewVisionDescribeTool wires the dependencies. Mgr may be nil when
// only file-based describes are needed.
func NewVisionDescribeTool(d vision.Describer, mgr *browser.Manager, policy BrowserHostPolicy) *VisionDescribeTool {
	return &VisionDescribeTool{Describer: d, Mgr: mgr, Policy: policy}
}

// WithRootDir constrains the `file:` describe mode to a workspace root.
func (t *VisionDescribeTool) WithRootDir(dir string) *VisionDescribeTool {
	t.RootDir = dir
	return t
}

func (t *VisionDescribeTool) Name() string { return "vision_describe" }

type visionDescribeInput struct {
	URL      string `json:"url"`      // navigate, screenshot, describe
	Selector string `json:"selector"` // element-only screenshot (paired with url or screenshot mode)
	File     string `json:"file"`     // describe an image file from disk (absolute or workspace-relative)
	Prompt   string `json:"prompt"`   // optional steering question
	Width    int    `json:"width"`    // viewport for screenshot
	Height   int    `json:"height"`
}

// Execute resolves exactly one source of pixels then hands it to the
// describer. Sources are mutually exclusive in the docs but we
// degrade gracefully: url > file > current-viewport.
func (t *VisionDescribeTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.Describer == nil {
		return errorJSON("vision_describe: no vision describer configured (set [vision] api_key + model)"), nil
	}
	var args visionDescribeInput
	if len(in) > 0 {
		if err := json.Unmarshal(in, &args); err != nil {
			return nil, fmt.Errorf("vision_describe: %w", err)
		}
	}

	var (
		png    []byte
		source string
		err    error
	)

	switch {
	case args.URL != "":
		if t.Mgr == nil {
			return errorJSON("vision_describe: url mode requires the browser manager"), nil
		}
		if err := t.Policy.CheckURL(args.URL); err != nil {
			return errorJSON(err.Error()), nil
		}
		b, err := t.Mgr.Get(ctx)
		if err != nil {
			return nil, err
		}
		if err := b.Navigate(args.URL); err != nil {
			return nil, err
		}
		png, err = takeScreenshot(b, args)
		if err != nil {
			return nil, err
		}
		source = args.URL
	case args.File != "":
		path := args.File
		if !filepath.IsAbs(path) {
			base := t.RootDir
			if base == "" {
				base, _ = os.Getwd()
			}
			path = filepath.Join(base, path)
		}
		path = filepath.Clean(path)
		// Containment check via filepath.Rel — without this the LLM
		// could `file: "/etc/shadow"` and bypass FSTool's guards
		// entirely. RootDir empty preserves legacy permissive mode for
		// CLI usage without a workspace.
		if t.RootDir != "" {
			root, rerr := filepath.Abs(t.RootDir)
			if rerr == nil {
				rel, relErr := filepath.Rel(filepath.Clean(root), path)
				if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return errorJSON(fmt.Sprintf("vision_describe: path %q is outside workspace", args.File)), nil
				}
			}
		}
		png, err = os.ReadFile(path)
		if err != nil {
			return errorJSON(fmt.Sprintf("vision_describe: read file: %v", err)), nil
		}
		source = "file:" + args.File
	default:
		if t.Mgr == nil {
			return errorJSON("vision_describe: provide url or file (no browser to screenshot)"), nil
		}
		b, err := t.Mgr.Get(ctx)
		if err != nil {
			return nil, err
		}
		png, err = takeScreenshot(b, args)
		if err != nil {
			return nil, err
		}
		cur, _ := b.URL()
		source = "viewport:" + cur
	}

	mime := vision.MIMEFromBytes(png)
	desc, err := t.Describer.Describe(ctx, []vision.Image{{Bytes: png, Mime: mime}}, args.Prompt)
	if err != nil {
		return errorJSON(fmt.Sprintf("vision_describe: %v", err)), nil
	}
	out, _ := json.Marshal(map[string]any{
		"source":      source,
		"mime":        mime,
		"bytes":       len(png),
		"description": strings.TrimSpace(desc),
	})
	return out, nil
}

func takeScreenshot(b *browser.Browser, args visionDescribeInput) ([]byte, error) {
	if args.Selector != "" {
		return b.ScreenshotElement(args.Selector)
	}
	return b.Screenshot(args.Width, args.Height)
}
