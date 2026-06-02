// Package shadows — hidden easter eggs and secret commands.
// DOOM lives here. Accessible only via /doom slash command in the TUI.
package shadows

import (
	"embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
)

//go:embed *.wasm *.js *.conf *.json *.jsdos
var doomFS embed.FS

// DoomPage returns an HTML page that loads js-dos and auto-starts DOOM.
func DoomPage() []byte {
	return []byte(`<!DOCTYPE html>
<html><head><meta charset="UTF-8"><title>DOOM</title>
<style>
*{margin:0;padding:0;background:#000}
body{display:flex;justify-content:center;align-items:center;height:100vh;overflow:hidden}
canvas{image-rendering:pixelated;max-width:100vw;max-height:100vh}
.controls{position:fixed;bottom:10px;left:50%;transform:translateX(-50%);
  color:#888;font:12px monospace;background:rgba(0,0,0,.7);padding:8px 16px;
  border-radius:4px;pointer-events:none;z-index:10}
</style></head><body>
<div class="controls">WASD move · Arrows turn · Space fire · E use · 1-7 weapons · Shift run · ESC exit</div>
<canvas id="dosbox"></canvas>
<script src="/shadows/wdosbox.js"></script>
<script>
Dos(document.getElementById("dosbox"), {
  url: "/shadows/doom.jsdos",
  backend: "wdosbox",
  theme: "dark",
  autoStart: true,
  onExit: function() { window.close(); }
});
</script>
</body></html>`)
}

// ServeDoomAssets registers HTTP handlers for the embedded DOOM assets.
func ServeDoomAssets(mux *http.ServeMux) {
	sub, err := fs.Sub(doomFS, ".")
	if err != nil {
		panic(fmt.Sprintf("shadows: embedded FS missing root: %v", err))
	}
	mux.Handle("/shadows/", http.StripPrefix("/shadows/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("/doom", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write(DoomPage())
	})
}

// LaunchDoom starts a local HTTP server on a random port, opens the browser
// to the DOOM page, and returns the port. Caller is responsible for shutting
// down the server when DOOM exits.
func LaunchDoom(mux *http.ServeMux) (int, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, nil, fmt.Errorf("shadows: cannot bind: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	ServeDoomAssets(mux)

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)

	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/doom"
	if err := openBrowser(url); err != nil {
		return port, func() { srv.Close() }, nil // browser open failed but server is up
	}

	return port, func() { srv.Close() }, nil
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", url).Start()
	default:
		return fmt.Errorf("unsupported platform")
	}
}
