package contract

import (
	"strings"
	"testing"

	"github.com/incantery/mako/pkg/ir"
	"github.com/incantery/mako/pkg/lang/lower"
	"github.com/incantery/mako/pkg/lang/parser"
)

func compile(t *testing.T, src string) ir.Document {
	t.Helper()
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return doc
}

const chatSrc = `type ChatDelta =
  thinking : String
  answer   : String

backend Api =
  url same-origin
  auth none

stream Chat -> prompt : String -> mode : String = ChatDelta
query ListModes = List<String>

view App =
  text "ok"
`

func TestFromDocExtractsOpsAndChannels(t *testing.T) {
	c := FromDoc(compile(t, chatSrc))
	if len(c.Streams) != 1 || c.Streams[0].Name != "Chat" {
		t.Fatalf("streams = %+v", c.Streams)
	}
	if got := strings.Join(c.Streams[0].Channels, ","); got != "thinking,answer" {
		t.Errorf("channels = %q", got)
	}
	if len(c.Queries) != 1 || c.Queries[0].Name != "ListModes" {
		t.Errorf("queries = %+v", c.Queries)
	}
	if len(c.Backends) != 1 || c.Backends[0].Name != "Api" {
		t.Errorf("backends = %+v", c.Backends)
	}
	if c.Empty() {
		t.Error("Empty() = true for a contract with ops")
	}
}

func TestHashStableAcrossDeclReorder(t *testing.T) {
	a := FromDoc(compile(t, chatSrc))
	// Same shapes, ops + types declared in a different order.
	reordered := `backend Api =
  url same-origin
  auth none

query ListModes = List<String>
stream Chat -> prompt : String -> mode : String = ChatDelta

type ChatDelta =
  thinking : String
  answer   : String

view App =
  text "ok"
`
	b := FromDoc(compile(t, reordered))
	if a.Hash() != b.Hash() {
		t.Errorf("hash not stable across decl reorder:\n  a=%s\n  b=%s", a.Hash(), b.Hash())
	}
}

func TestHashChangesWhenWireShapeChanges(t *testing.T) {
	a := FromDoc(compile(t, chatSrc))
	// One param type changed: String → Int.
	changed := strings.Replace(chatSrc, "mode : String", "mode : Int", 1)
	b := FromDoc(compile(t, changed))
	if a.Hash() == b.Hash() {
		t.Error("hash unchanged after a param type change")
	}
}

func TestHashIgnoresBackendURL(t *testing.T) {
	a := FromDoc(compile(t, chatSrc))
	moved := strings.Replace(chatSrc, "url same-origin", `url "http://localhost:8092"`, 1)
	b := FromDoc(compile(t, moved))
	if a.Hash() != b.Hash() {
		t.Error("hash changed on a backend URL move — deployment config must not affect the wire digest")
	}
}

func TestHashFormat(t *testing.T) {
	h := FromDoc(compile(t, chatSrc)).Hash()
	if !strings.HasPrefix(h, "c1:") || len(h) != len("c1:")+64 {
		t.Errorf("hash format = %q, want c1:<64 hex>", h)
	}
}
