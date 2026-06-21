package lsp

import "testing"

func TestDocStoreSetGetRemove(t *testing.T) {
	d := newDocStore()
	if _, ok := d.get("file:///a.sigil"); ok {
		t.Fatal("empty store should not have the doc")
	}
	d.set("file:///a.sigil", "pub let x = 1")
	got, ok := d.get("file:///a.sigil")
	if !ok || got != "pub let x = 1" {
		t.Errorf("get = %q,%v want the text", got, ok)
	}
	d.remove("file:///a.sigil")
	if _, ok := d.get("file:///a.sigil"); ok {
		t.Error("removed doc should be gone")
	}
}

func TestDocStoreOverlayKeysByPath(t *testing.T) {
	d := newDocStore()
	d.set("file:///proj/app.sigil", "text-a")
	ov := d.overlay()
	if ov["/proj/app.sigil"] != "text-a" {
		t.Errorf("overlay should key by filesystem path; got %v", ov)
	}
}
