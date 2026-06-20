package gogen

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/incantery/sigil/pkg/contract"
	"github.com/incantery/sigil/pkg/lang/lower"
	sparser "github.com/incantery/sigil/pkg/lang/parser"
)

// assertContains is the in-test grep helper. Fails the test with
// the full emitted Go printed when the needle is missing — quicker
// than parsing diffs.
func assertContains(t *testing.T, hay, needle string) {
	t.Helper()
	if !strings.Contains(hay, needle) {
		t.Fatalf("emitted Go missing %q\n---\n%s", needle, hay)
	}
}

// emitFromSource is the test helper: parse + lower a Sigil source
// string, extract the contract, and emit the Go API.
func emitFromSource(t *testing.T, src, pkgName string) string {
	t.Helper()
	root, err := sparser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	c := contract.FromDoc(doc)
	out, err := Emit(c, pkgName, c.Hash())
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return out
}

// TestEmitParsesAsValidGo is the structural lock: anything gogen
// emits must parse as a valid Go file. format.Source inside Emit
// already guarantees gofmt-cleanliness; this locks it from the
// outside too.
func TestEmitParsesAsValidGo(t *testing.T) {
	src := `type Stats =
  hp     : Int
  attack : Int
type PokemonType =
  | fire
  | water
  | grass
type Pokemon =
  id       : Int
  name     : String
  nickname : String?
  kind     : PokemonType
  active   : Bool
  stats    : Stats
type ChatDelta =
  thinking : String
  answer   : String

backend Api =
  url same-origin
  auth none

query ListPokemon = List<Pokemon>
query GetPokemon -> id : Int = Pokemon
command Rename -> id : Int -> nickname : String = Bool
stream Chat -> prompt : String -> mode : String = ChatDelta
stream Tail -> path : String = String

view App =
  text "ok"
`
	out := emitFromSource(t, src, "api")
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "gen.go", out, 0); err != nil {
		t.Fatalf("emitted Go does not parse: %v\n---\n%s", err, out)
	}
}

const chatSrc = `type ChatDelta =
  thinking : String
  answer   : String

backend Api =
  url same-origin
  auth none

query ListModes = List<String>
stream Chat -> prompt : String -> mode : String = ChatDelta
stream Tail -> path : String = String

view App =
  text "ok"
`

func TestEmitErrors(t *testing.T) {
	root, err := sparser.Parse(chatSrc)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatal(err)
	}
	c := contract.FromDoc(doc)
	if _, err := Emit(c, "", "h"); err == nil {
		t.Error("want error for empty package name")
	}
	if _, err := Emit(contract.Contract{}, "api", "h"); err == nil {
		t.Error("want error for empty contract")
	}
}

func TestContractHashStamped(t *testing.T) {
	root, _ := sparser.Parse(chatSrc)
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatal(err)
	}
	c := contract.FromDoc(doc)
	out, err := Emit(c, "api", c.Hash())
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, `const ContractHash = "`+c.Hash()+`"`)
}

func TestInterfaceNamedAfterBackend(t *testing.T) {
	out := emitFromSource(t, chatSrc, "api")
	assertContains(t, out, "type Api interface {")
	assertContains(t, out, "func NewApiHandler(impl Api, opts ...HandlerOption) http.Handler {")
	// Streams take the typed sender; queries stay (result, error).
	assertContains(t, out, "Chat(ctx context.Context, req ChatRequest, stream *ChatStream) error")
	assertContains(t, out, "ListModes(ctx context.Context, req ListModesRequest) ([]string, error)")
}

func TestStreamSenders(t *testing.T) {
	out := emitFromSource(t, chatSrc, "api")
	// Multi-channel: one method per channel, NDJSON framing inside.
	assertContains(t, out, "func (s *ChatStream) Thinking(text string) error {")
	assertContains(t, out, "func (s *ChatStream) Answer(text string) error {")
	assertContains(t, out, `writeChannel("thinking", text)`)
	// Scalar: Send, raw framing.
	assertContains(t, out, "func (s *TailStream) Send(text string) error {")
	assertContains(t, out, "writeRaw(text)")
}

