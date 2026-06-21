package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevFailsFastOnBadEntry(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.sigil")
	if err := os.WriteFile(bad, []byte("pub let x = (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A broken entry fails the up-front dev build before binding a port.
	_, _, err := run("dev", "--root", dir, "--port", "0", bad)
	if err == nil {
		t.Fatal("expected dev to fail fast on a broken entry")
	}
}

func TestBundleDevIsInstrumented(t *testing.T) {
	js, err := bundleDev(counterEntry(), repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js, "__sigilDev.counter++") {
		t.Error("dev bundle missing instrumented __cell")
	}
}
