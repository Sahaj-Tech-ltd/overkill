// Package multimodal — image extractor via vision describer.
//
// Hands the image bytes to the wired vision describer (Anthropic
// today, more later) and returns the caption as text. When no
// describer is wired (config didn't set it up), we still return a
// useful Result — bytes + dimensions + MIME — so the agent has a
// verbal handle on the image even without vision.
package multimodal

import (
	"context"
	"fmt"
	"image"
	// PNG + JPEG + GIF decoders, registered via init() side-effects.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
)

// ImageExtractor wraps a vision.Describer. When Describer is nil the
// extractor still runs — it just returns dimensions + MIME without a
// caption.
type ImageExtractor struct {
	Describer vision.Describer
	// Prompt steers the description. Empty = describer's default
	// ("describe this image in 1-2 sentences").
	Prompt string
	// Timeout caps the describer call. Default 30s.
	Timeout time.Duration
}

// NewImageExtractor returns an extractor that wraps the given
// describer. Pass nil to register the extractor without vision —
// useful for builds without an Anthropic key configured.
func NewImageExtractor(d vision.Describer) *ImageExtractor {
	return &ImageExtractor{Describer: d, Timeout: 30 * time.Second}
}

func (e *ImageExtractor) Name() string { return "image" }

func (e *ImageExtractor) Supports(mime, ext string) bool {
	if strings.HasPrefix(mime, "image/") {
		return true
	}
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tiff":
		return true
	}
	return false
}

func (e *ImageExtractor) Extract(ctx context.Context, path string) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("image: read %s: %w", path, err)
	}
	mime := vision.MIMEFromBytes(data)

	meta := map[string]string{
		"bytes": strconv.Itoa(len(data)),
		"mime":  mime,
	}
	// Best-effort dimension probe — uses the stdlib image decoders.
	// WEBP/BMP/TIFF aren't decodable here without extra deps; we
	// just skip the dimension metadata for those.
	if f, err := os.Open(path); err == nil {
		cfg, _, derr := image.DecodeConfig(f)
		f.Close()
		if derr == nil {
			meta["width"] = strconv.Itoa(cfg.Width)
			meta["height"] = strconv.Itoa(cfg.Height)
		}
	}

	if e.Describer == nil {
		return Result{
			Text: fmt.Sprintf("[image: %s, %s, no vision describer wired — install an Anthropic key to enable image captioning]",
				mime, humanBytesSize(len(data))),
			Metadata:  meta,
			Extractor: "image-noop",
		}, nil
	}

	to := e.Timeout
	if to <= 0 {
		to = 30 * time.Second
	}
	dctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	caption, err := e.Describer.Describe(dctx,
		[]vision.Image{{Bytes: data, Mime: mime}},
		e.Prompt,
	)
	if err != nil {
		// Don't fail extraction — surface a degraded result that
		// includes the dimensions + MIME so the agent at least
		// knows a file landed.
		return Result{
			Text:      fmt.Sprintf("[image: %s — vision describe failed: %s]", mime, err.Error()),
			Metadata:  meta,
			Extractor: "image-degraded",
		}, nil
	}

	return Result{
		Text:      strings.TrimSpace(caption),
		Metadata:  meta,
		Extractor: e.Name(),
	}, nil
}

// humanBytesSize formats a byte count as "12.3KB" / "4.5MB" so the
// degraded message is readable in chat.
func humanBytesSize(n int) string {
	const (
		kb = 1024
		mb = kb * 1024
	)
	switch {
	case n >= mb:
		return fmt.Sprintf("%.1fMB", float64(n)/float64(mb))
	case n >= kb:
		return fmt.Sprintf("%.1fKB", float64(n)/float64(kb))
	default:
		return fmt.Sprintf("%dB", n)
	}
}
