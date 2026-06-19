package style

import (
	"testing"

	"github.com/incantery/mako/pkg/ir"
)

// TestIconOnlyButtonSquarePadding locks REQUEST 11 item 4: a button
// with an icon and no label drops its wide inline text padding to the
// block value, so radius=full renders a circle instead of an oval.
// Explicit padding kwargs still win.
func TestIconOnlyButtonSquarePadding(t *testing.T) {
	iconOnly := ir.Node{Kind: ir.KindButton, Props: map[string]any{
		"label": "", "icon": "send", "icon-set": "Ui", "radius": "full",
	}}
	s := SpecFor(iconOnly)
	if s.PadInline != s.PadBlock {
		t.Errorf("icon-only button padding not square: inline %q vs block %q", s.PadInline, s.PadBlock)
	}

	labeled := ir.Node{Kind: ir.KindButton, Props: map[string]any{
		"label": "Send",
	}}
	if s := SpecFor(labeled); s.PadInline == s.PadBlock {
		t.Errorf("labeled button lost its wide inline padding: inline %q block %q", s.PadInline, s.PadBlock)
	}

	explicit := ir.Node{Kind: ir.KindButton, Props: map[string]any{
		"label": "", "icon": "send", "icon-set": "Ui", "padx": "10px", "pady": "10px",
	}}
	s = SpecFor(explicit)
	if s.PadInline != "10px" || s.PadBlock != "10px" {
		t.Errorf("explicit padding kwargs must win: inline %q block %q", s.PadInline, s.PadBlock)
	}
}
