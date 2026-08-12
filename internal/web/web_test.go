package web

import (
	"io/fs"
	"strings"
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

// TestStaticFSDashboardAssets proves the FD1 dashboard wiring is embedded:
// the chart math module and the dashboard view must ship in the binary, and
// app.js must wire the dashboard route to renderDashboard — otherwise the
// #/dashboard route keeps showing the placeholder card.
func TestStaticFSDashboardAssets(t *testing.T) {
	fsys := StaticFS()
	for _, name := range []string{"static/charts.js", "static/views/dashboard.js"} {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			t.Fatalf("%s in embed FS: %v", name, err)
		}
		if len(b) == 0 {
			t.Fatalf("%s is empty", name)
		}
	}

	appJS, err := fs.ReadFile(fsys, "static/app.js")
	if err != nil {
		t.Fatalf("static/app.js in embed FS: %v", err)
	}
	for _, want := range []string{"views/dashboard.js", "renderDashboard("} {
		if !strings.Contains(string(appJS), want) {
			t.Errorf("app.js must wire the dashboard view (FD1): missing %q", want)
		}
	}
}

// TestNoAccessTokenPersistence enforces the FR5 storage contract statically:
// the access token must live in memory only, so no asset may read or write
// browser persistence, and the token modules must stay storage-agnostic
// (storage is injected through the createTokenStore adapter, which is what
// lets node --test exercise them). The single permitted localStorage touch is
// the window.localStorage adapter passed at app.js boot.
func TestNoAccessTokenPersistence(t *testing.T) {
	assets := []string{
		"static/app.js",
		"static/api.js",
		"static/dom.js",
		"static/tokens.js",
		"static/charts.js",
		"static/views/login.js",
		"static/views/dashboard.js",
	}
	readAsset := func(t *testing.T, name string) string {
		t.Helper()
		src, err := fs.ReadFile(StaticFS(), name)
		if err != nil {
			t.Fatalf("%s in embed FS: %v", name, err)
		}
		return string(src)
	}

	// Module-level prohibition: the token logic must not reference any
	// browser persistence or DOM surface at all. The dashboard modules are
	// pure math and a storage-agnostic view, so they belong in this list too.
	for _, name := range []string{"static/tokens.js", "static/api.js", "static/dom.js", "static/charts.js"} {
		src := readAsset(t, name)
		for _, banned := range []string{"localStorage", "sessionStorage", "document.cookie", "cookieStore"} {
			if strings.Contains(src, banned) {
				t.Errorf("%s must not touch %s (FR5: access token stays in memory)", name, banned)
			}
		}
	}

	// Whole-tree prohibition: cookies and sessionStorage never appear anywhere;
	// localStorage appears ONLY in app.js and only as the boot adapter
	// (window.localStorage), never with a read/write call against a key.
	for _, name := range assets {
		src := readAsset(t, name)
		for _, banned := range []string{"sessionStorage", "document.cookie", "cookieStore", "localStorage.setItem", "localStorage.getItem", "localStorage.removeItem"} {
			if strings.Contains(src, banned) {
				t.Errorf("%s must not use %s (FR5)", name, banned)
			}
		}
	}

	appJS := readAsset(t, "static/app.js")
	adapterLine := false
	for _, line := range strings.Split(appJS, "\n") {
		if strings.Contains(line, "localStorage") {
			if strings.Contains(line, "window.localStorage") {
				adapterLine = true
				continue
			}
			t.Errorf("app.js localStorage use must be only the injected adapter: %q", line)
		}
	}
	if !adapterLine {
		t.Error("app.js must pass window.localStorage as the storage adapter at boot")
	}
}
