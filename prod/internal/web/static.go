package web

import "embed"

// Assets is the SPA bundle compiled into the binary. Listed explicitly so the
// build fails loudly if a file goes missing instead of silently shipping an
// empty page.
//
//go:embed static/index.html static/style.css static/app.js
var Assets embed.FS