func TestStreamHandlerErrorMapping(t *testing.T) {
	out := emitFromSource(t, chatSrc, "api")
	assertContains(t, out, `mux.HandleFunc("POST /stream/Chat"`)
	// 502 before first delta.
	assertContains(t, out, "http.Error(w, err.Error(), http.StatusBadGateway)")
	// Reserved error channel after first delta (multi-channel only).
	assertContains(t, out, `sw.writeChannel("__error", err.Error())`)
	// Content types split by stream shape.
	assertContains(t, out, `w.Header().Set("Content-Type", "application/x-ndjson")`)
	assertContains(t, out, `w.Header().Set("Content-Type", "text/plain; charset=utf-8")`)
}

// TestResponseControlsInjected locks REQUEST 13: every op handler
// (unary and stream) calls the impl with a context carrying the
// response controls, so an impl can reach serve.ResponseFrom(ctx).
func TestResponseControlsInjected(t *testing.T) {
	out := emitFromSource(t, chatSrc, "api")
	// Unary op: controls injected, then impl invoked with that ctx.
	assertContains(t, out, "impl.ListModes(serve.WithResponse(r.Context(), w, r), req)")
	// Stream op: same injection on the streaming call site.
	assertContains(t, out, "impl.Chat(serve.WithResponse(r.Context(), w, r), req, &ChatStream{s: sw})")
}

func TestPageHandlerEmitted(t *testing.T) {
	out := emitFromSource(t, chatSrc, "api")
	assertContains(t, out, "func NewPageHandler(sigilPackageDir string, opts ...serve.PageOption) (http.Handler, error) {")
	assertContains(t, out, "serve.ExpectContract(ContractHash)")
}

func TestMiddlewareSeam(t *testing.T) {
	out := emitFromSource(t, chatSrc, "api")
	assertContains(t, out, "func WithMiddleware(mw func(http.Handler) http.Handler) HandlerOption {")
	assertContains(t, out, "func WithMaxRequestBytes(n int64) HandlerOption {")
	assertContains(t, out, "return cfg.wrap(mux)")
}

func TestEmitShape(t *testing.T) {
	src := `type Tone =
  | dark
  | light
type Card =
  id    : Int
  label : String?
  tone  : Tone

query GetCard -> id : Int = Card
command SaveCard -> id : Int -> label : String = Bool

view App =
  text "ok"
`
	out := emitFromSource(t, src, "cards")
	assertContains(t, out, "package cards")
	assertContains(t, out, "type Card struct {")
	assertContains(t, out, "Label *string `json:\"label,omitempty\"`")
	assertContains(t, out, "type Tone string")
	// gofmt column-aligns const blocks, so match name and value
	// separately rather than assuming single-space separation.
	assertContains(t, out, "ToneDark")
	assertContains(t, out, `Tone = "dark"`)
	assertContains(t, out, "type GetCardRequest struct {")
	assertContains(t, out, "Id int64 `json:\"id\"`")
	// No backend declared → group named API.
	assertContains(t, out, "type API interface {")
	assertContains(t, out, "func NewAPIHandler(impl API, opts ...HandlerOption) http.Handler {")
	assertContains(t, out, `mux.HandleFunc("POST /query/GetCard"`)
	assertContains(t, out, `mux.HandleFunc("POST /command/SaveCard"`)
}

func TestZeroArgOp(t *testing.T) {
	src := `query Ping = Bool

view App =
  text "ok"
`
	out := emitFromSource(t, src, "api")
	assertContains(t, out, "type PingRequest struct{}")
	assertContains(t, out, "Ping(ctx context.Context, req PingRequest) (bool, error)")
}

func TestListReturn(t *testing.T) {
	src := `type Pokemon =
  id : Int

query ListPokemon = List<Pokemon>

view App =
  text "ok"
`
	out := emitFromSource(t, src, "api")
	assertContains(t, out, "ListPokemon(ctx context.Context, req ListPokemonRequest) ([]Pokemon, error)")
}

