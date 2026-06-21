package emit

import (
	"testing"

	"github.com/dop251/goja"
)

// The dev __cell takes its identity from creation order: with a hydration map
// {0: 41, 1: 99}, the first cell created starts at 41 and the second at 99,
// regardless of their init values. A cell with no hydration entry uses init.
func TestDevCellHydratesByOrder(t *testing.T) {
	vm := goja.New()
	// Stand in for the browser/agent registry.
	_, err := vm.RunString(`
var __sigilDev = {
  counter: 0,
  hydration: new Map([[0, 41], [1, 99]]),
  cells: new Map(),
  disposers: [],
  generation: 0,
};
`)
	if err != nil {
		t.Fatal(err)
	}
	// Run the dev prelude, then create three cells with init 0, 0, 7.
	v, err := vm.RunString(devPrelude + `
;(() => {
  const a = __cell(0);
  const b = __cell(0);
  const c = __cell(7);
  return [a.v, b.v, c.v];
})()`)
	if err != nil {
		t.Fatalf("JS error: %v", err)
	}
	got := v.Export().([]any)
	if got[0] != int64(41) || got[1] != int64(99) || got[2] != int64(7) {
		t.Errorf("hydrated values = %v, want [41 99 7]", got)
	}
}
