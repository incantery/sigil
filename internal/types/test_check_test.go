package types

import (
	"strings"
	"testing"

	"github.com/incantery/sigil/internal/parse"
)

func checkSrc(t *testing.T, src string) error {
	t.Helper()
	m, err := parse.Module(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = Check(m)
	return err
}

func TestTestDeclAcceptsMatchRecord(t *testing.T) {
	src := `test "ok" {
  let c = __cell 0;
  __set c 1;
  expect { pass = __get c == 1, label = "eq", got = "1", expected = "1" }
}`
	if err := checkSrc(t, src); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestBrowserIntrinsicsTyped(t *testing.T) {
	// __domText is a read returning String; __navigate is String -> Unit (effect).
	src := `test "b" {
  __navigate "http://x";
  expect { pass = __domText "#h" == "hi", label = "eq", got = "got", expected = "exp" }
}`
	if err := checkSrc(t, src); err != nil {
		t.Fatalf("expected browser intrinsics to type-check, got %v", err)
	}
}

func TestTestDeclRejectsNonMatch(t *testing.T) {
	src := `test "bad" {
  expect 5
}`
	err := checkSrc(t, src)
	if err == nil {
		t.Fatal("expected a type error for non-Match expect argument")
	}
	if !strings.Contains(err.Error(), "Int") && !strings.Contains(err.Error(), "record") {
		t.Errorf("error %q should mention the mismatch", err.Error())
	}
}
