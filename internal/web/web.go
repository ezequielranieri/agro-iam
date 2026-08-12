// Package web embeds the demo SPA into the binary via go:embed (D1). The
// static tree lives inside the package directory — go:embed cannot cross
// package boundaries — and StaticFS exposes it to the HTTP router, which
// mounts GET / and GET /static/ (FR1).
package web

import (
	"embed"
	"io/fs"
)

// staticFS is the embedded asset tree: index.html plus the app modules that
// the SPA slices add. Compile-time checked: any missing file breaks the build
// (FR1 scenario "embed package-dir constraint").
//
//go:embed static/*
var staticFS embed.FS

// StaticFS returns the embedded static tree for the router to mount. The tree
// is served read-only; nothing in the process writes into the binary.
func StaticFS() fs.FS {
	return staticFS
}
