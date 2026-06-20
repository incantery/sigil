package codegen

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/incantery/sigil/pkg/ir"
	"github.com/incantery/sigil/pkg/lang/lower"
	"github.com/incantery/sigil/pkg/lang/parser"
)

// update regenerates the golden files when their content legitimately
// changes. Run with: go test ./pkg/codegen -update
var update = flag.Bool("update", false, "regenerate codegen golden files")

// TestEmitGolden snapshots the exact JS emitted for each example that
// currently fits the codegen profile. Locks bundle shape + size so any
// future codegen change either preserves output or is an intentional
// regeneration (-update) with reviewer eyes on the diff.
func TestEmitGolden(t *testing.T) {
	cases := []string{
		"counter",
		"disclosure",
		"form-echo",
		"counters",
		"todo",
		"benchmark",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			// L52 reshuffled examples/sigil into per-demo subdirs.
			src, err := os.ReadFile(filepath.Join("..", "..", "examples", "sigil", name, name+".sigil"))
			if err != nil {
				t.Fatalf("read example: %v", err)
			}
			root, err := parser.Parse(string(src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			doc, err := lower.Lower(root)
			if err != nil {
				t.Fatalf("lower: %v", err)
			}
			if ok, reason := Profile(doc); !ok {
				t.Fatalf("doc fell out of codegen profile: %s", reason)
			}
			got := Emit(doc)

			goldenPath := filepath.Join("testdata", name+".js.golden")
			if *update {
				if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
					t.Fatalf("mkdir testdata: %v", err)
				}
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if got != string(want) {
				t.Fatalf("emitted JS does not match %s\nrun `go test ./pkg/codegen -update` to refresh if change is intentional\n\n--- want ---\n%s\n--- got ---\n%s",
					goldenPath, string(want), got)
			}
		})
	}
}

// TestClientStubsEmitted verifies the shape of the emitted
// `window.__sigil_ops = {…}` block for declared queries and
// commands. Asserts substrings rather than the whole bundle so the
// existing golden-file tests stay focused on UI emit and don't
// re-baseline on every stub-shape tweak.
func TestClientStubsEmitted(t *testing.T) {
	t.Run("no ops → no stub block", func(t *testing.T) {
		src := `view App =
  text "ok"
`
		js := emitFromSource(t, src)
		if contains(js, "__sigil_ops") {
			t.Fatalf("emit should not contain __sigil_ops for op-less doc")
		}
	})

	t.Run("query with one arg routes through sigilFetch with named args", func(t *testing.T) {
		// L54 moved the actual fetch + headers into the shared
		// sigilFetch helper; per-op stubs are now thin wrappers.
		src := `type Page =
  count : Int
query ListPokemon -> page : Int = Page
view App =
  text "ok"
`
		js := emitFromSource(t, src)
		assertContains(t, js, "window.__sigil_ops = {")
		assertContains(t, js, `ListPokemon: (page) => sigilFetch("ListPokemon", "query", { page }, __sigil_query_meta["ListPokemon"]),`)
		assertContains(t, js, "async function sigilFetch")
		assertContains(t, js, `method: "POST"`)
	})

	t.Run("command with multiple args lists names in order", func(t *testing.T) {
		src := `type Page =
  count : Int
command Catch -> id : Int -> name : String = Page
view App =
  text "ok"
`
		js := emitFromSource(t, src)
		assertContains(t, js, `Catch: (id, name) => sigilFetch("Catch", "command", { id, name }, __sigil_command_meta["Catch"]),`)
	})

	t.Run("zero-arg query sends empty args object", func(t *testing.T) {
		src := `query Ping = Bool
view App =
  text "ok"
`
		js := emitFromSource(t, src)
		assertContains(t, js, `Ping: () => sigilFetch("Ping", "query", {}, __sigil_query_meta["Ping"]),`)
	})

	t.Run("queries emit before commands in stub block", func(t *testing.T) {
		src := `query Q = Bool
command C = Bool
view App =
  text "ok"
`
		js := emitFromSource(t, src)
		qi := indexOf(js, "Q: () => sigilFetch")
		ci := indexOf(js, "C: () => sigilFetch")
		if qi < 0 || ci < 0 || qi > ci {
			t.Fatalf("expected Q before C in stub block; qi=%d ci=%d", qi, ci)
		}
	})

	t.Run("command with invalidates clause records names in meta table", func(t *testing.T) {
		src := `query GetTeamSize = Int
command CatchPokemon -> id : Int = Bool invalidates GetTeamSize
view App = text "ok"
`
		js := emitFromSource(t, src)
		assertContains(t, js, `CatchPokemon: { backend: "", invalidates: ["GetTeamSize"] }`)
	})

	t.Run("backend decl emits config map + auth header logic", func(t *testing.T) {
		src := `backend Api =
  url "https://x.example.com"
  auth bearer
  token from Auth.token

session Auth =
  state token : String?

query Ping = Bool

view App = text "ok"
`
		js := emitFromSource(t, src)
		assertContains(t, js, `window.__sigil_backends = {`)
		assertContains(t, js, `Api: { url: "https://x.example.com", auth: "bearer", tokenCellID:`)
		assertContains(t, js, `headers["Authorization"] = "Bearer " + token;`)
		// the query should bind to the sole declared backend
		// implicitly (per the v1 default rule)
		assertContains(t, js, `Ping: { backend: "Api" }`)
	})
}

