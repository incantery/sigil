package load

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRecordPopulatesEntryInfo(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "app.sigil")
	if err := os.WriteFile(entry, []byte("let twice x = x + x\nlet main = twice 21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := Load(entry, Options{Root: dir, Record: true})
	if err != nil {
		t.Fatal(err)
	}
	if prog.EntryInfo == nil {
		t.Fatal("EntryInfo is nil with Record: true")
	}
	if sc, ok := prog.EntryInfo.SchemeOf("twice"); !ok || sc != "Int -> Int" {
		t.Errorf("SchemeOf(twice) = %q,%v want Int -> Int,true", sc, ok)
	}
	if len(prog.EntryInfo.Nodes) == 0 {
		t.Error("EntryInfo.Nodes is empty")
	}
}

func TestLoadWithoutRecordHasNilEntryInfo(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "app.sigil")
	if err := os.WriteFile(entry, []byte("let main = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := Load(entry, Options{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	if prog.EntryInfo != nil {
		t.Error("EntryInfo should be nil without Record")
	}
}
