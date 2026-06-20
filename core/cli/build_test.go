package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildBundle(t *testing.T) {
	out, _, err := run("build", "--root", repoRoot, counterEntry())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("expected a non-empty JS bundle on stdout")
	}
}

func TestBuildHTML(t *testing.T) {
	out, _, err := run("build", "--html", "--root", repoRoot, counterEntry())
	if err != nil {
		t.Fatalf("build --html: %v", err)
	}
	if !strings.Contains(out, `id="app"`) {
		t.Error("html output is missing the #app mount point")
	}
	if !strings.Contains(out, "<script>") {
		t.Error("html output is missing the embedded <script>")
	}
}

func TestBuildOutFile(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "bundle.js")
	_, _, err := run("build", "--root", repoRoot, "-o", dst, counterEntry())
	if err != nil {
		t.Fatalf("build -o: %v", err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if len(b) == 0 {
		t.Error("output file is empty")
	}
}