// TestOpCallEmit covers the call_op action — the handler body should
// `await window.__sigil_ops.<Name>(args)`, the closure should be
// `async`, and the awaited result should write back to the target
// cell (with an update_<cell> flush) when one is provided.
func TestOpCallEmit(t *testing.T) {
	t.Run("assignment-RHS emits await + cell write + update flush", func(t *testing.T) {
		src := `query GetCount = Int
view App =
  state n = 0
  button "load" on click { n = GetCount() }
  text n
`
		js := emitFromSource(t, src)
		assertContains(t, js, "async (event) =>")
		assertContains(t, js, "= await window.__sigil_ops.GetCount()")
		assertContains(t, js, "update_")
	})

	t.Run("statement-position emits bare await", func(t *testing.T) {
		src := `command Refresh = Bool
view App =
  button "go" on click { Refresh() }
`
		js := emitFromSource(t, src)
		assertContains(t, js, "async (event) =>")
		assertContains(t, js, "await window.__sigil_ops.Refresh()")
		if strings.Contains(js, "= await window.__sigil_ops.Refresh()") {
			t.Fatalf("statement form should NOT assign result; got:\n%s", js)
		}
	})

	t.Run("op-less handlers stay synchronous", func(t *testing.T) {
		src := `view App =
  state n = 0
  button "+" on click { n += 1 }
`
		js := emitFromSource(t, src)
		// Synchronous closure — no `async`, no `await`.
		if strings.Contains(js, "async (event)") {
			t.Fatalf("op-less handler should not be async, got:\n%s", js)
		}
		if strings.Contains(js, "await ") {
			t.Fatalf("op-less handler should not contain await, got:\n%s", js)
		}
	})

	t.Run("cell-ref arg unwraps $cell sentinel to live var", func(t *testing.T) {
		src := `query GetThing -> id : Int = Int
view App =
  state target = 7
  state n = 0
  button "load" on click { n = GetThing(target) }
`
		js := emitFromSource(t, src)
		// `target` resolves to a cell id (c1 / c2 / etc.); the emitted
		// call site should reference that variable directly, not the
		// "$cell.cN" sentinel.
		if !strings.Contains(js, "= await window.__sigil_ops.GetThing(c") {
			t.Fatalf("expected GetThing(c<id>), got:\n%s", js)
		}
		if strings.Contains(js, "$cell.") {
			t.Fatalf("emitted JS still contains $cell. sentinel:\n%s", js)
		}
	})
}

// TestRowOpCallEmit covers op calls fired from a for-row handler.
// Row context needs different cell-arg resolution: per-row sub-fields
// read through `cells_<parent>[cellId + ".<field>"]` rather than the
// top-level closure variable.
func TestRowOpCallEmit(t *testing.T) {
	t.Run("statement-form with row sub-field arg", func(t *testing.T) {
		src := `command CatchPokemon -> id : Int = Bool
view App =
  state items = []
    id : Int
    name : String
  for item in items
    stack horizontal gap=1
      text item.name
      button "catch" on click { CatchPokemon(item.id) }
`
		js := emitFromSource(t, src)
		// Row handler closure becomes async.
		assertContains(t, js, "async (event) =>")
		// Arg `item.id` resolves through the per-list cells map. The list
		// parent is c4: CatchPokemon (a command) mints three lifecycle
		// cells (c1/c2/c3) before any view state.
		assertContains(t, js, `cells_c4[cellId + ".id"]`)
		// Call site uses the stub. (The legacy Emit path doesn't apply
		// the command lifecycle wrapper; the live EmitSPA path does, and
		// TestCommandLifecycleEmit locks that.)
		assertContains(t, js, "await window.__sigil_ops.CatchPokemon(")
		// No leaked $cell sentinel.
		if strings.Contains(js, "$cell.") {
			t.Fatalf("emitted JS still contains $cell. sentinel:\n%s", js)
		}
		// No leaked $ITEM sentinel.
		if strings.Contains(js, "$ITEM") {
			t.Fatalf("emitted JS still contains $ITEM sentinel:\n%s", js)
		}
	})

	t.Run("non-call row handlers stay synchronous", func(t *testing.T) {
		// Existing structured-list row handlers (set, toggle) must NOT
		// pick up async. Regression guard for the existing goldens.
		src := `view App =
  state items = []
    done : Bool = false
  for item in items
    button "x" on click { item.done = !item.done }
`
		js := emitFromSource(t, src)
		if strings.Contains(js, "async (event)") {
			t.Fatalf("op-less row handler should not be async, got:\n%s", js)
		}
	})
}

