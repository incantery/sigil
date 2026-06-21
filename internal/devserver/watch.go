package devserver

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Snapshot maps every .sigil file under root to its mtime (unix nanos).
func Snapshot(root string) (map[string]int64, error) {
	out := map[string]int64{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".sigil") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[path] = info.ModTime().UnixNano()
		return nil
	})
	return out, err
}

// Changed reports whether any file was added, removed, or has a different mtime.
func Changed(prev, cur map[string]int64) bool {
	if len(prev) != len(cur) {
		return true
	}
	for p, m := range cur {
		if prev[p] != m {
			return true
		}
	}
	return false
}

// Watch polls root every interval and calls onChange whenever the .sigil set
// changes. A burst of edits between ticks coalesces into a single onChange. The
// returned stop function ends polling and blocks until the poll goroutine has
// fully exited; it is safe to call more than once.
func Watch(root string, interval time.Duration, onChange func()) (stop func()) {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		prev, _ := Snapshot(root)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				cur, err := Snapshot(root)
				if err != nil {
					continue
				}
				if Changed(prev, cur) {
					prev = cur
					onChange()
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
		<-stopped
	}
}
