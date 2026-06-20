package cli

import (
	"bytes"
	"path/filepath"
)

// repoRoot is the module root that holds std/ (two levels up from core/cli).
const repoRoot = "../.."

// counterEntry is the path to the committed counter example.
func counterEntry() string {
	return filepath.Join(repoRoot, "core", "examples", "counter", "counter.mako")
}

// run executes the mako command tree with args, capturing stdout and stderr.
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