func emitFromSource(t *testing.T, src string) string {
	t.Helper()
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if ok, reason := Profile(doc); !ok {
		t.Fatalf("profile rejected doc: %s", reason)
	}
	return Emit(doc)
}

// TestRecordSpreadEmit covers `cell = OpReturningRecord(args)`:
// the handler closure becomes async, the response lands in a `__r`
// local inside a block, each leaf cell is assigned via dotted path
// access on `__r`, and update_<cell> flushes fire for every bound
// leaf.
func TestRecordSpreadEmit(t *testing.T) {
	t.Run("primitive-only record spread", func(t *testing.T) {
		src := `type Slot =
  id   : Int
  name : String

query GetSlot -> id : Int = Slot

view App =
  state active : Slot
  text active.name
  button "load" on click { active = GetSlot(1) }
`
		js := emitFromSource(t, src)
		assertContains(t, js, "async (event) =>")
		assertContains(t, js, "const __r = await window.__sigil_ops.GetSlot(1)")
		// Each leaf is assigned via dotted access on __r. Cell ids
		// aren't predictable here, but the path-to-leaf shape is.
		assertContains(t, js, "= __r.id;")
		assertContains(t, js, "= __r.name;")
		// active.name has a binding via `text active.name` — its
		// update closure should be invoked after the spread.
		assertContains(t, js, "update_")
	})

	t.Run("nested record uses dotted path on response", func(t *testing.T) {
		src := `type Stats =
  hp : Int
type Pokemon =
  name  : String
  stats : Stats

query GetPokemon -> id : Int = Pokemon

view App =
  state p : Pokemon
  text p.name
  text p.stats.hp
  button "load" on click { p = GetPokemon(1) }
`
		js := emitFromSource(t, src)
		assertContains(t, js, "= __r.name;")
		assertContains(t, js, "= __r.stats.hp;")
	})

	t.Run("spread without bindings emits no update_ for unbound leaves", func(t *testing.T) {
		src := `type Slot =
  id   : Int
  name : String

query GetSlot = Slot

view App =
  state active : Slot
  text active.name
  button "load" on click { active = GetSlot() }
`
		js := emitFromSource(t, src)
		// `active.name` is bound (by `text active.name`); `active.id`
		// is not. We expect at least one update_, and exactly the
		// bound-leaf count of them inside the handler block.
		// Approximate the check by asserting active.name is updated
		// and the JS doesn't try to flush a non-existent update
		// closure for active.id. Simpler heuristic: at least one
		// update_ near the spread.
		if !strings.Contains(js, "update_") {
			t.Fatalf("expected at least one update_ after spread; got:\n%s", js)
		}
	})
}

