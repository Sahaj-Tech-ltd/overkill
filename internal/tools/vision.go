// Package tools — vision_describe runs a vision model over a webpage,
// a file on disk, or the current browser viewport. The agent itself is
// text-only; this tool is the seam where pixels become prose.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/browser"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
)

const maxVisionImageBytes = 20 << 20

// VisionDescribeTool wraps a vision.Describer and (optionally) the
// browser manager so the agent can describe web pages without a
// separate "screenshot, then describe" two-step.
type VisionDescribeTool struct {
	Describer vision.Describer
	Mgr       *browser.Manager  // optional; required for url/screenshot modes
	Policy    BrowserHostPolicy // re-uses the same SSRF policy as browser tools
	// RootDir bounds the `file:` mode so `vision_describe` can't read
	// arbitrary paths like /etc/shadow. File mode fails closed without a root.
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

func (t *VisionDescribeTool) Description() string {
	return "Describe an image file within this workspace, a browser URL, or the current browser viewport. File images are limited to 20 MiB. URL and viewport modes require a configured browser and its host policy."
}

func (t *VisionDescribeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","maxLength":16384},"selector":{"type":"string","maxLength":4096},"file":{"type":"string","maxLength":4096},"prompt":{"type":"string","maxLength":8192},"width":{"type":"integer","minimum":0,"maximum":4096},"height":{"type":"integer","minimum":0,"maximum":4096}},"additionalProperties":false}`)
}

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
	if len(in) > 32768 {
		return nil, fmt.Errorf("vision_describe: input exceeds bound")
	}
	if len(in) > 0 {
		dec := json.NewDecoder(strings.NewReader(string(in)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&args); err != nil {
			return nil, fmt.Errorf("vision_describe: %w", err)
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("vision_describe: one input object required")
		}
	}
	if len(args.URL) > 16384 || len(args.Selector) > 4096 || len(args.File) > 4096 || len(args.Prompt) > 8192 || args.Width < 0 || args.Width > 4096 || args.Height < 0 || args.Height > 4096 {
		return nil, fmt.Errorf("vision_describe: input field exceeds bound")
	}
	if args.URL != "" && args.File != "" {
		return nil, fmt.Errorf("vision_describe: choose one image source")
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
		if t.RootDir == "" {
			return errorJSON("vision_describe: file mode requires a workspace root"), nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		root, name, err := openWorkspace(t.RootDir, args.File)
		if err != nil {
			return errorJSON(fmt.Sprintf("vision_describe: workspace path: %v", err)), nil
		}
		defer root.Close()
		file, err := openVisionFile(root, name)
		if err != nil {
			return errorJSON(fmt.Sprintf("vision_describe: read file: %v", err)), nil
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return errorJSON("vision_describe: image must be a regular file"), nil
		}
		if info.Size() > maxVisionImageBytes {
			return errorJSON("vision_describe: image exceeds 20 MiB limit"), nil
		}
		png, err = io.ReadAll(io.LimitReader(file, maxVisionImageBytes+1))
		if err != nil {
			return errorJSON(fmt.Sprintf("vision_describe: read file: %v", err)), nil
		}
		if len(png) > maxVisionImageBytes {
			return errorJSON("vision_describe: image exceeds 20 MiB limit"), nil
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

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mime := vision.MIMEFromBytes(png)
	desc, err := t.Describer.Describe(ctx, []vision.Image{{Bytes: png, Mime: mime}}, args.Prompt)
	if err != nil {
		return errorJSON(fmt.Sprintf("vision_describe: %v", err)), nil
	}
	out, err := json.Marshal(map[string]any{
		"source":      source,
		"mime":        mime,
		"bytes":       len(png),
		"description": strings.TrimSpace(desc),
	})
	if err != nil {
		return nil, fmt.Errorf("vision_describe: marshal: %w", err)
	}
	return out, nil
}

func takeScreenshot(b *browser.Browser, args visionDescribeInput) ([]byte, error) {
	if args.Selector != "" {
		return b.ScreenshotElement(args.Selector)
	}
	return b.Screenshot(args.Width, args.Height)
}
