# Sigil CLI Skeleton — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an installable `sigil` CLI binary (cobra-based) with `serve`, `build`, `check`, and `version` subcommands over the new `core/` kernel, and repoint the Makefile to install `sigil` instead of `sigil`.

**Architecture:** Mirror the existing `cmd/sigil` (thin binary) → `internal/cli` (testable command surface) split, rebound onto `core/`. A new `core/cli` package holds the cobra command tree; `core/cmd/sigil/main.go` is a thin wrapper. Each subcommand is built by a constructor function (`newXCmd()`) closing over local flag vars, so every invocation and every test gets fresh flag state. Commands are thin wrappers over `core/load`: `load.Load(entry, Options{Root})` for type-checking and `prog.Bundle()` for emitting JS.

**Tech Stack:** Go, cobra v1.10.2 (already in `go.mod`), `core/load`, `core/emit` (via Bundle).

## Global Constraints

- Module path: `github.com/incantery/sigil`. New packages: `core/cli` and `core/cmd/sigil`.
- No new dependencies. cobra (`github.com/spf13/cobra` v1.10.2) is already required.
- Do NOT use viper/config or bubbletea in this phase.
- `go build ./...` and `go test ./core/...` must stay green after every task.
- All file-taking commands take exactly one `.sigil` entry path (`cobra.ExactArgs(1)`); the `--root` flag defaults to `"."`.
- `Version` is set via `-ldflags "-X github.com/incantery/sigil/core/cli.Version=…"`.
- Commands write user output via `cmd.OutOrStdout()` / `cmd.ErrOrStderr()` (never bare `fmt.Println`/`os.Stdout`) so tests can capture it.
- `check`'s JSON shapes: success `{"ok":true,"file":"<path>"}`, failure `{"ok":false,"file":"<path>","error":"<msg>"}`. On any check failure return `ErrSilent`.
- Leave sigil-era Makefile targets (studio, docs, chat, gauntlet, tree-sitter, nvim, vscode, gen) working against the old kernel; only repoint the core toolchain targets (build/install/run) to sigil.

---

### Task 1: Package scaffold — root, version, binary wrapper

Establishes the `core/cli` package (cobra root, `Execute`, `ErrSilent`, `Version`), the `version` subcommand, the shared test helper, and the `core/cmd/sigil` binary. Deliverable: `sigil version` and bare `sigil` (help) work.

**Files:**
- Create: `core/cli/root.go`
- Create: `core/cli/version.go`
- Create: `core/cli/cli_test.go` (shared test helpers)
- Create: `core/cli/version_test.go`
- Create: `core/cmd/sigil/main.go`

**Interfaces:**
- Produces: `cli.Execute() error`; `cli.ErrSilent error`; `cli.Version string`; unexported `newRootCmd() *cobra.Command`; unexported test helper `run(args ...string) (stdout, stderr string, err error)`; unexported test consts `repoRoot` and helper `counterEntry() string`.

- [ ] **Step 1: Write the failing test**

Create `core/cli/cli_test.go`:

```go
package cli

import (
	"bytes"
	"path/filepath"

	"github.com/spf13/cobra"
)

// repoRoot is the module root that holds std/ (two levels up from core/cli).
const repoRoot = "../.."

// counterEntry is the path to the committed counter example.
func counterEntry() string {
	return filepath.Join(repoRoot, "core", "examples", "counter", "counter.sigil")
}

// run executes the sigil command tree with args, capturing stdout and stderr.
// A fresh command tree per call keeps flag state isolated between tests.
func run(args ...string) (string, string, error) {
	root := newRootCmd()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errb.String(), err
}

var _ = cobra.Command{} // ensure cobra import is used even before subcommands land
```

Create `core/cli/version_test.go`:

```go
package cli

import (
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	out, _, err := run("version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.HasPrefix(out, "sigil ") {
		t.Errorf("version output = %q, want prefix %q", out, "sigil ")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/cli/ -run TestVersion -v`
Expected: FAIL to compile — `undefined: newRootCmd`.

- [ ] **Step 3: Write minimal implementation**