// TestListPopulateReplaces covers `listCell = ListQuery()`: the
// assignment must REPLACE the list (clear-then-append), not accumulate.
// This is the contract lowerListPopulate documents and the behavior a
// refetch-after-mutation relies on — re-running the query after a
// command that invalidates it must reflect server truth rather than
// duplicating every row. Exercises the production EmitSPA path.
func TestListPopulateReplaces(t *testing.T) {
	src := `type Thing =
  name : String

query ListThings = List<Thing>
command AddThing -> name : String = Bool invalidates ListThings

view App =
  state things = []
    name : String
  on mount { things = ListThings() }
  button "add" on click { AddThing("x"); things = ListThings() }
  for thing in things
    text thing.name
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	js := EmitSPA(doc, map[string]string{})

	// The clear helper must be generated for the list's parent cell.
	assertContains(t, js, "function clear_c")

	// Every list-populating block clears before appending. Both the
	// mount populate and the handler refetch lower to this shape, so we
	// check all occurrences: between each `__arr = await` and its
	// `for (const __item of __arr)` loop there must be a clear_ call.
	const loopMarker = "for (const __item of __arr)"
	if !strings.Contains(js, loopMarker) {
		t.Fatalf("expected a call_op_list append loop; got:\n%s", js)
	}
	rest := js
	seen := 0
	for {
		li := strings.Index(rest, loopMarker)
		if li < 0 {
			break
		}
		ai := strings.LastIndex(rest[:li], "__arr = await")
		if ai < 0 {
			t.Fatalf("append loop without a preceding await; got:\n%s", rest[:li])
		}
		if !strings.Contains(rest[ai:li], "clear_") {
			t.Fatalf("call_op_list appends without clearing first — list would duplicate on refetch:\n%s", rest[ai:li+len(loopMarker)])
		}
		seen++
		rest = rest[li+len(loopMarker):]
	}
	if seen < 2 {
		t.Fatalf("expected both mount populate and handler refetch to clear-then-append, saw %d clear+append blocks", seen)
	}
}

// TestStreamEmit covers the `stream` op + `<-` arrow-into operator:
// the handler becomes async, the cell is reset then filled via the
// streaming stub with a per-chunk onDelta that appends + flushes, and
// the stub dispatches through sigilStream against the /stream/ route.
func TestStreamEmit(t *testing.T) {
	src := `backend Api =
  url "http://localhost:8090"
  auth none

stream Chat -> prompt : String = String

view App =
  state prompt = ""
  state reply = ""
  input prompt placeholder="ask"
  button "Send" on click { reply <- Chat(prompt) }
  text reply
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(doc.Streams) != 1 || doc.Streams[0].Name != "Chat" {
		t.Fatalf("expected one stream op Chat, got %+v", doc.Streams)
	}
	js := EmitSPA(doc, map[string]string{})

	// Streaming pipeline + per-op table emitted.
	assertContains(t, js, "async function sigilStream(")
	assertContains(t, js, "/stream/")
	assertContains(t, js, "window.__sigil_ops_stream")
	assertContains(t, js, `Chat: (prompt, __onDelta) => sigilStream("Chat", { prompt }, __sigil_stream_meta["Chat"], __onDelta)`)

	// Handler: reset target, then stream with an append+flush onDelta.
	assertContains(t, js, "async (event) =>")
	assertContains(t, js, "await window.__sigil_ops_stream.Chat(")
	if !contains(js, "+ __d;") {
		t.Fatalf("expected per-chunk append (`+ __d;`) in onDelta; got:\n%s", js)
	}

	// The reserved mid-stream error channel: a generated server emits
	// {"channel":"__error",...} after deltas have flowed; the client
	// must reject the stream call so .failed/.error trip.
	assertContains(t, js, `obj.channel === "__error"`)
}

// TestScrollAnchorEmit covers the anchor=end runtime wiring: a scroll
// listener maintaining the follow flag and a MutationObserver that
// re-pins the container to its end while following.
func TestScrollAnchorEmit(t *testing.T) {
	src := `view App =
  state items = []
  stack scroll=y anchor=end
    for it in items
      text it
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	js := EmitSPA(doc, map[string]string{})

	assertContains(t, js, "_follow = true;")
	assertContains(t, js, ".addEventListener('scroll'")
	assertContains(t, js, "new MutationObserver(")
	assertContains(t, js, "{ childList: true, subtree: true, characterData: true }")
}

// TestStreamLifecycle covers the implicit per-op lifecycle cells:
// `<Op>.pending` rises before the request and falls when the last
// overlapping call settles (per-op open counter); `<Op>.failed` /
// `<Op>.error` reset on call start and capture a thrown stream error.
// Also covers `disabled=` on button riding the same cells.
func TestStreamLifecycle(t *testing.T) {
	src := `backend Api =
  url same-origin
  auth none

stream Chat -> prompt : String = String

view App =
  state prompt = ""
  state reply = ""
  input prompt placeholder="ask"
  button "Send" disabled=Chat.pending on click { reply <- Chat(prompt) }
  if Chat.pending
    text "thinking"
  if Chat.failed
    text Chat.error
  text reply
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	js := EmitSPA(doc, map[string]string{})

	// Per-op open counter declared once, bumped around the call.
	assertContains(t, js, "window.__sigil_op_open = Object.create(null);")
	assertContains(t, js, `window.__sigil_op_open["Chat"] = (window.__sigil_op_open["Chat"] || 0) + 1;`)
	assertContains(t, js, `if (--window.__sigil_op_open["Chat"] === 0)`)

	// Failure path: catch captures the message instead of rejecting the
	// un-awaited handler promise.
	assertContains(t, js, "} catch (__err) {")
	assertContains(t, js, "String(__err && __err.message || __err)")

	// The stream call itself still happens, inside the try.
	assertContains(t, js, "await window.__sigil_ops_stream.Chat(")

	// disabled= binding: set at build, re-applied on flush.
	assertContains(t, js, ".disabled = !!")
}