func TestValidatorsEmitted(t *testing.T) {
	src := `type Tone =
  | dark
  | light
type Card =
  id   : Int
  tone : Tone
type Deck =
  cards : List<Card>

query GetDeck = Deck
command SetTone -> tone : Tone = Bool

view App =
  text "ok"
`
	out := emitFromSource(t, src, "api")
	assertContains(t, out, "func validateTone(v Tone) error {")
	assertContains(t, out, `return fmt.Errorf("Tone: unknown variant %q", string(v))`)
	assertContains(t, out, "func validateCard(v Card) error {")
	assertContains(t, out, "func validateDeck(v Deck) error {")
	assertContains(t, out, "for i, elem := range v.Cards {")
	assertContains(t, out, "func validateSetToneRequest(v SetToneRequest) error {")
	assertContains(t, out, "if err := validateTone(v.Tone); err != nil {")
}

func TestStreamRequestValidated(t *testing.T) {
	out := emitFromSource(t, chatSrc, "api")
	assertContains(t, out, "func validateChatRequest(v ChatRequest) error {")
	assertContains(t, out, "if err := validateChatRequest(req); err != nil {")
}

func TestStreamSenderNameCollision(t *testing.T) {
	src := `type ChatStream =
  id : Int

stream Chat -> prompt : String = String

view App =
  text "ok"
`
	root, err := sparser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatal(err)
	}
	c := contract.FromDoc(doc)
	if _, err := Emit(c, "api", c.Hash()); err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("want collision error, got %v", err)
	}
}

func TestGoPackageName(t *testing.T) {
	cases := map[string]string{
		"internal/gen/chat":   "chat",
		"gen":                 "gen",
		"internal/gen/my-app": "myapp",
		"internal/gen/9lives": "lives",
	}
	for in, want := range cases {
		if got := goPackageName(in); got != want {
			t.Errorf("goPackageName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRejectsCrossKindOpNameCollision(t *testing.T) {
	src := `query Foo = Bool
command Foo = Bool

view App =
  text "ok"
`
	root, _ := sparser.Parse(src)
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatal(err)
	}
	c := contract.FromDoc(doc)
	if _, err := Emit(c, "api", c.Hash()); err == nil || !strings.Contains(err.Error(), "unique across kinds") {
		t.Fatalf("want cross-kind collision error, got %v", err)
	}
}

func TestRejectsRequestTypeCollision(t *testing.T) {
	src := `type FooRequest =
  id : Int

query Foo = Bool

view App =
  text "ok"
`
	root, _ := sparser.Parse(src)
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatal(err)
	}
	c := contract.FromDoc(doc)
	if _, err := Emit(c, "api", c.Hash()); err == nil || !strings.Contains(err.Error(), "request struct") {
		t.Fatalf("want request-type collision error, got %v", err)
	}
}

// TestUnionTypeEmit locks the discriminated-union Go representation:
// a tagged struct (Tag + per-variant pointer fields), New<Union>* con-
// structors, a {tag,value} JSON codec, and a tag-validating validator.
// A plain (all-unit) sum stays a string enum.
func TestUnionTypeEmit(t *testing.T) {
	src := `type User =
  name : String
type Result =
  | ok : User
  | failed : String
  | pending

query Check = Result

view App =
  text "ok"
`
	out := emitFromSource(t, src, "api")
	assertContains(t, out, "type Result struct {")
	// gofmt column-aligns struct fields, so match name + type loosely.
	assertContains(t, out, "Tag")
	assertContains(t, out, "*User")
	assertContains(t, out, "*string")
	assertContains(t, out, "func NewResultOk(v User) Result { return Result{Tag: \"ok\", Ok: &v} }")
	assertContains(t, out, "func NewResultPending() Result { return Result{Tag: \"pending\"} }")
	assertContains(t, out, "func (u Result) MarshalJSON() ([]byte, error) {")
	assertContains(t, out, "func (u *Result) UnmarshalJSON(data []byte) error {")
	assertContains(t, out, `Value any    `+"`json:\"value,omitempty\"`")
	// validator switches on the tag, recurses into the record payload.
	assertContains(t, out, "func validateResult(v Result) error {")
	assertContains(t, out, "switch v.Tag {")
	assertContains(t, out, "if err := validateUser(*v.Ok); err != nil {")
	// Check returns the union by value.
	assertContains(t, out, "Check(ctx context.Context, req CheckRequest) (Result, error)")
}
