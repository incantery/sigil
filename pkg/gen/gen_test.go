package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeTarget records the request and emits one file.
type fakeTarget struct {
	name string
	last *Request
}

func (f *fakeTarget) Name() string { return f.name }
func (f *fakeTarget) Generate(req Request) ([]File, error) {
	f.last = &req
	return []File{{Path: "out.txt", Content: []byte("hash=" + req.Hash)}}, nil
}

func setupModule(t *testing.T, genYAML string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "sigil.mod", "module example.com/proj\n")
	if genYAML != "" {
		writeFile(t, root, ConfigName, genYAML)
	}
	writeFile(t, filepath.Join(root, "app", "chat"), "chat.mako", `stream Chat -> prompt : String = String

view App =
  text "ok"
`)
	return root
}

func TestRunMissingConfigErrorsWithExample(t *testing.T) {
	root := setupModule(t, "")
	err := Run(root, nil)
	if err == nil {
		t.Fatal("expected missing-config error")
	}
	if !strings.Contains(err.Error(), "version: 1") || !strings.Contains(err.Error(), "target: go") {
		t.Errorf("missing-config error must show a minimal example, got:\n%v", err)
	}
}

func TestRunExecutesEntry(t *testing.T) {
	ft := &fakeTarget{name: "faketest"}
	Register(ft)
	t.Cleanup(func() { delete(registry, "faketest") })

	root := setupModule(t, `version: 1
generate:
  - target: faketest
    package: app/chat
    out: internal/gen/chat
    opts:
      package: chatgen
`)
	var lines []string
	logf := func(format string, args ...any) { lines = append(lines, format) }
	if err := Run(root, logf); err != nil {
		t.Fatal(err)
	}
	if ft.last == nil {
		t.Fatal("target never invoked")
	}
	if ft.last.Package != "app/chat" || ft.last.Opts["package"] != "chatgen" {
		t.Errorf("request = %+v", ft.last)
	}
	if len(ft.last.Contract.Streams) != 1 {
		t.Errorf("contract streams = %+v", ft.last.Contract.Streams)
	}
	got, err := os.ReadFile(filepath.Join(root, "internal", "gen", "chat", "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "hash=c1:") {
		t.Errorf("written file = %q", got)
	}
	if len(lines) != 1 {
		t.Errorf("want 1 log line, got %d", len(lines))
	}
}

func TestRunUnknownTarget(t *testing.T) {
	root := setupModule(t, `version: 1
generate:
  - target: cobol
    package: app/chat
    out: gen
`)
	err := Run(root, nil)
	if err == nil || !strings.Contains(err.Error(), `unknown target "cobol"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadConfigRejectsUnknownKeys(t *testing.T) {
	root := setupModule(t, `version: 1
generate:
  - target: go
    package: app/chat
    out: gen
    outt: typo
`)
	_, err := LoadConfig(root)
	if err == nil {
		t.Fatal("expected unknown-key error")
	}
	if !strings.Contains(err.Error(), "outt") {
		t.Errorf("err should name the unknown key: %v", err)
	}
}

func TestLoadConfigRejectsBadVersion(t *testing.T) {
	root := setupModule(t, `version: 2
generate:
  - target: go
    package: app/chat
    out: gen
`)
	_, err := LoadConfig(root)
	if err == nil || !strings.Contains(err.Error(), "unsupported version 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadConfigRejectsEscapingPaths(t *testing.T) {
	root := setupModule(t, `version: 1
generate:
  - target: go
    package: ../outside
    out: gen
`)
	_, err := LoadConfig(root)
	if err == nil || !strings.Contains(err.Error(), "module-relative") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadConfigRejectsDuplicateOut(t *testing.T) {
	root := setupModule(t, `version: 1
generate:
  - target: go
    package: app/chat
    out: gen
  - target: go
    package: app/login
    out: gen
`)
	_, err := LoadConfig(root)
	if err == nil || !strings.Contains(err.Error(), "share an output directory") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunRejectsOplessPackage(t *testing.T) {
	ft := &fakeTarget{name: "faketest2"}
	Register(ft)
	t.Cleanup(func() { delete(registry, "faketest2") })

	root := setupModule(t, `version: 1
generate:
  - target: faketest2
    package: app/empty
    out: gen
`)
	writeFile(t, filepath.Join(root, "app", "empty"), "empty.mako", `view App =
  text "ok"
`)
	err := Run(root, nil)
	if err == nil || !strings.Contains(err.Error(), "no query/command/stream ops") {
		t.Fatalf("err = %v", err)
	}
}

// TestRunRejectsSymlinkedOutEscape locks the symlink containment
// guard: a committed symlink whose target is outside the module must
// not let generation write through it, even though the lexical path
// check passes.
func TestRunRejectsSymlinkedOutEscape(t *testing.T) {
	ft := &fakeTarget{name: "faketest3"}
	Register(ft)
	t.Cleanup(func() { delete(registry, "faketest3") })

	root := setupModule(t, `version: 1
generate:
  - target: faketest3
    package: app/chat
    out: build
`)
	// ./build -> a sibling dir outside the module.
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "build")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	err := Run(root, nil)
	if err == nil || !strings.Contains(err.Error(), "outside the module") {
		t.Fatalf("want symlink-escape rejection, got %v", err)
	}
	// And nothing was written into the escape target.
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("files leaked outside the module: %v", entries)
	}
}