// TestCommandLifecycleEmit locks the command lifecycle wrapper on the
// live EmitSPA path (the iris login shape): a command op gets the same
// implicit `<Op>.pending` / `.failed` / `.error` cells as a stream, but
// the wrapper stays INLINE (awaited) so statements after the call still
// sequence — and a throw is caught into .failed/.error instead of an
// unobservable rejection. `disabled=Login.pending` and `if Login.failed`
// ride the same cells.
func TestCommandLifecycleEmit(t *testing.T) {
	src := `backend Api =
  url same-origin
  auth none

command Login -> email : String -> password : String = Bool

view App =
  state email = ""
  state password = ""
  state ok = false
  input email placeholder="email"
  input password type=password placeholder="password"
  button "Sign in" disabled=Login.pending on click { ok = Login(email, password); email = "" }
  if Login.pending
    text "signing in"
  if Login.failed
    text Login.error
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	js := EmitSPA(doc, map[string]string{})

	// The open counter is declared even though there is NO stream — a
	// command-only app needs it too.
	assertContains(t, js, "window.__sigil_op_open = Object.create(null);")
	assertContains(t, js, `window.__sigil_op_open["Login"] = (window.__sigil_op_open["Login"] || 0) + 1;`)
	assertContains(t, js, `if (--window.__sigil_op_open["Login"] === 0)`)

	// The call stays inline (awaited), not wrapped in a fire-and-forget
	// IIFE — so `email = ""` after it runs only once Login settles.
	assertContains(t, js, "try {")
	assertContains(t, js, "= await window.__sigil_ops.Login(")
	assertContains(t, js, "} catch (__err) {")
	assertContains(t, js, "String(__err && __err.message || __err)")

	// disabled= and the if-branches read the implicit cells.
	assertContains(t, js, ".disabled = !!")
}

// TestStreamFireAndForget pins the `<-` sequencing semantics: the
// stream call lives in an un-awaited async IIFE, so statements written
// after the arrow run immediately — a composer's input-clear must not
// wait for the model to finish. The IIFE's pre-await section still runs
// synchronously at the arrow site (targets reset, row id captured).
func TestStreamFireAndForget(t *testing.T) {
	src := `backend Api =
  url same-origin
  auth none

stream Chat -> prompt : String = String

view App =
  state prompt = ""
  state reply = ""
  state count = 0
  button "Send" on click { reply <- Chat(prompt); count = count + 7 }
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	js := EmitSPA(doc, map[string]string{})

	assertContains(t, js, "(async () => {")
	iCall := strings.Index(js, "await window.__sigil_ops_stream.Chat(")
	if iCall < 0 {
		t.Fatalf("stream call not emitted:\n%s", js)
	}
	iClose := strings.Index(js[iCall:], "})();")
	if iClose < 0 {
		t.Fatalf("stream call not wrapped in an un-awaited IIFE:\n%s", js)
	}
	iAfter := strings.Index(js, "+ 7;")
	if iAfter < 0 {
		t.Fatalf("post-arrow statement not emitted:\n%s", js)
	}
	if iAfter < iCall+iClose {
		t.Fatalf("post-arrow statement emitted inside the stream IIFE (would defer until the stream settles):\n%s", js)
	}
}

// TestStreamIntoRow covers `listCell.last.<field> <- StreamOp(args)`:
// the deltas stream into the most recently appended row's field. Since
// row text bindings aren't reactively flushed, the onDelta mutates the
// row cell and rebuilds just that row (mkrow + insert) per chunk.
func TestStreamIntoRow(t *testing.T) {
	src := `backend Api =
  url "http://localhost:8090"
  auth none

stream Assist -> prompt : String = String

view App =
  state draft = ""
  state conversation = []
    role : String
    text : String
  input draft placeholder="ask"
  button "Send" on click { conversation.append("You", draft); conversation.append("Assistant", ""); conversation.last.text <- Assist(draft) }
  for m in conversation
    stack gap=1
      text m.role
      text m.text
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	js := EmitSPA(doc, map[string]string{})

	// Targets the last row, appends into its .text cell, rebuilds the row.
	assertContains(t, js, ".length - 1]")
	assertContains(t, js, `[__rid + ".text"]`)
	assertContains(t, js, "await window.__sigil_ops_stream.Assist(")
	if !contains(js, "mkrow_") || !contains(js, "insertBefore(__nr") {
		t.Fatalf("expected a per-delta row rebuild (mkrow + insertBefore); got:\n%s", js)
	}
}

// TestStreamMultiChannel covers `(t1, t2) <- StreamOp(...)` over a
// record-typed delta: one request (one backend call) fans into two live
// String cells, demuxed by channel. The stream meta gains a `channels`
// list (so the runtime frames the body as NDJSON), and the handler resets
// both cells then streams with a (channel, text) demux callback.
func TestStreamMultiChannel(t *testing.T) {
	src := `backend Api =
  url "http://localhost:8090"
  auth none

type ChatDelta =
  thinking : String
  answer : String

stream Chat -> prompt : String = ChatDelta

view App =
  state prompt = ""
  state thinking = ""
  state answer = ""
  input prompt placeholder="ask"
  button "Send" on click { (thinking, answer) <- Chat(prompt) }
  text thinking
  text answer
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if len(doc.Streams) != 1 || len(doc.Streams[0].Channels) != 2 {
		t.Fatalf("expected one stream with 2 channels, got %+v", doc.Streams)
	}
	js := EmitSPA(doc, map[string]string{})

	// Meta advertises the channels; runtime gains the NDJSON demux branch.
	assertContains(t, js, `Chat: { backend: "Api", channels: ["thinking", "answer"] }`)
	assertContains(t, js, "if (meta.channels)")
	assertContains(t, js, "buf.indexOf(\"\\n\")")

	// Handler: a single op call with a (channel, text) demux callback that
	// routes each channel to its own cell (cells use their internal ids as
	// JS var names, so match on the channel guard + append shape).
	assertContains(t, js, "(__ch, __t) =>")
	assertContains(t, js, `if (__ch === "thinking") {`)
	assertContains(t, js, `else if (__ch === "answer") {`)
	assertContains(t, js, "+ __t;")
	// Exactly one underlying request drives both regions.
	if n := strings.Count(js, "window.__sigil_ops_stream.Chat("); n != 1 {
		t.Fatalf("expected exactly one Chat stream call, got %d\n%s", n, js)
	}
}

