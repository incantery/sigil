package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckOK(t *testing.T) {
	out, _, err := run("check", "--root", repoRoot, counterEntry())
	if err != nil {
		t.Fatalf("check ok: %v", err)
	}
	if !strings.HasPrefix(out, "ok ") {
		t.Errorf("got %q, want an \"ok\" line", out)
	}
}

func TestCheckJSONOK(t *testing.T) {
	out, _, err := run("check", "--json", "--root", repoRoot, counterEntry())
	if err != nil {
		t.Fatalf("check --json ok: %v", err)
	}
	if !strings.Contains(out, `"ok":true`) {
		t.Errorf("got %q, want ok:true", out)
	}
}

func TestCheckBroken(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.mako")
	if err := os.WriteFile(bad, []byte("pub let x = (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := run("check", "--json", "--root", dir, bad)
	if err == nil {
		t.Fatalf("expected a nonzero exit for a broken file; out=%q", out)
	}
	if !strings.Contains(out, `"ok":false`) {
		t.Errorf("got %q, want ok:false", out)
	}
}
