package sigilmod

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, FileName)
	if err := os.WriteFile(path, []byte("module github.com/seth/pokedex\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Path != "github.com/seth/pokedex" {
		t.Fatalf("Path = %q", m.Path)
	}
	if m.Root != tmp {
		t.Fatalf("Root = %q want %q", m.Root, tmp)
	}
}

func TestParseIgnoresCommentsAndBlankLines(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, FileName)
	src := `// project manifest

module github.com/seth/pokedex

// trailing comment
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(path); err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

func TestParseRejectsUnknownDirective(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, FileName)
	src := "module github.com/seth/pokedex\nrequire something\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Parse(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown directive") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRejectsDuplicateModule(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, FileName)
	src := "module a\nmodule b\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Parse(path)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRequiresModule(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, FileName)
	if err := os.WriteFile(path, []byte("// empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Parse(path)
	if err == nil || !strings.Contains(err.Error(), "missing `module`") {
		t.Fatalf("err = %v", err)
	}
}

func TestFind(t *testing.T) {
	tmp := t.TempDir()
	// tmp/sigil.mod, tmp/foo/bar/file
	if err := os.WriteFile(filepath.Join(tmp, FileName),
		[]byte("module example.com/proj\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(tmp, "foo", "bar")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	leafFile := filepath.Join(deep, "x.mako")
	if err := os.WriteFile(leafFile, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	// From a deep file
	m, err := Find(leafFile)
	if err != nil {
		t.Fatalf("Find from file: %v", err)
	}
	if m.Path != "example.com/proj" {
		t.Fatalf("Path = %q", m.Path)
	}
	// From a deep dir
	m, err = Find(deep)
	if err != nil {
		t.Fatalf("Find from dir: %v", err)
	}
	if m.Path != "example.com/proj" {
		t.Fatalf("Path = %q", m.Path)
	}
}

func TestFindNotFound(t *testing.T) {
	tmp := t.TempDir()
	_, err := Find(tmp)
	if err == nil {
		t.Fatal("expected ErrNotFound")
	}
}

func TestPackagePath(t *testing.T) {
	tmp := t.TempDir()
	m := &Module{Path: "example.com/proj", Root: tmp}
	cases := []struct {
		dir  string
		want string
	}{
		{tmp, "example.com/proj"},
		{filepath.Join(tmp, "views"), "example.com/proj/views"},
		{filepath.Join(tmp, "views", "ui"), "example.com/proj/views/ui"},
	}
	for _, tc := range cases {
		got, err := m.PackagePath(tc.dir)
		if err != nil {
			t.Errorf("PackagePath(%q): %v", tc.dir, err)
			continue
		}
		if got != tc.want {
			t.Errorf("PackagePath(%q) = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

func TestPackageDir(t *testing.T) {
	tmp := t.TempDir()
	m := &Module{Path: "example.com/proj", Root: tmp}
	got, err := m.PackageDir("example.com/proj/views/ui")
	if err != nil {
		t.Fatalf("PackageDir: %v", err)
	}
	want := filepath.Join(tmp, "views", "ui")
	if got != want {
		t.Fatalf("PackageDir = %q, want %q", got, want)
	}
}

func TestPackageDirRejectsExternal(t *testing.T) {
	m := &Module{Path: "example.com/proj", Root: "/tmp/proj"}
	_, err := m.PackageDir("github.com/other/foo")
	if err == nil {
		t.Fatal("expected error for external module")
	}
	if !strings.Contains(err.Error(), "external modules not yet supported") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidatePath(t *testing.T) {
	good := []string{"a", "github.com/seth/pokedex", "example.com/x.y/z"}
	for _, p := range good {
		if err := validatePath(p); err != nil {
			t.Errorf("validatePath(%q) = %v, want nil", p, err)
		}
	}
	bad := []string{"", "/leading", "trailing/", "double//slash", "has space", "has@symbol"}
	for _, p := range bad {
		if err := validatePath(p); err == nil {
			t.Errorf("validatePath(%q) = nil, want error", p)
		}
	}
}