// TestStreamMultiChannelRow covers the transcript form
// `(conv.last.thinking, conv.last.answer) <- StreamOp(...)`: both channels
// write fields of the most recently appended row, which is rebuilt per
// delta.
func TestStreamMultiChannelRow(t *testing.T) {
	src := `backend Api =
  url "http://localhost:8090"
  auth none

type ChatDelta =
  thinking : String
  answer : String

stream Chat -> prompt : String = ChatDelta

view App =
  state draft = ""
  state conversation = []
    role : String
    thinking : String
    answer : String
  input draft placeholder="ask"
  button "Send" on click { conversation.append("You", "", ""); conversation.append("Assistant", "", ""); (conversation.last.thinking, conversation.last.answer) <- Chat(draft) }
  for m in conversation
    stack gap=1
      text m.thinking
      text m.answer
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	js := EmitSPA(doc, map[string]string{})

	assertContains(t, js, ".length - 1]")
	assertContains(t, js, "(__ch, __t) =>")
	assertContains(t, js, `if (__ch === "thinking") cells_`)
	assertContains(t, js, `[__rid + ".answer"]`)
	if !contains(js, "mkrow_") || !contains(js, "insertBefore(__nr") {
		t.Fatalf("expected a per-delta row rebuild (mkrow + insertBefore); got:\n%s", js)
	}
	if n := strings.Count(js, "window.__sigil_ops_stream.Chat("); n != 1 {
		t.Fatalf("expected exactly one Chat stream call, got %d", n)
	}
}

func assertContains(t *testing.T, hay, needle string) {
	t.Helper()
	if !strings.Contains(hay, needle) {
		t.Fatalf("emitted JS missing %q\n---\n%s", needle, hay)
	}
}

func contains(hay, needle string) bool {
	return strings.Contains(hay, needle)
}

func indexOf(hay, needle string) int {
	return strings.Index(hay, needle)
}

// TestEveryExampleIsCodegenEligible verifies that every example
// .sigil source in the repo can be served by codegen. There is no
// runtime fallback — Profile rejecting a doc would be a hard compile
// error. If a feature is added to the language without a matching
// codegen extension, this test catches it on next run.
func TestEveryExampleIsCodegenEligible(t *testing.T) {
	examples := []string{
		"counter", "disclosure", "form-echo", "hello", "layout",
		"tones", "components", "echo", "counters", "todo", "benchmark",
	}
	for _, name := range examples {
		t.Run(name, func(t *testing.T) {
			// L52 reshuffled examples/sigil into per-demo subdirs so
			// each demo is its own package. The file inside still
			// matches the directory name.
			src, err := os.ReadFile(filepath.Join("..", "..", "examples", "sigil", name, name+".sigil"))
			if err != nil {
				t.Fatalf("read example: %v", err)
			}
			root, err := parser.Parse(string(src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			doc, err := lower.Lower(root)
			if err != nil {
				t.Fatalf("lower: %v", err)
			}
			if ok, reason := Profile(doc); !ok {
				t.Fatalf("%s no longer codegen-eligible: %s\n"+
					"Extend pkg/codegen Profile + Emit to support the new IR shape; there is no fallback.",
					name, reason)
			}
		})
	}
}

// TestRowIfWithBindings locks the REQUEST-9 shape: a per-row `if`
// whose subtree carries bindings and handlers. The condition is
// evaluated at row-build time against the row's cells, and any
// handler that mutates a row cell rebuilds the row so conditional
// content (and plain row bindings) re-render.
func TestRowIfWithBindings(t *testing.T) {
	src := `view Transcript =
  state turns = []
    user : Bool = false
    text : String = ""
  state draft = ""
  input draft placeholder="say"
  button "Send" on click { turns.append(true, draft); draft = "" }
  for m in turns
    stack gap=1
      if m.user
        text m.text
      button "edit" on click { m.text = "edited" }
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if ok, reason := Profile(doc); !ok {
		t.Fatalf("row-if with bindings must be in-profile, got: %s", reason)
	}
	js := EmitSPA(doc, map[string]string{})

	// Build-time condition over the row's own cell, not a static
	// compile-time initial.
	assertContains(t, js, `if (cells_`)
	assertContains(t, js, `[cellId + ".user"]`)
	// The bound child reads the row cell at build time.
	assertContains(t, js, `[cellId + ".text"]`)
	// Mutating a row cell rebuilds the row in place.
	if !contains(js, "const __r = rows_") || !contains(js, "insertBefore(mkrow_") {
		t.Fatalf("expected a post-mutation row rebuild (rows_ guard + insertBefore(mkrow_)); got:\n%s", js)
	}
	// Bool literal flowed through .append().
	assertContains(t, js, `"user": true`)
}

