package cli

import (
	"strings"
	"testing"
)

func TestLspCommandRegistered(t *testing.T) {
	// `sigil lsp` exists and shows usage with --help (does not start the server).
	out, _, err := run("lsp", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "language server") {
		t.Errorf("lsp --help should describe the language server; got: %s", out)
	}
}
