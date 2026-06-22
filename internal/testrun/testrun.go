// Package testrun discovers *_test.sigil files, compiles each against the
// standard library, and runs it in goja (the non-browser tier). Tests that need
// a real DOM fail gracefully here; Slice B routes them to Chrome.
package testrun

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dop251/goja"
	"github.com/incantery/sigil/internal/browser"
	"github.com/incantery/sigil/internal/load"
)

// ExpectResult is one `expect` outcome.
type ExpectResult struct {
	Pass     bool   `json:"pass"`
	Label    string `json:"label"`
	Got      string `json:"got"`
	Expected string `json:"expected"`
}

// TestResult is one `test "name" { ... }` outcome.
type TestResult struct {
	Name    string         `json:"name"`
	Expects []ExpectResult `json:"expects"`
	Error   string         `json:"error"`
}

// discover returns the *_test.sigil files under path. path may be a file.
func discover(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	var files []string
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, "_test.sigil") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

// runFile compiles file as a test module and runs it in goja.
func runFile(file, root string) ([]TestResult, error) {
	prog, err := load.Load(file, load.Options{Root: root})
	if err != nil {
		return nil, err
	}
	js, err := prog.BundleTest()
	if err != nil {
		return nil, err
	}
	vm := goja.New()
	v, err := vm.RunString(js + "\n;JSON.stringify(__runTests())")
	if err != nil {
		return nil, err
	}
	var results []TestResult
	s, ok := v.Export().(string)
	if !ok {
		return nil, fmt.Errorf("__runTests() did not return a string (got %T)", v.Export())
	}
	if err := json.Unmarshal([]byte(s), &results); err != nil {
		return nil, err
	}
	return results, nil
}

// Run discovers and runs every *_test.sigil under path, writing a report to w.
// It returns true only if every test passed. Infrastructure failures (a path
// that cannot be walked) return an error; per-file compile/run failures are
// reported inline and make ok=false.
func Run(w io.Writer, path, root string) (bool, error) {
	files, err := discover(path)
	if err != nil {
		return false, err
	}
	total, passed, failed, skipped := 0, 0, 0, 0
	allOK := true

	// Lazily create one browser Session, shared across browser files.
	var sess *browser.Session
	var browserUnavailable bool
	getSession := func() *browser.Session {
		if sess == nil && !browserUnavailable {
			s, e := browser.New()
			if e != nil {
				browserUnavailable = true
				return nil
			}
			sess = s
		}
		return sess
	}
	defer func() {
		if sess != nil {
			sess.Close()
		}
	}()
	artifactDir := filepath.Join(".sigil-test", "last")

	for _, file := range files {
		fmt.Fprintln(w, file)

		prog, lerr := load.Load(file, load.Options{Root: root})
		browserFile := lerr == nil && isBrowserProgram(prog)

		var results []TestResult
		var rerr error
		if browserFile {
			s := getSession()
			if s == nil {
				skipped++
				fmt.Fprintf(w, "  ⤼ skipped (no Chrome): browser test\n")
				continue
			}
			results, rerr = runFileBrowser(file, root, s, artifactDir)
		} else {
			results, rerr = runFile(file, root)
		}
		if rerr != nil {
			allOK = false
			fmt.Fprintf(w, "  ✗ failed to compile/run: %v\n", rerr)
			continue
		}
		for _, r := range results {
			total++
			if r.Error == "" && allExpectsPass(r.Expects) {
				passed++
				fmt.Fprintf(w, "  ✓ %s\n", r.Name)
				continue
			}
			failed++
			allOK = false
			fmt.Fprintf(w, "  ✗ %s\n", r.Name)
			if r.Error != "" {
				hint := ""
				if looksBrowser(r.Error) {
					hint = " (looks like a browser test — needs std/browser)"
				}
				fmt.Fprintf(w, "      error: %s%s\n", r.Error, hint)
			}
			for _, ex := range r.Expects {
				if !ex.Pass {
					fmt.Fprintf(w, "      %s: expected %s, got %s\n", ex.Label, ex.Expected, ex.Got)
				}
			}
		}
	}
	fmt.Fprintf(w, "\n%d files, %d tests, %d passed, %d failed, %d skipped\n", len(files), total, passed, failed, skipped)
	return allOK, nil
}

func allExpectsPass(es []ExpectResult) bool {
	for _, e := range es {
		if !e.Pass {
			return false
		}
	}
	return true
}

// looksBrowser detects a goja failure caused by touching the DOM/host globals,
// so the report can point at Slice B.
func looksBrowser(msg string) bool {
	return strings.Contains(msg, "document") ||
		strings.Contains(msg, "window") ||
		strings.Contains(msg, "is not defined")
}