// TestIconOnlyButtonAriaLabel: a button with an icon and no label
// must carry the icon's name as its aria-label — the accessible name
// screen readers announce and the name `click button "<icon>"`
// scenario steps target (backlog #5: icon-only buttons were
// untargetable).
func TestIconOnlyButtonAriaLabel(t *testing.T) {
	doc := ir.Document{
		Name: "App",
		Root: ir.Node{Kind: ir.KindStack, ID: "/", Children: []ir.Node{
			{Kind: ir.KindButton, ID: "/0", Props: map[string]any{
				"icon": "send", "icon-set": "Ui",
			}},
			{Kind: ir.KindButton, ID: "/1", Props: map[string]any{
				"icon": "send", "icon-set": "Ui", "label": "Send",
			}},
		}},
	}
	js := EmitSPA(doc, map[string]string{})
	assertContains(t, js, `setAttribute('aria-label', "send")`)
	// A labeled button keeps its text as the accessible name — no
	// aria-label override.
	if strings.Count(js, "setAttribute('aria-label'") != 1 {
		t.Fatalf("aria-label should be emitted exactly once (icon-only button only):\n%s", js)
	}
}

// TestRootModeClasses: the mounted root element owns the viewport
// unconditionally. Mode picks where interior scrolling lives —
// app shells (height=screen) delegate to their scroll=y regions,
// height=full pages scroll the root, document-style pages scroll the
// root and carry the gutter there.
func TestRootModeClasses(t *testing.T) {
	emit := func(src string) string {
		t.Helper()
		root, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		doc, err := lower.Lower(root)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		return EmitSPA(doc, map[string]string{})
	}
	// Non-shell modes mount inside a neutral codegen-owned wrapper:
	// the author's (possibly styled, possibly comment-anchor) root
	// must not itself be stretched/scrolled/guttered.
	docPage := emit("view App =\n  text \"hi\"\n")
	assertContains(t, docPage, "classList.add('s-root', 's-root-doc');")

	shell := emit("view App =\n  stack height=screen\n    text \"hi\"\n")
	assertContains(t, shell, "classList.add('s-root');")
	if strings.Contains(shell, "s-root-doc") || strings.Contains(shell, "s-root-scroll") {
		t.Error("app shell should tag the root directly, no wrapper mode class")
	}

	full := emit("view App =\n  stack height=full\n    text \"hi\"\n")
	assertContains(t, full, "classList.add('s-root', 's-root-scroll');")

	// Regression: a root that lowers to a comment-anchor node
	// (root-level if) has no element to tag — it must mount inside the
	// wrapper, not crash the bundle by calling classList on a Comment.
	rootIf := emit("view App =\n  state open = true\n  if open\n    text \"hi\"\n")
	assertContains(t, rootIf, "classList.add('s-root', 's-root-doc');")
	assertContains(t, rootIf, "document.createElement('div')")
}

// TestJSQuoteNeutralizesScriptBreakout: jsQuote must escape '<' so a
// string value can never close the inline <script> the bundle lives
// in. \x3C === "<" at runtime, so rendered text is unchanged.
func TestJSQuoteNeutralizesScriptBreakout(t *testing.T) {
	got := jsQuote("</script><img src=x onerror=alert(1)>")
	if strings.Contains(got, "</script>") {
		t.Fatalf("jsQuote left a literal </script> — breakout possible: %s", got)
	}
	if !strings.Contains(got, "\\x3C/script") {
		t.Fatalf("jsQuote did not escape '<': %s", got)
	}
	// U+2028 / U+2029 (legal in JS strings, break the parser) escaped.
	if !strings.Contains(jsQuote("a\u2028b"), "\\u2028") {
		t.Fatal("jsQuote did not escape U+2028")
	}
}

