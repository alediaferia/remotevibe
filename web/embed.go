// Package web holds the phone-facing PWA, embedded into the daemon binary so
// deployment is a single file.
package web

import "embed"

//go:embed index.html app.js style.css manifest.webmanifest
var Files embed.FS
