package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServeFailsFastOnBadEntry(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.mako")
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
