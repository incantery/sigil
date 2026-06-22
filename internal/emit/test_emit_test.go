package emit

import (
	"encoding/json"
	"testing"

	"github.com/dop251/goja"
	"github.com/incantery/sigil/internal/parse"
	"github.com/incantery/sigil/internal/peval"
)

type expectJSON struct {
	Pass     bool   `json:"pass"`
	Label    string `json:"label"`
	Got      string `json:"got"`
	Expected string `json:"expected"`
}
type testJSON struct {
	Name    string       `json:"name"`
	Expects []expectJSON `json:"expects"`
	Error   string       `json:"error"`
}

func TestBundleTestRunsExpects(t *testing.T) {
	src := `test "demo" {
  expect { pass = true, label = "eqx", got = "1", expected = "1" };
  expect { pass = false, label = "eqx", got = "2", expected = "1" }
}`
	m, err := parse.Module(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	js, err := BundleTest([]LinkedModule{{ID: "entry", AST: m}}, peval.NewEnv())
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	vm := goja.New()
	v, err := vm.RunString(js + "\n;JSON.stringify(__runTests())")
	if err != nil {
		t.Fatalf("run: %v\n--- emitted ---\n%s", err, js)
	}
	var got []testJSON
	if err := json.Unmarshal([]byte(v.Export().(string)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Name != "demo" {
		t.Fatalf("got %+v, want one test named demo", got)
	}
	if len(got[0].Expects) != 2 || !got[0].Expects[0].Pass || got[0].Expects[1].Pass {
		t.Errorf("expects = %+v, want [pass, fail]", got[0].Expects)
	}
}
