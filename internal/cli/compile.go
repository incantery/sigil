package cli

import (
	"errors"

	"github.com/incantery/mako/pkg/ir"
	"github.com/incantery/mako/pkg/lang/diag"
	"github.com/incantery/mako/pkg/lang/loader"
	"github.com/incantery/mako/pkg/lang/lower"
)

// ErrSilent signals that a subcommand has already printed its own error
// output (e.g. as JSON to stdout). Returning it from RunE causes a nonzero
// exit without main's default stderr message.
var ErrSilent = errors.New("silent")

// compileFile is the CLI's single compilation entry point: loader
// resolves the module + package graph starting from path (file or
// directory), merges the program into one AST, and lowers it to IR.
//
// The historical "single file" model is preserved at the API level —
// every CLI subcommand still passes one path — but under the hood
// every compile is now a *package* compile that follows imports
// transitively. A bare file passed to a single-package project still
// works because the loader compiles its containing package.
//
// On parse/lower errors the underlying *diag.Diagnostic gets its File
// field stamped so structured-output consumers can rely on a fully-
// populated diagnostic instead of re-parsing message strings.
func compileFile(path string) (ir.Document, error) {
	prog, err := loader.Load(path)
	if err != nil {
		attachFile(err, path)
		return ir.Document{}, err
	}
	merged, err := prog.Merge()
	if err != nil {
		attachFile(err, path)
		return ir.Document{}, err
	}
	doc, err := lower.Lower(merged)
	if err != nil {
		attachFile(err, path)
		return ir.Document{}, err
	}
	return doc, nil
}

func attachFile(err error, path string) {
	var d *diag.Diagnostic
	if errors.As(err, &d) {
		d.File = path
	}
}