Create `core/cli/root.go`:

```go
// Package cli implements the sigil command-line interface: a cobra command tree
// over the core kernel (core/load + core/emit). Subcommands live in sibling
// files; core/cmd/sigil is a thin binary wrapper around Execute.
package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

// Version is overridden at build time via
// -ldflags "-X github.com/incantery/sigil/core/cli.Version=…".
var Version = "0.0.1-dev"

// ErrSilent signals that a subcommand has already printed its own error output
// (e.g. `sigil check --json`). Returning it yields a nonzero exit without main's
// default stderr message.
var ErrSilent = errors.New("silent")

// newRootCmd builds the sigil command tree. Using a constructor instead of a
// package-global command gives every invocation — and every test — fresh flag
// state.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "sigil",
		Short:         "The sigil frontend-web language toolchain",
		Long:          "sigil compiles a typed reactive UI language to a single npm-free JS bundle.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	return root
}

// Execute runs the sigil command tree. The binary wrapper surfaces the error.
func Execute() error {
	return newRootCmd().Execute()
}
```

Create `core/cli/version.go`:

```go
package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the sigil version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "sigil %s (%s/%s, go %s)\n",
				Version, runtime.GOOS, runtime.GOARCH, runtime.Version())
		},
	}
}
```

Then delete the now-unnecessary `var _ = cobra.Command{}` line from `core/cli/cli_test.go` (the `cobra` import is used by `run`'s `*cobra.Command`? No — `run` uses `newRootCmd()`, not cobra directly. Keep the blank-identifier line, OR drop the `cobra` import and the blank line together.) Simplest: edit `cli_test.go` to remove both the `"github.com/spf13/cobra"` import and the `var _ = cobra.Command{}` line.

Resulting `core/cli/cli_test.go` import block + body becomes:

```go
package cli

import (
	"bytes"
	"path/filepath"
)

const repoRoot = "../.."

func counterEntry() string {
	return filepath.Join(repoRoot, "core", "examples", "counter", "counter.sigil")
}

func run(args ...string) (string, string, error) {
	root := newRootCmd()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errb.String(), err
}
```

Create `core/cmd/sigil/main.go`:

```go
// Command sigil is the sigil toolchain CLI. The command surface lives in
// core/cli so it can be tested independently of this binary wrapper.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/incantery/sigil/core/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		// Subcommands that already printed their own diagnostic (e.g.
		// `sigil check --json`) return cli.ErrSilent — exit nonzero without a
		// duplicate stderr message.
		if !errors.Is(err, cli.ErrSilent) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/cli/ -run TestVersion -v && go build ./...`
Expected: PASS; build succeeds (produces no output).

- [ ] **Step 5: Commit**

```bash
git add core/cli/root.go core/cli/version.go core/cli/cli_test.go core/cli/version_test.go core/cmd/sigil/main.go
git commit -m "feat(cli): scaffold sigil CLI package with version command"
```

---

### Task 2: `check` command

Adds `sigil check ENTRY.sigil [--root DIR] [--json]` — type-check only, no bundling.

**Files:**
- Create: `core/cli/check.go`
- Modify: `core/cli/root.go` (register the command)
- Create: `core/cli/check_test.go`

**Interfaces:**
- Consumes: `newRootCmd()`, `ErrSilent`, test helpers `run`, `repoRoot`, `counterEntry` (Task 1).
- Produces: unexported `newCheckCmd() *cobra.Command`.

- [ ] **Step 1: Write the failing test**

Create `core/cli/check_test.go`:

```go
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
	bad := filepath.Join(dir, "bad.sigil")
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/cli/ -run TestCheck -v`
Expected: FAIL to compile — `undefined: newCheckCmd` is not referenced yet, so actually it fails at runtime with "unknown command \"check\"". Either way: FAIL.

- [ ] **Step 3: Write minimal implementation**

Create `core/cli/check.go`:

```go
package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/incantery/sigil/core/load"
)

func newCheckCmd() *cobra.Command {
	var (
		root   string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "check ENTRY.sigil",
		Short: "Type-check a sigil module without bundling or running it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry := args[0]
			if _, err := load.Load(entry, load.Options{Root: root}); err != nil {
				return reportCheckError(cmd, entry, asJSON, err)
			}
			return reportCheckOK(cmd, entry, asJSON)
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "module root directory (where std/ lives)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit result as JSON to stdout")
	return cmd
}

func reportCheckOK(cmd *cobra.Command, entry string, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
			"ok":   true,
			"file": entry,
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "ok  %s\n", entry)
	return nil
}

func reportCheckError(cmd *cobra.Command, entry string, asJSON bool, err error) error {
	if asJSON {
		_ = json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
			"ok":    false,
			"file":  entry,
			"error": err.Error(),
		})
		return ErrSilent
	}
	fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
	return ErrSilent
}
```

Modify `core/cli/root.go` — register the command. Change:

```go
	root.AddCommand(newVersionCmd())
```

to:

```go
	root.AddCommand(newVersionCmd())
	root.AddCommand(newCheckCmd())
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/cli/ -run TestCheck -v && go build ./...`
Expected: PASS; build succeeds.

- [ ] **Step 5: Commit**

```bash
git add core/cli/check.go core/cli/check_test.go core/cli/root.go
git commit -m "feat(cli): add sigil check (type-check only)"
```

---

### Task 3: shared compile helper + `build` command

Adds `sigil build ENTRY.sigil [--root DIR] [-o FILE] [--html]` and the shared `bundle`/`htmlPage`/`shell` helpers used by both `build` and (next) `serve`.

**Files:**
- Create: `core/cli/compile.go`
- Create: `core/cli/build.go`
- Modify: `core/cli/root.go` (register the command)
- Create: `core/cli/build_test.go`

**Interfaces:**
- Consumes: `newRootCmd()`, test helpers (Task 1).
- Produces: unexported `bundle(entry, root string) (string, error)`; `htmlPage(title, js string) string`; `const shell`; `newBuildCmd() *cobra.Command`.

- [ ] **Step 1: Write the failing test**

Create `core/cli/build_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/cli/ -run TestBuild -v`
Expected: FAIL — "unknown command \"build\"".

- [ ] **Step 3: Write minimal implementation**

Create `core/cli/compile.go`:

```go
package cli

import (
	"fmt"

	"github.com/incantery/sigil/core/load"
)

// shell is the minimal HTML page that hosts a sigil bundle.
const shell = `<!doctype html>
<html>
  <head><meta charset="utf-8"><title>%s</title></head>
  <body>
    <div id="app"></div>
    <script>%s</script>
  </body>
</html>`

// htmlPage wraps a JS bundle in the host page shell.
func htmlPage(title, js string) string {
	return fmt.Sprintf(shell, title, js)
}

// bundle type-checks the entry module against the standard library under root
// and returns the linked, npm-free JS bundle.
func bundle(entry, root string) (string, error) {
	prog, err := load.Load(entry, load.Options{Root: root})
	if err != nil {
		return "", err
	}
	return prog.Bundle()
}
```

Create `core/cli/build.go`:

```go
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newBuildCmd() *cobra.Command {
	var (
		root   string
		out    string
		asHTML bool
	)
	cmd := &cobra.Command{
		Use:   "build ENTRY.sigil",
		Short: "Compile a sigil module to a JS bundle (or a full HTML page)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry := args[0]
			js, err := bundle(entry, root)
			if err != nil {
				return err
			}
			output := js
			if asHTML {
				output = htmlPage(entry, js)
			}
			if out == "" {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), output)
				return err
			}
			return os.WriteFile(out, []byte(output), 0o644)
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "module root directory (where std/ lives)")
	cmd.Flags().StringVarP(&out, "out", "o", "", "write output to FILE instead of stdout")
	cmd.Flags().BoolVar(&asHTML, "html", false, "wrap the bundle in a full HTML page")
	return cmd
}
```

Modify `core/cli/root.go` — register the command. Change:

```go
	root.AddCommand(newVersionCmd())
	root.AddCommand(newCheckCmd())
```

to:

```go
	root.AddCommand(newVersionCmd())
	root.AddCommand(newCheckCmd())
	root.AddCommand(newBuildCmd())
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/cli/ -run TestBuild -v && go build ./...`
Expected: PASS; build succeeds.

- [ ] **Step 5: Commit**

```bash
git add core/cli/compile.go core/cli/build.go core/cli/build_test.go core/cli/root.go
git commit -m "feat(cli): add sigil build with shared bundle/html helpers"
```

---

### Task 4: `serve` command

Adds `sigil serve ENTRY.sigil [--root DIR] [--port N]` — a rebuild-per-request dev server (the port of `core/cmd/serve`), reusing the Task 3 helpers.

**Files:**
- Create: `core/cli/serve.go`
- Modify: `core/cli/root.go` (register the command)
- Create: `core/cli/serve_test.go`

**Interfaces:**
- Consumes: `bundle`, `htmlPage` (Task 3); `newRootCmd()`, test helper `run` (Task 1).
- Produces: unexported `newServeCmd() *cobra.Command`.

- [ ] **Step 1: Write the failing test**

Create `core/cli/serve_test.go`. The test exercises only the fail-fast path (a broken entry errors out before any port is bound), so it never starts a real server:

```go
package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServeFailsFastOnBadEntry(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.sigil")
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/cli/ -run TestServe -v`
Expected: FAIL — "unknown command \"serve\"".

- [ ] **Step 3: Write minimal implementation**

Create `core/cli/serve.go`:

```go
package cli

import (
	"fmt"
	"log"
	"net/http"

	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	var (
		root string
		port string
	)
	cmd := &cobra.Command{
		Use:   "serve ENTRY.sigil",
		Short: "Serve a sigil module as a live-rebuilding dev page",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry := args[0]
			// Build once up front to fail fast on errors (before binding a port).
			if _, err := bundle(entry, root); err != nil {
				return err
			}
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
				js, err := bundle(entry, root)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprintf(w, "build error: %v", err)
					return
				}
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, htmlPage(entry, js))
			})
			addr := ":" + port
			log.Printf("serving %s on http://localhost%s", entry, addr)
			return http.ListenAndServe(addr, mux)
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "module root directory (where std/ lives)")
	cmd.Flags().StringVar(&port, "port", "8099", "port to serve on")
	return cmd
}
```

(Note: a fresh `http.NewServeMux()` per invocation — not `http.HandleFunc` on the default mux — keeps repeated calls panic-free.)

Modify `core/cli/root.go` — register the command. Change:

```go
	root.AddCommand(newVersionCmd())
	root.AddCommand(newCheckCmd())
	root.AddCommand(newBuildCmd())
```

to:

```go
	root.AddCommand(newVersionCmd())
	root.AddCommand(newCheckCmd())
	root.AddCommand(newBuildCmd())
	root.AddCommand(newServeCmd())
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/cli/ -v && go build ./...`
Expected: PASS (all `core/cli` tests); build succeeds.

- [ ] **Step 5: Commit**

```bash
git add core/cli/serve.go core/cli/serve_test.go core/cli/root.go
git commit -m "feat(cli): add sigil serve (rebuild-per-request dev server)"
```

---

### Task 5: Repoint the Makefile to sigil

Switch the core toolchain targets (build/install/run) to the `sigil` binary; pin the sigil-era `gen` target to the old path so it keeps working.

**Files:**
- Modify: `Makefile:1`, `Makefile:5-7`, `Makefile:68`, `Makefile:73`, `Makefile:77`, `Makefile:83-84`

**Interfaces:**
- Consumes: `core/cmd/sigil` (Task 1) and `core/cli.Version` (Task 1).
- Produces: a `make build` that emits `bin/sigil`; a `make install` that installs `sigil`.

- [ ] **Step 1: Edit the header comment**

Change `Makefile:1`:

```makefile
# Sigil Makefile
```

to:

```makefile
# Sigil Makefile
```

- [ ] **Step 2: Repoint the binary variables**

Change `Makefile:5-7`:

```makefile
BIN_NAME := sigil
CMD_PATH := ./cmd/sigil
PKG      := github.com/incantery/sigil/internal/cli
```

to:

```makefile
BIN_NAME := sigil
CMD_PATH := ./core/cmd/sigil
PKG      := github.com/incantery/sigil/core/cli
```

- [ ] **Step 3: Update the build/install/run help text**

Change `Makefile:68`:

```makefile
build: ## Build the sigil binary into bin/
```

to:

```makefile
build: ## Build the sigil binary into bin/
```

Change `Makefile:73`:

```makefile
install: ## Install sigil to $GOBIN (or $GOPATH/bin)
```

to:

```makefile
install: ## Install sigil to $GOBIN (or $GOPATH/bin)
```

Change `Makefile:77`:

```makefile
run: ## Run sigil without installing
```

to:

```makefile
run: ## Run sigil without installing
```

- [ ] **Step 4: Pin the sigil-era `gen` target to the old path**

The `gen` target reuses `$(CMD_PATH)`, which now points at sigil (no `gen` subcommand). Pin it to the old sigil path so it keeps working. Change `Makefile:83-84`:

```makefile
gen: ## Run sigil gen (sigil.gen.yaml) + refresh the conformance fixture
	@go run $(CMD_PATH) gen
```

to:

```makefile
gen: ## Run sigil gen (sigil.gen.yaml) + refresh the conformance fixture [legacy kernel]
	@go run ./cmd/sigil gen
```

- [ ] **Step 5: Verify the build target produces bin/sigil**

Run: `make build`
Expected: prints `→ bin/sigil (<version>)`; `bin/sigil` exists.

Then sanity-check the binary:

Run: `./bin/sigil version`
Expected: a line beginning with `sigil `.

Run: `./bin/sigil check --root . core/examples/counter/counter.sigil`
Expected: `ok  core/examples/counter/counter.sigil`.

- [ ] **Step 6: Verify the whole repo still builds and tests pass**

Run: `go build ./... && go test ./core/...`
Expected: build succeeds; `core/...` tests pass (browser e2e may skip without Chrome).

- [ ] **Step 7: Commit**

```bash
git add Makefile
git commit -m "build: repoint Makefile build/install/run to the sigil binary"
```

---

## Self-Review

**Spec coverage:**
- `core/cmd/sigil/main.go` thin wrapper → Task 1. ✓
- `core/cli` cobra surface, constructor pattern, `Version`, `ErrSilent` → Task 1. ✓
- `version` command → Task 1. ✓
- `check` (+`--json`, `{ok,file,error}`, `ErrSilent`) → Task 2. ✓
- `build` (`-o`, `--html`, shared `shell`/`htmlPage`/`bundle`) → Task 3. ✓
- `serve` (port of `core/cmd/serve`, fail-fast) → Task 4. ✓
- No-subcommand prints help → covered implicitly: a root cobra command with subcommands and no `Run` prints help by default (no explicit test; acceptable). ✓
- Makefile repoint (BIN_NAME/CMD_PATH/PKG, build/install/run), leave sigil-era targets → Task 5; `gen` pinned to old path. ✓
- No new deps (cobra already present), no viper/bubbletea → honored across tasks. ✓
- Tests in-process via `RunE`, output captured through `cmd.OutOrStdout` → Tasks 1-4. ✓

**Placeholder scan:** No TBD/TODO; every code step shows complete code. ✓

**Type consistency:** `newRootCmd`, `newVersionCmd`, `newCheckCmd`, `newBuildCmd`, `newServeCmd`, `bundle(entry, root)`, `htmlPage(title, js)`, `shell`, `run(args...)`, `repoRoot`, `counterEntry()`, `ErrSilent`, `Version` — names used consistently across tasks. ✓

**Note on Task 1 test scaffolding:** Step 1 writes `cli_test.go` with a temporary `var _ = cobra.Command{}` so the package compiles before subcommands exist; Step 3 removes both that line and the `cobra` import. This keeps the test-first ordering honest without an unused-import compile error.
