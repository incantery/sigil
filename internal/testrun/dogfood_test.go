package testrun

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestDogfood runs the real tests/ suite through the runner, wiring the sigil
// test suite into `go test ./...`. tests/browser/ is excluded: those files
// require a live app server (run them manually via make test-browser).
func TestDogfood(t *testing.T) {
	// Discover test files but skip tests/browser/ (needs a live server).
	const testsDir = "../../tests"
	var files []string
	if err := filepath.WalkDir(testsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "browser" {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, "_test.sigil") {
			files = append(files, p)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", testsDir, err)
	}
	sort.Strings(files)

	var buf bytes.Buffer
	allOK := true
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil || info.IsDir() {
			continue
		}
		var b bytes.Buffer
		ok, rerr := Run(&b, f, "../..")
		if _, werr := buf.Write(b.Bytes()); werr != nil {
			t.Fatal(werr)
		}
		if rerr != nil {
			t.Fatalf("run %s: %v\n%s", f, rerr, b.String())
		}
		if !ok {
			allOK = false
		}
	}
	if !allOK {
		t.Fatalf("dogfood tests failed:\n%s", buf.String())
	}
	t.Logf("\n%s", buf.String())
}
