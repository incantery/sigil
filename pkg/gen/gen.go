// Package gen is sigil's code-generation runner: it reads the
// committed sigil.gen.yaml next to sigil.mod, compiles each entry's
// sigil package, extracts its contract (pkg/contract), and hands it
// to the named generator target.
//
// Targets are in-tree and compiled into the CLI — the yaml's
// `target:` key selects from a registry, it does not locate external
// plugin binaries. Adding a language is implementing Target and
// calling Register from an init(); there is no exec protocol, no
// discovery, and no public IR serialization to stabilize. If a true
// external-plugin need ever appears, the upgrade path is mechanical
// (serialize the contract across an exec boundary), but that cost is
// deferred until something demands it.
package gen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/incantery/sigil/pkg/contract"
	"github.com/incantery/sigil/pkg/lang/loader"
	"github.com/incantery/sigil/pkg/lang/lower"
	"github.com/incantery/sigil/pkg/lang/sigilmod"
)

// Target is one code generator. Implementations consume the contract
// — never the UI IR — so they stay insulated from the document
// model's churn.
type Target interface {
	// Name is the registry key the config's `target:` field selects.
	Name() string
	// Generate emits the target's files for one config entry. Paths
	// are relative to the entry's `out:` directory.
	Generate(req Request) ([]File, error)
}

// Request is everything a target gets to see for one entry.
type Request struct {
	// Contract is the compiled package's IDL.
	Contract contract.Contract
	// Hash is Contract.Hash(), precomputed so every target stamps
	// the identical digest.
	Hash string
	// Package is the entry's sigil package, as spelled in the config
	// (module-relative directory path).
	Package string
	// Out is the entry's output directory, as spelled in the config.
	// Returned File paths are relative to it; targets may also use
	// it for naming defaults (the go target derives its package name
	// from the last path element).
	Out string
	// Opts carries the entry's target-specific options verbatim.
	Opts map[string]string
}

// File is one generated output file.
type File struct {
	Path    string // relative to the entry's out dir
	Content []byte
}

var registry = map[string]Target{}

// Register adds a target to the registry. Call from an init() in the
// target's package; a duplicate name is a programmer error and
// panics at startup.
func Register(t Target) {
	if _, dup := registry[t.Name()]; dup {
		panic(fmt.Sprintf("gen: duplicate target %q", t.Name()))
	}
	registry[t.Name()] = t
}

// knownTargets returns the registered target names, sorted, for
// diagnostics.
func knownTargets() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Run executes the full generation config. start is any path inside
// the module (usually the working directory); the runner walks up to
// sigil.mod and reads sigil.gen.yaml beside it. logf receives one
// line per written file.
func Run(start string, logf func(format string, args ...any)) error {
	mod, err := sigilmod.Find(start)
	if err != nil {
		return fmt.Errorf("sigil gen: %w", err)
	}
	cfg, err := LoadConfig(mod.Root)
	if err != nil {
		return err
	}
	for _, entry := range cfg.Generate {
		if err := runEntry(mod, entry, logf); err != nil {
			return err
		}
	}
	return nil
}

// ensureInside verifies that target, after resolving symlinks on its
// longest existing ancestor, stays within root. The config's path
// validation is purely lexical (it can't see the filesystem), so the
// real containment check has to happen here — otherwise a symlink
// committed inside an untrusted monorepo (`out: build` where ./build
// → /etc) would let `sigil gen`, the one sanctioned entrypoint, write
// generated files outside the checkout. target need not exist yet.
func ensureInside(root, target, label string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolving module root: %w", err)
	}
	// Walk up to the longest existing ancestor of target (target
	// itself, or a parent dir we'll create), resolve its symlinks,
	// then re-append the not-yet-existing tail.
	probe := filepath.Clean(target)
	var tail []string
	for {
		if resolved, err := filepath.EvalSymlinks(probe); err == nil {
			real := filepath.Join(append([]string{resolved}, tail...)...)
			rel, err := filepath.Rel(realRoot, real)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("%s path %q resolves outside the module (via a symlink) — refusing to write there", label, target)
			}
			return nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			// Reached the filesystem root without finding an existing
			// ancestor; treat as outside.
			return fmt.Errorf("%s path %q has no resolvable ancestor inside the module", label, target)
		}
		tail = append([]string{filepath.Base(probe)}, tail...)
		probe = parent
	}
}

func runEntry(mod *sigilmod.Module, entry Entry, logf func(string, ...any)) error {
	target, ok := registry[entry.Target]
	if !ok {
		return fmt.Errorf("sigil gen: entry %q: unknown target %q (registered targets: %v)",
			entry.Package, entry.Target, knownTargets())
	}

	pkgDir := filepath.Join(mod.Root, filepath.FromSlash(entry.Package))
	if err := ensureInside(mod.Root, pkgDir, "package"); err != nil {
		return fmt.Errorf("sigil gen: entry %q: %w", entry.Package, err)
	}
	if fi, err := os.Stat(pkgDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("sigil gen: entry %q: package directory %s does not exist", entry.Package, pkgDir)
	}

	prog, err := loader.Load(pkgDir)
	if err != nil {
		return fmt.Errorf("sigil gen: compiling %q: %w", entry.Package, err)
	}
	merged, err := prog.Merge()
	if err != nil {
		return fmt.Errorf("sigil gen: compiling %q: %w", entry.Package, err)
	}
	doc, err := lower.Lower(merged)
	if err != nil {
		return fmt.Errorf("sigil gen: compiling %q: %w", entry.Package, err)
	}

	c := contract.FromDoc(doc)
	if c.Empty() {
		return fmt.Errorf("sigil gen: entry %q declares no query/command/stream ops — nothing to generate", entry.Package)
	}

	files, err := target.Generate(Request{
		Contract: c,
		Hash:     c.Hash(),
		Package:  entry.Package,
		Out:      entry.Out,
		Opts:     entry.Opts,
	})
	if err != nil {
		return fmt.Errorf("sigil gen: target %q on %q: %w", entry.Target, entry.Package, err)
	}

	outDir := filepath.Join(mod.Root, filepath.FromSlash(entry.Out))
	if err := ensureInside(mod.Root, outDir, "out"); err != nil {
		return fmt.Errorf("sigil gen: entry %q: %w", entry.Package, err)
	}
	for _, f := range files {
		dst := filepath.Join(outDir, filepath.FromSlash(f.Path))
		if err := ensureInside(mod.Root, dst, "out"); err != nil {
			return fmt.Errorf("sigil gen: entry %q: %w", entry.Package, err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("sigil gen: %w", err)
		}
		if err := os.WriteFile(dst, f.Content, 0o644); err != nil {
			return fmt.Errorf("sigil gen: %w", err)
		}
		if logf != nil {
			rel, relErr := filepath.Rel(mod.Root, dst)
			if relErr != nil {
				rel = dst
			}
			logf("sigil gen: wrote %s (target %s, package %s)", rel, entry.Target, entry.Package)
		}
	}
	return nil
}
