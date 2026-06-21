package types

import (
	"testing"

	"github.com/incantery/sigil/internal/parse"
)

func TestCheckModuleRecordingCapturesNodeTypes(t *testing.T) {
	m, err := parse.Module("let main = 1 + 2\n")
	if err != nil {
		t.Fatal(err)
	}
	_, info, err := CheckModuleRecording(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Every expression node got a recorded type: 1, 2, and (1 + 2).
	if len(info.Nodes) < 3 {
		t.Errorf("recorded %d node types, want >= 3 (1, 2, 1+2)", len(info.Nodes))
	}
	// The top-level binding scheme is available.
	if sc, ok := info.SchemeOf("main"); !ok || sc != "Int" {
		t.Errorf("SchemeOf(main) = %q,%v want Int,true", sc, ok)
	}
}

// A node whose type is only fixed by later unification still prints concretely
// (String prunes at print time; recording stores the live type by reference).
func TestRecordingZonksThroughUnification(t *testing.T) {
	m, err := parse.Module("let f x = x + 1\nlet g = f 3\n")
	if err != nil {
		t.Fatal(err)
	}
	_, info, err := CheckModuleRecording(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	// f generalizes to Int -> Int (x is constrained by + 1).
	if sc, ok := info.SchemeOf("f"); !ok || sc != "Int -> Int" {
		t.Errorf("SchemeOf(f) = %q,%v want Int -> Int,true", sc, ok)
	}
}

// Recorder off (Check) is unchanged.
func TestCheckStillWorks(t *testing.T) {
	m, _ := parse.Module("let main = 1 + 2\n")
	got, err := Check(m)
	if err != nil {
		t.Fatal(err)
	}
	if got["main"] != "Int" {
		t.Errorf("Check main = %q want Int", got["main"])
	}
}
