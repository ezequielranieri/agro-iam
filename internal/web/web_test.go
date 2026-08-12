package web

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

// TestStaticFSServesIndexAndAsset proves the go:embed surface (FR1): the
// binary must contain the demo index and at least one script asset, so the
// router can serve the SPA from the same process with no external files.
func TestStaticFSServesIndexAndAsset(t *testing.T) {
	fsys := StaticFS()

	// The embed tree must expose the demo entrypoint.
	index, err := fs.ReadFile(fsys, "static/index.html")
	if err != nil {
		t.Fatalf("static/index.html in embed FS: %v", err)
	}
	if len(index) == 0 {
		t.Fatal("static/index.html is empty")
	}

	// At least one script asset must exist so the router's /static/ mount
	// serves something real, not an empty directory.
	appJS, err := fs.ReadFile(fsys, "static/app.js")
	if err != nil {
		t.Fatalf("static/app.js in embed FS: %v", err)
	}
	if len(appJS) == 0 {
		t.Fatal("static/app.js is empty")
	}
}

// TestStaticFSSubTree proves StaticFS returns a file system that supports
// fs.Sub — the router uses fs.Sub to mount only the static subtree under
// /static/ (D1). fstest.TestFS walks the tree and validates names, sizes and
// consistency.
func TestStaticFSSubTree(t *testing.T) {
	sub, err := fs.Sub(StaticFS(), "static")
	if err != nil {
		t.Fatalf("fs.Sub(static): %v", err)
	}
	if err := fstest.TestFS(sub, "index.html", "app.js"); err != nil {
		t.Fatalf("static sub FS invalid: %v", err)
	}
}
