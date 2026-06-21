package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeFailsFastOnBadEntry(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.sigil")
	if err := os.WriteFile(bad, []byte("pub let x = (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A broken entry fails the up-front build before ListenAndServe, so this
	// returns an error without ever binding a port.
	_, _, err := run("serve", "--root", dir, "--port", "0", bad)
	if err == nil {
		t.Fatal("expected serve to fail fast on a broken entry")
	}
}

func TestServeBuildsOnceAndServesStatic(t *testing.T) {
	// A good entry builds at startup; we exercise the bundle path directly
	// (ListenAndServe blocks, so we assert the production bundle is produced
	// and carries no dev instrumentation).
	js, err := bundle(counterEntry(), repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js, "count") {
		t.Error("counter bundle did not compile")
	}
	if strings.Contains(js, "__sigilDev") {
		t.Error("production serve bundle must not be instrumented")
	}
}
