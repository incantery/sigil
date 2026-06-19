// Package sigilmod parses sigil.mod, the per-project manifest that
// declares the module path. The format is intentionally Go-style and
// minimal at v0:
//
//	module github.com/seth/pokedex
//
// One directive (`module`) declaring the URL-shaped module path used
// as the prefix of every import in the project. Future directives
// (`require`, `replace`, `go`-equivalent compiler version) land here
// when the features that need them ship.
//
// The URL shape (`github.com/...`) is future-proofing for `sigil get`:
// once remote fetching exists, the module path is also the address
// where dependents fetch the package from. Today only local resolution
// works — paths outside the current module's prefix are a hard error.
package sigilmod

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the canonical manifest name. Lives at module root.
const FileName = "sigil.mod"

// Module is one parsed sigil.mod manifest.
type Module struct {
	// Path is the module's import-path prefix, e.g.
	// "github.com/seth/pokedex". Every import in the project must
	// begin with this prefix in v1 (external imports are a hard
	// error until `sigil get` lands).
	Path string

	// Root is the absolute filesystem path of the directory
	// containing the sigil.mod file. Package import paths resolve
	// relative to this root.
	Root string
}

// Find walks upward from start, looking for sigil.mod. Returns the
// parsed Module on success. start may be a file or a directory; if a
// file, its directory is searched first. Walks until the filesystem
// root, returning ErrNotFound if no manifest is reachable.
func Find(start string) (*Module, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	dir := abs
	if !info.IsDir() {
		dir = filepath.Dir(abs)
	}
	for {
		candidate := filepath.Join(dir, FileName)
		if _, err := os.Stat(candidate); err == nil {
			return Parse(candidate)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, fmt.Errorf("%w: looked upward from %s", ErrNotFound, abs)
		}
		dir = parent
	}
}

// Parse reads and parses a sigil.mod file from path. The returned
// Module's Root is the directory containing path.
func Parse(path string) (*Module, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	mod := &Module{Root: filepath.Dir(path)}
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		// Comments + blank lines are ignored. Comments use `//` like
		// Sigil source — keeps one style across the project.
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("%s:%d: malformed directive %q", path, lineNo, line)
		}
		switch fields[0] {
		case "module":
			if mod.Path != "" {
				return nil, fmt.Errorf("%s:%d: duplicate `module` directive", path, lineNo)
			}
			if len(fields) != 2 {
				return nil, fmt.Errorf("%s:%d: `module` takes exactly one path", path, lineNo)
			}
			if err := validatePath(fields[1]); err != nil {
				return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
			}
			mod.Path = fields[1]
		default:
			return nil, fmt.Errorf("%s:%d: unknown directive %q (v1 supports only `module`)",
				path, lineNo, fields[0])
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if mod.Path == "" {
		return nil, fmt.Errorf("%s: missing `module` directive", path)
	}
	return mod, nil
}

// PackagePath returns the import path of the package living at the
// given absolute directory, relative to the module's root. Returns
// the module path itself when dir == Root. Errors if dir is not
// under Root.
func (m *Module) PackagePath(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(m.Root, abs)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("directory %s is outside module %s", abs, m.Root)
	}
	if rel == "." {
		return m.Path, nil
	}
	// Always use forward slashes in import paths, regardless of the
	// host filesystem separator. Matches Go's behavior.
	return m.Path + "/" + filepath.ToSlash(rel), nil
}

// PackageDir is the inverse of PackagePath: given an import path,
// return the absolute filesystem directory it lives in. Errors if
// the path doesn't begin with the module path (external modules
// aren't supported in v1).
func (m *Module) PackageDir(importPath string) (string, error) {
	if importPath == m.Path {
		return m.Root, nil
	}
	prefix := m.Path + "/"
	if !strings.HasPrefix(importPath, prefix) {
		return "", fmt.Errorf("import %q is outside module %q: external modules not yet supported (only %s/* paths work in v1)",
			importPath, m.Path, m.Path)
	}
	rel := strings.TrimPrefix(importPath, prefix)
	return filepath.Join(m.Root, filepath.FromSlash(rel)), nil
}

// ErrNotFound is returned by Find when no sigil.mod exists between
// the starting directory and the filesystem root.
var ErrNotFound = errors.New("no sigil.mod found")

// validatePath enforces the dotted-and-slashed URL shape we want for
// module identifiers. Allowed: letters, digits, `-`, `_`, `.`, `/`.
// Disallowed: leading/trailing slash, consecutive slashes, empty
// segments. (Tightened over time as we learn what real paths need.)
func validatePath(p string) error {
	if p == "" {
		return errors.New("module path cannot be empty")
	}
	if strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") {
		return fmt.Errorf("module path %q cannot start or end with `/`", p)
	}
	if strings.Contains(p, "//") {
		return fmt.Errorf("module path %q contains empty segment", p)
	}
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == '/':
			// allowed
		default:
			return fmt.Errorf("module path %q contains invalid character %q", p, r)
		}
	}
	return nil
}
