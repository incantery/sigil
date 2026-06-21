package emit

import (
	"strings"
	"testing"
)

// Production prelude must remain exactly today's bytes.
func TestProdPreludeUnchanged(t *testing.T) {
	if !strings.Contains(prelude, "const __cell = (init) => ({ v: init, subs: new Set() });") {
		t.Fatal("production __cell line changed; update the golden expectation deliberately")
	}
}

// The dev prelude swaps in the instrumented intrinsics.
func TestDevPreludeInstrumented(t *testing.T) {
	if strings.Contains(devPrelude, "const __cell = (init) => ({ v: init, subs: new Set() });") {
		t.Error("dev prelude still has the production __cell")
	}
	if !strings.Contains(devPrelude, "__sigilDev.counter++") {
		t.Error("dev __cell must take a call-order index from __sigilDev.counter")
	}
	if !strings.Contains(devPrelude, "__sigilDev.hydration") {
		t.Error("dev __cell must consult the hydration map")
	}
	if !strings.Contains(devPrelude, "__sigilDev.disposers.push") {
		t.Error("dev __onPopState must register a disposer")
	}
	if !strings.Contains(devPrelude, `getElementById("__sigil_styles")`) {
		t.Error("dev __installStyles must reuse a keyed <style> tag")
	}
	if !strings.Contains(devPrelude, "__sigilDev.generation") {
		t.Error("dev __fetch must guard on generation")
	}
	// Everything else (e.g. __each) is preserved verbatim.
	if !strings.Contains(devPrelude, "const __each = (src) => (render) => {") {
		t.Error("dev prelude dropped a non-instrumented intrinsic")
	}
}
