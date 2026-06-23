package testrun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dop251/goja"
	"github.com/incantery/sigil/internal/browser"
	"github.com/incantery/sigil/internal/load"
)

// driver is the browser surface the runner needs (satisfied by *browser.Session
// and by test stubs).
type driver interface {
	Navigate(url string) error
	Click(sel string) error
	Fill(sel, text string) error
	WaitVisible(sel string) error
	DomText(sel string) (string, error)
}

// runFileBrowserWith compiles file and runs it in goja with the browser
// intrinsics bound to d (each call blocks on d and returns synchronously).
func runFileBrowserWith(file, root string, d driver) ([]TestResult, error) {
	prog, err := load.Load(file, load.Options{Root: root})
	if err != nil {
		return nil, err
	}
	js, err := prog.BundleTest()
	if err != nil {
		return nil, err
	}
	vm := goja.New()
	bindBrowser(vm, d)
	v, err := vm.RunString(js + "\n;JSON.stringify(__runTests())")
	if err != nil {
		return nil, err
	}
	s, ok := v.Export().(string)
	if !ok {
		return nil, fmt.Errorf("__runTests() did not return a string (got %T)", v.Export())
	}
	var results []TestResult
	if err := json.Unmarshal([]byte(s), &results); err != nil {
		return nil, err
	}
	return results, nil
}

// bindBrowser injects the five browser intrinsics, each delegating to d and
// throwing a JS error (caught by __runTests) on failure.
func bindBrowser(vm *goja.Runtime, d driver) {
	throw := func(err error) { panic(vm.ToValue(err.Error())) }
	vm.Set("__navigate", func(c goja.FunctionCall) goja.Value {
		if err := d.Navigate(c.Argument(0).String()); err != nil {
			throw(err)
		}
		return goja.Undefined()
	})
	vm.Set("__click", func(c goja.FunctionCall) goja.Value {
		if err := d.Click(c.Argument(0).String()); err != nil {
			throw(err)
		}
		return goja.Undefined()
	})
	vm.Set("__fill", func(c goja.FunctionCall) goja.Value {
		if err := d.Fill(c.Argument(0).String(), c.Argument(1).String()); err != nil {
			throw(err)
		}
		return goja.Undefined()
	})
	vm.Set("__waitVisible", func(c goja.FunctionCall) goja.Value {
		if err := d.WaitVisible(c.Argument(0).String()); err != nil {
			throw(err)
		}
		return goja.Undefined()
	})
	vm.Set("__domText", func(c goja.FunctionCall) goja.Value {
		txt, err := d.DomText(c.Argument(0).String())
		if err != nil {
			throw(err)
		}
		return vm.ToValue(txt)
	})
}

// runFileBrowser is the production entry: it uses a real *browser.Session and,
// on any failing/errored test, writes a screenshot + console + errors artifact.
func runFileBrowser(file, root string, sess *browser.Session, artifactDir string) ([]TestResult, error) {
	results, err := runFileBrowserWith(file, root, sess)
	if err != nil {
		return nil, err
	}
	failed := false
	for _, r := range results {
		if r.Error != "" || !allExpectsPass(r.Expects) {
			failed = true
			break
		}
	}
	if failed && artifactDir != "" {
		dir := filepath.Join(artifactDir, filepath.Base(file))
		_ = os.MkdirAll(dir, 0o755)
		if png, err := sess.ScreenshotPNG(); err == nil {
			_ = os.WriteFile(filepath.Join(dir, "screenshot.png"), png, 0o644)
		}
		_ = os.WriteFile(filepath.Join(dir, "console.log"), []byte(join(sess.Console())), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "errors.log"), []byte(join(sess.Errors())), 0o644)
	}
	return results, nil
}

func join(ss []string) string {
	out := ""
	for _, s := range ss {
		out += s + "\n"
	}
	return out
}
