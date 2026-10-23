package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/chromedp/chromedp"
)

// Snapshot uses a frozen committed state and the same renderer as the SPA.
// It never navigates to a caller-supplied URL or passes application credentials
// to the isolated browser process.
func (s *Server) handleCanvasSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	captureContext, cancelCapture := context.WithCancel(r.Context())
	defer cancelCapture()
	stopShutdown := context.AfterFunc(s.canvasSnapshotContext, cancelCapture)
	defer stopShutdown()
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid query encoding", 400)
		return
	}
	for key, values := range query {
		if len(values) != 1 || (key != "sessionId" && key != "surfaceId" && key != "width" && key != "height") {
			http.Error(w, "unknown or duplicate snapshot query", 400)
			return
		}
	}
	sid, surfaceID := query.Get("sessionId"), query.Get("surfaceId")
	if !canvasIdentifier(sid) || !canvasIdentifier(surfaceID) {
		http.Error(w, "valid sessionId and surfaceId required", 400)
		return
	}
	dimension := func(key string, fallback, min, max int) (int, error) {
		value, ok := query[key]
		if !ok {
			return fallback, nil
		}
		raw := value[0]
		if raw == "" {
			return 0, fmt.Errorf("invalid %s", key)
		}
		for _, c := range raw {
			if c < '0' || c > '9' {
				return 0, fmt.Errorf("invalid %s", key)
			}
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < min || n > max {
			return 0, fmt.Errorf("%s outside supported range", key)
		}
		return n, nil
	}
	width, err := dimension("width", 960, 240, 1920)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	height, err := dimension("height", 640, 160, 1440)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if s.cfg.Canvas == nil {
		http.Error(w, "canvas storage unavailable", 503)
		return
	}
	state, err := s.cfg.Canvas.Get(captureContext, sid)
	if err != nil {
		canvasStoreError(w, err)
		return
	}
	found := false
	for _, surface := range state.Surfaces {
		if surface.SurfaceID == surfaceID {
			found = true
			if !surface.Rendering {
				http.Error(w, "surface has not begun rendering", 409)
				return
			}
			break
		}
	}
	if !found {
		http.Error(w, "surface not found", 404)
		return
	}
	select {
	case s.canvasSnapshotSlots <- struct{}{}:
		defer func() { <-s.canvasSnapshotSlots }()
	default:
		http.Error(w, "snapshot capture busy", 429)
		return
	}
	png, err := captureCanvasSnapshot(captureContext, s.cfg.CanvasChromePath, state, surfaceID, width, height)
	if err != nil {
		log.Printf("web: canvas capture: %v", err)
		http.Error(w, "canvas snapshot browser unavailable or capture failed; configure browser.chrome_path for an installed Chromium browser", 503)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", `attachment; filename="canvas.png"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Canvas-Revision", strconv.FormatInt(state.Revision, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(png)
}

func captureCanvasSnapshot(parent context.Context, chrome string, state CanvasState, surfaceID string, width, height int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	if chrome != "" {
		opts = append(opts, chromedp.ExecPath(chrome))
	}
	allocator, closeAllocator := chromedp.NewExecAllocator(ctx, opts...)
	defer closeAllocator()
	browser, closeBrowser := chromedp.NewContext(allocator)
	defer closeBrowser()
	js, err := Assets.ReadFile("static/canvas.js")
	if err != nil {
		return nil, err
	}
	css, err := Assets.ReadFile("static/canvas.css")
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	target, _ := json.Marshal(surfaceID)
	// User data is base64 text, never interpreted as markup or program source.
	bootstrap := `const state=JSON.parse(new TextDecoder().decode(Uint8Array.from(atob("` + base64.StdEncoding.EncodeToString(raw) + `"),c=>c.charCodeAt(0))));try{OverkillCanvas.render(document.getElementById('canvas-root'),state,` + string(target) + `);document.documentElement.dataset.canvasReady='true'}catch(error){document.documentElement.dataset.canvasFailed='true'}`
	document := `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none';script-src 'unsafe-inline';style-src 'unsafe-inline';base-uri 'none';form-action 'none'"><style>` + string(css) + fmt.Sprintf(`html,body{margin:0;background:#181b20}#canvas-root{box-sizing:border-box;width:%dpx;height:%dpx;padding:16px;overflow:hidden}`, width, height) + `</style></head><body><main id="canvas-root"></main><script>` + string(js) + `</script><script>` + bootstrap + `</script></body></html>`
	var png []byte
	err = chromedp.Run(browser,
		chromedp.EmulateViewport(int64(width), int64(height)),
		chromedp.Navigate("data:text/html;base64,"+base64.StdEncoding.EncodeToString([]byte(document))),
		chromedp.WaitReady("#canvas-root", chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var ready bool
			if err := chromedp.Evaluate(`document.documentElement.dataset.canvasReady==='true'`, &ready).Do(ctx); err != nil {
				return err
			}
			if !ready {
				return fmt.Errorf("shared canvas renderer rejected committed state")
			}
			return nil
		}),
		chromedp.Screenshot("#canvas-root", &png, chromedp.ByQuery),
	)
	return png, err
}
