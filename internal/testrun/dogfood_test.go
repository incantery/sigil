package testrun

import (
	"bytes"
	"testing"
)

// TestDogfood runs the real tests/ suite through the runner, wiring the sigil
// test suite into `go test ./...`.
func TestDogfood(t *testing.T) {
	var buf bytes.Buffer
	ok, err := Run(&buf, "../../tests", "../..")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, buf.String())
	}
	if !ok {
		t.Fatalf("dogfood tests failed:\n%s", buf.String())
	}
	t.Logf("\n%s", buf.String())
}
