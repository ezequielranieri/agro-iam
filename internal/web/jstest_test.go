package web

import (
	"os/exec"
	"testing"
)

// TestNodeJSTests runs the SPA's pure-logic unit tests (D6: the project has no
// Node toolchain, but the token/escape logic must still be testable, so the
// modules under static/ are written as pure ESM and exercised with `node
// --test`). The test self-skips when node is not installed — the same pattern
// as the RLS integration tests — and runs for real in CI, where the
// ubuntu-latest runner ships node preinstalled, so no workflow change is
// needed. Go's test binary runs with the package dir as its working directory,
// so "jstest/*.test.mjs" resolves under internal/web. Deterministic and fast
// (three suites, well under a second).
func TestNodeJSTests(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed: JS unit tests skipped")
	}

	// The glob pattern is passed straight to node, which expands it itself
	// (Node >= 21) — no shell involved, works on Windows too.
	out, err := exec.Command(node, "--test", "jstest/*.test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("node --test failed: %v\n%s", err, out)
	}
}