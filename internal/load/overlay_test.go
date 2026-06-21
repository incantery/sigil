package load

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An overlay entry shadows the on-disk content for that path.
func TestOverlayShadowsDisk(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "app.sigil")
	// On disk: a valid module.
	if err := os.WriteFile(entry, []byte("pub let x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Overlay: a BROKEN version. Load must see the overlay and fail.
	_, err := Load(entry, Options{
		Root:    dir,
		Overlay: map[string]string{entry: "pub let x = (\n"},
	})
	if err == nil {
		t.Fatal("expected overlay (broken) to fail the load, but it succeeded")
	}
	if !strings.Contains(err.Error(), entry) {
		t.Errorf("error %q should name the entry file", err)
	}
}

// With no overlay entry for a path, the on-disk content is used.
func TestOverlayFallsBackToDisk(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "app.sigil")
	if err := os.WriteFile(entry, []byte("pub let x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Overlay names a DIFFERENT file, so the entry reads from disk (valid).
	if _, err := Load(entry, Options{
		Root:    dir,
		Overlay: map[string]string{filepath.Join(dir, "other.sigil"): "garbage"},
	}); err != nil {
		t.Fatalf("expected disk fallback to succeed, got %v", err)
	}
}
