package devserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotAndChanged(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.sigil")
	if err := os.WriteFile(a, []byte("pub let x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A non-sigil file is ignored.
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	s1, err := Snapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s1) != 1 {
		t.Fatalf("snapshot tracked %d files, want 1 (.sigil only)", len(s1))
	}
	if Changed(s1, s1) {
		t.Error("identical snapshots reported as changed")
	}

	// Touch with a strictly newer mtime so the test is not clock-resolution flaky.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(a, future, future); err != nil {
		t.Fatal(err)
	}
	s2, err := Snapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !Changed(s1, s2) {
		t.Error("modified file not detected as changed")
	}
}

func TestWatchFiresOnChange(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.sigil")
	if err := os.WriteFile(a, []byte("pub let x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fired := make(chan struct{}, 4)
	stop := Watch(dir, 15*time.Millisecond, func() { fired <- struct{}{} })
	defer stop()

	time.Sleep(30 * time.Millisecond) // let the baseline snapshot settle
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(a, future, future); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not fire on change")
	}
}

func TestWatchStopHaltsFiring(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.sigil")
	if err := os.WriteFile(a, []byte("pub let x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fired := make(chan struct{}, 4)
	stop := Watch(dir, 15*time.Millisecond, func() { fired <- struct{}{} })

	time.Sleep(30 * time.Millisecond) // let the baseline snapshot settle
	stop()
	stop() // idempotent: a second call must not panic

	// Any change after a synchronous stop must not produce a fire.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(a, future, future); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fired:
		t.Fatal("watch fired after stop()")
	case <-time.After(150 * time.Millisecond):
		// good: no fire after stop
	}
}
