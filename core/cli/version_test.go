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
	if !strings.HasPrefix(out, "mako ") {
		t.Errorf("version output = %q, want prefix %q", out, "mako ")
	}
}