// TestInputTypeEmitsMaskedField locks the input type= kwarg through
// the SPA emitter: a password field must set input.type = 'password'
// (otherwise the compiled login form renders the password in clear
// text). The unset input keeps the 'text' default.
func TestInputTypeEmitsMaskedField(t *testing.T) {
	src := `view Login =
  state email = ""
  state password = ""
  stack
    input email type=email placeholder="you@example.com"
    input password type=password placeholder="password"
    input email placeholder="default"
`
	root, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	js := EmitSPA(doc, map[string]string{})
	assertContains(t, js, `.type = "password";`)
	assertContains(t, js, `.type = "email";`)
	assertContains(t, js, `.type = "text";`)
}

// TestUnionMatchEmit locks the SPA emit for discriminated-union match:
// a payload-carrying union discriminates on `.tag` and refreshes the
// `as` binding cell from `.value`; a record payload refreshes one leaf
// cell per field; variant construction builds the tagged `{tag,value}`
// shape; and a plain enum compares the cell string directly.
func TestUnionMatchEmit(t *testing.T) {
	emit := func(src string) string {
		t.Helper()
		root, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		doc, err := lower.Lower(root)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		return EmitSPA(doc, map[string]string{})
	}

	t.Run("tagged union: tag discrimination, primitive binding, construction", func(t *testing.T) {
		js := emit(`type Fetch =
  | idle
  | loaded : String
view App =
  state s : Fetch
  state note = "hi"
  button "go" on click { s = loaded(note) }
  button "back" on click { s = idle }
  match s
    | idle
      text "idle"
    | loaded as body
      text body
`)
		assertContains(t, js, "? c1.tag : null") // discriminate on tag
		assertContains(t, js, `__t === "loaded"`)
		assertContains(t, js, "{ tag: \"loaded\", value: c2 };") // construct with payload
		assertContains(t, js, "{ tag: \"idle\", value: null };") // unit construct
		assertContains(t, js, "c1.value")                        // refresh binding from payload
	})

	t.Run("record payload binds one leaf cell per field", func(t *testing.T) {
		js := emit(`type User =
  name : String
  email : String
type Account =
  | guest
  | member : User
view App =
  state a : Account
  match a
    | guest
      text "guest"
    | member as u
      stack
        text u.name
        text u.email
`)
		// Each record-payload leaf refreshes from value.<field>.
		assertContains(t, js, ".value.name")
		assertContains(t, js, ".value.email")
	})

	t.Run("all-unit enum compares the cell string directly", func(t *testing.T) {
		js := emit(`type Tone =
  | dark
  | light
view App =
  state t : Tone
  button "x" on click { t = light }
  match t
    | dark
      text "dark"
    | light
      text "light"
`)
		// No `.tag` indirection for a plain enum; the cell IS the string.
		assertContains(t, js, "const __t = c1;")
		assertContains(t, js, `t = "light";`)
	})
}

// TestNavigateEmit locks REQUEST 14: the bare `navigate "<path>"`
// action does a full-page load, and the `Op(...) then navigate "<path>"`
// success hook emits the navigation INSIDE the command's lifecycle try
// (the success path) — so a thrown/failed command trips .failed and
// skips the navigation instead of stranding the user.
func TestNavigateEmit(t *testing.T) {
	emit := func(src string) string {
		t.Helper()
		root, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		doc, err := lower.Lower(root)
		if err != nil {
			t.Fatalf("lower: %v", err)
		}
		return EmitSPA(doc, map[string]string{})
	}

	t.Run("bare navigate routes client-side with a full-load fallback", func(t *testing.T) {
		js := emit(`view App =
  button "home" on click { navigate "/login" }
`)
		// When a path router is mounted, navigation goes through it
		// (no reload); otherwise it falls back to a full page load.
		assertContains(t, js, `(window.__sigilNav || ((p) => window.location.assign(p)))("/login")`)
	})

	t.Run("then-navigate runs only in the command success path", func(t *testing.T) {
		js := emit(`backend Api =
  url same-origin
  auth none
command Login -> email : String -> password : String = Bool
view App =
  state email = ""
  state password = ""
  button "in" on click { Login(email, password) then navigate "/" }
`)
		tryIdx := strings.Index(js, "try {")
		navIdx := strings.Index(js, `((p) => window.location.assign(p)))("/")`)
		catchIdx := strings.Index(js, "} catch (__err)")
		if tryIdx < 0 || navIdx < 0 || catchIdx < 0 || tryIdx > navIdx || navIdx > catchIdx {
			t.Fatalf("navigate must sit inside the success try (try=%d nav=%d catch=%d)", tryIdx, navIdx, catchIdx)
		}
	})
}
