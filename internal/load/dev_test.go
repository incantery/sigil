package load

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestProgramBundleDev(t *testing.T) {
	entry := filepath.Join("..", "..", "examples", "counter", "counter.sigil")
	prog, err := Load(entry, Options{Root: filepath.Join("..", "..")})
	if err != nil {
		t.Fatal(err)
	}
	dev, err := prog.BundleDev()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dev, "__sigilDev.counter++") {
		t.Error("dev bundle is missing the instrumented __cell")
	}
	prod, err := prog.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prod, "__sigilDev") {
		t.Error("production bundle must not contain dev instrumentation")
	}
}
