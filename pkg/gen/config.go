package gen

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// ConfigName is the fixed config filename, expected next to
// sigil.mod. There is deliberately exactly one way to invoke
// generation — no flags fallback — so a teammate's checkout, CI, and
// `make gen` cannot drift.
const ConfigName = "sigil.gen.yaml"

// Config is the parsed sigil.gen.yaml.
type Config struct {
	// Version is the config format version. Only 1 exists.
	Version int `yaml:"version"`
	// Generate lists the generation entries, run in order.
	Generate []Entry `yaml:"generate"`
}

// Entry is one generation unit: run one target over one sigil
// package, writing into one output directory.
type Entry struct {
	// Target names a registered generator ("go").
	Target string `yaml:"target"`
	// Package is the sigil package to compile, as a module-relative
	// directory path ("app/chat"; "." for the module root).
	Package string `yaml:"package"`
	// Out is the output directory, module-relative. Generated output
	// is meant to be gitignored and regenerated, never edited.
	Out string `yaml:"out"`
	// Opts carries target-specific options (e.g. go: package name).
	Opts map[string]string `yaml:"opts,omitempty"`
}

const configExample = `a minimal ` + ConfigName + `:

    version: 1
    generate:
      - target: go
        package: app/chat
        out: internal/gen/chat
`

// LoadConfig reads and validates sigil.gen.yaml from the module
// root. Unknown keys, unknown versions, and structurally invalid
// entries are hard errors with precise messages — the config is part
// of the build contract, so it gets compiler-grade strictness.
func LoadConfig(moduleRoot string) (Config, error) {
	path := filepath.Join(moduleRoot, ConfigName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("sigil gen: no %s found next to sigil.mod (%s)\n\ncreate %s", ConfigName, moduleRoot, configExample)
	}
	if err != nil {
		return Config{}, fmt.Errorf("sigil gen: %w", err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("sigil gen: %s: %w", path, err)
	}

	if cfg.Version != 1 {
		return Config{}, fmt.Errorf("sigil gen: %s: unsupported version %d (this sigil supports version 1)", path, cfg.Version)
	}
	if len(cfg.Generate) == 0 {
		return Config{}, fmt.Errorf("sigil gen: %s: `generate` list is empty — %s", path, configExample)
	}

	seen := map[string]string{} // out dir -> first entry's package, to reject collisions
	for i, e := range cfg.Generate {
		at := fmt.Sprintf("%s: generate[%d]", path, i)
		if e.Target == "" {
			return Config{}, fmt.Errorf("sigil gen: %s: missing `target`", at)
		}
		if e.Package == "" {
			return Config{}, fmt.Errorf("sigil gen: %s: missing `package` (module-relative directory of the sigil package)", at)
		}
		if e.Out == "" {
			return Config{}, fmt.Errorf("sigil gen: %s: missing `out` (module-relative output directory)", at)
		}
		for label, p := range map[string]string{"package": e.Package, "out": e.Out} {
			clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
			if filepath.IsAbs(p) || clean == ".." || strings.HasPrefix(clean, "../") {
				return Config{}, fmt.Errorf("sigil gen: %s: `%s` must be a module-relative path inside the module, got %q", at, label, p)
			}
		}
		if prev, dup := seen[e.Out]; dup {
			return Config{}, fmt.Errorf("sigil gen: %s: `out` %q already used by the entry for package %q — entries must not share an output directory", at, e.Out, prev)
		}
		seen[e.Out] = e.Package
	}
	return cfg, nil
}
