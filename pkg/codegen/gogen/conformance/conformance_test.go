// Package conformance is the executable wire-contract spec for
// generated sigil servers. The generated client's expectations are
// pinned here as HTTP-level tests against a real generated handler
// (sigil.gen.go, committed; TestGeneratedUpToDate locks it to the
// fixture). Any future backend target (python, …) must pass the
// same suite over its own generated output.
//
// The rules under test:
//
//  1. Ops are POST /query/<Op>, /command/<Op>, /stream/<Op> with a
//     JSON body keyed by declared param names.
//  2. Malformed bodies and validation failures → 400.
//  3. Unary impl errors → 500; result encodes as one JSON value
//     with Content-Type application/json.
//  4. Multi-channel streams: Content-Type application/x-ndjson, one
//     {"channel","text"} object per line, flushed per delta.
//  5. Scalar streams: Content-Type text/plain, raw chunks appended
//     verbatim.
//  6. Stream impl error BEFORE the first delta → non-200 (502), so
//     the client's .failed/.error cells trip.
//  7. Stream impl error AFTER the first delta → multi-channel
//     streams emit a final {"channel":"__error","text":…} line (the
//     client recognizes the reserved channel and rejects); scalar
//     streams have no in-band frame, so the handler aborts the
//     connection uncleanly (panic(http.ErrAbortHandler)) — the
//     client's reader rejects, which is what trips .failed/.error.
//     A clean return is forbidden: it would write the terminating
//     chunk and present truncation as success.
//  8. Request bodies are capped (default 1 MiB, WithMaxRequestBytes
//     to change); over-cap → 400.
//  9. WithMiddleware wraps the whole handler, outermost-first.
package conformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"context"

	"github.com/incantery/sigil/pkg/codegen/gogen"
	"github.com/incantery/sigil/pkg/contract"
	"github.com/incantery/sigil/pkg/lang/lower"
	"github.com/incantery/sigil/pkg/lang/parser"
	"github.com/incantery/sigil/pkg/serve"
)

var update = flag.Bool("update", false, "regenerate sigil.gen.go from fixture.sigil")

func fixtureContract(t *testing.T) contract.Contract {
	t.Helper()
	src, err := os.ReadFile("fixture.sigil")
	if err != nil {
		t.Fatal(err)
	}
	root, err := parser.Parse(string(src))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	doc, err := lower.Lower(root)
	if err != nil {
		t.Fatalf("lower fixture: %v", err)
	}
	return contract.FromDoc(doc)
}

// TestGeneratedUpToDate locks sigil.gen.go to fixture.sigil. Run
// `go test ./pkg/codegen/gogen/conformance -run UpToDate -update`
// after changing the generator or the fixture.
func TestGeneratedUpToDate(t *testing.T) {
	c := fixtureContract(t)
	want, err := gogen.Emit(c, "conformance", c.Hash())
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile("sigil.gen.go", []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile("sigil.gen.go")
	if err != nil {
		t.Fatalf("missing committed generated file (run with -update): %v", err)
	}
	if !bytes.Equal(got, []byte(want)) {
		t.Fatal("sigil.gen.go is stale — regenerate with `go test ./pkg/codegen/gogen/conformance -run UpToDate -update`")
	}
}

// impl is the test implementation; function fields let each test
// inject behavior. The `*C` fields take the context too, for the
// REQUEST 13 response-controls seam tests; when set they win over
// their context-less siblings.
type impl struct {
	listModes func() ([]string, error)
	setMode   func(SetModeRequest) (bool, error)
	setModeC  func(context.Context, SetModeRequest) (bool, error)
	login     func(LoginRequest) (LoginOutcome, error)
	chat      func(ChatRequest, *ChatStream) error
	chatC     func(context.Context, ChatRequest, *ChatStream) error
	tail      func(TailRequest, *TailStream) error
}

func (i impl) Login(_ context.Context, req LoginRequest) (LoginOutcome, error) {
	if i.login == nil {
		return NewLoginOutcomeIdle(), nil
	}
	return i.login(req)
}

func (i impl) ListModes(_ context.Context, _ ListModesRequest) ([]string, error) {
	if i.listModes == nil {
		return []string{"fast", "deep"}, nil
	}
	return i.listModes()
}
func (i impl) SetMode(ctx context.Context, req SetModeRequest) (bool, error) {
	if i.setModeC != nil {
		return i.setModeC(ctx, req)
	}
	if i.setMode == nil {
		return true, nil
	}
	return i.setMode(req)
}
func (i impl) Chat(ctx context.Context, req ChatRequest, s *ChatStream) error {
	if i.chatC != nil {
		return i.chatC(ctx, req, s)
	}
	if i.chat == nil {
		return nil
	}
	return i.chat(req, s)
}
func (i impl) Tail(_ context.Context, req TailRequest, s *TailStream) error {
	if i.tail == nil {
		return nil
	}
	return i.tail(req, s)
}

func server(t *testing.T, i impl, opts ...HandlerOption) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewApiHandler(i, opts...))
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	res, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestQueryHappyPath(t *testing.T) {
	srv := server(t, impl{})
	res := post(t, srv.URL+"/query/ListModes", `{}`)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var got []string
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "fast" {
		t.Errorf("body = %v", got)
	}
}

func TestMalformedBodyIs400(t *testing.T) {
	srv := server(t, impl{})
	res := post(t, srv.URL+"/query/ListModes", `{not json`)
	if res.StatusCode != 400 {
		t.Errorf("status = %d, want 400", res.StatusCode)
	}
}

func TestValidationFailureIs400(t *testing.T) {
	srv := server(t, impl{})
	res := post(t, srv.URL+"/command/SetMode", `{"mode":"frire"}`)
	if res.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
	if body := readAll(t, res.Body); !strings.Contains(body, "unknown variant") {
		t.Errorf("body = %q, want variant error", body)
	}
}

func TestUnaryImplErrorIs500(t *testing.T) {
	srv := server(t, impl{setMode: func(SetModeRequest) (bool, error) {
		return false, errors.New("boom")
	}})
	res := post(t, srv.URL+"/command/SetMode", `{"mode":"fast"}`)
	if res.StatusCode != 500 {
		t.Errorf("status = %d, want 500", res.StatusCode)
	}
}

func TestWrongMethodIs405(t *testing.T) {
	srv := server(t, impl{})
	res, err := http.Get(srv.URL + "/query/ListModes")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 405 {
		t.Errorf("status = %d, want 405", res.StatusCode)
	}
}

func TestMultiChannelStream(t *testing.T) {
	srv := server(t, impl{chat: func(req ChatRequest, s *ChatStream) error {
		if req.Prompt != "hi" {
			return fmt.Errorf("prompt = %q", req.Prompt)
		}
		if err := s.Thinking("th1"); err != nil {
			return err
		}
		if err := s.Thinking("th2"); err != nil {
			return err
		}
		return s.Answer("ans")
	}})
	res := post(t, srv.URL+"/stream/Chat", `{"prompt":"hi"}`)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("Content-Type = %q", ct)
	}
	var lines []map[string]string
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var obj map[string]string
		if err := json.Unmarshal(sc.Bytes(), &obj); err != nil {
			t.Fatalf("non-JSON line %q: %v", sc.Text(), err)
		}
		lines = append(lines, obj)
	}
	want := []struct{ ch, text string }{
		{"thinking", "th1"}, {"thinking", "th2"}, {"answer", "ans"},
	}
	if len(lines) != len(want) {
		t.Fatalf("lines = %v", lines)
	}
	for i, w := range want {
		if lines[i]["channel"] != w.ch || lines[i]["text"] != w.text {
			t.Errorf("line %d = %v, want %+v", i, lines[i], w)
		}
	}
}

func TestStreamErrorBeforeFirstDeltaIs502(t *testing.T) {
	srv := server(t, impl{chat: func(ChatRequest, *ChatStream) error {
		return errors.New("model unavailable")
	}})
	res := post(t, srv.URL+"/stream/Chat", `{"prompt":"hi"}`)
	if res.StatusCode != 502 {
		t.Fatalf("status = %d, want 502", res.StatusCode)
	}
	if body := readAll(t, res.Body); !strings.Contains(body, "model unavailable") {
		t.Errorf("body = %q", body)
	}
}

func TestStreamMidErrorEmitsReservedChannel(t *testing.T) {
	srv := server(t, impl{chat: func(_ ChatRequest, s *ChatStream) error {
		if err := s.Thinking("partial"); err != nil {
			return err
		}
		return errors.New("connection lost")
	}})
	res := post(t, srv.URL+"/stream/Chat", `{"prompt":"hi"}`)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d (status is committed at first delta)", res.StatusCode)
	}
	body := readAll(t, res.Body)
	if !strings.Contains(body, `"channel":"__error"`) || !strings.Contains(body, "connection lost") {
		t.Errorf("body = %q, want trailing __error line", body)
	}
}

func TestScalarStream(t *testing.T) {
	srv := server(t, impl{tail: func(req TailRequest, s *TailStream) error {
		if err := s.Send("a "); err != nil {
			return err
		}
		return s.Send("b")
	}})
	res := post(t, srv.URL+"/stream/Tail", `{"path":"/var/log"}`)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}
	if body := readAll(t, res.Body); body != "a b" {
		t.Errorf("body = %q", body)
	}
}

func TestScalarStreamErrorBeforeFirstDeltaIs502(t *testing.T) {
	srv := server(t, impl{tail: func(TailRequest, *TailStream) error {
		return errors.New("file missing")
	}})
	res := post(t, srv.URL+"/stream/Tail", `{"path":"/nope"}`)
	if res.StatusCode != 502 {
		t.Fatalf("status = %d, want 502", res.StatusCode)
	}
	if body := readAll(t, res.Body); !strings.Contains(body, "file missing") {
		t.Errorf("body = %q", body)
	}
}

// TestScalarStreamMidErrorAbortsUncleanly: a raw-text stream that
// fails after a delta must NOT close cleanly — a clean close writes
// the terminating chunk and makes truncation look like success. The
// handler panics http.ErrAbortHandler, so the client reads a partial
// body then an unexpected-EOF error (the signal that trips
// .failed/.error). httptest surfaces the abort as a read error on
// the body.
func TestScalarStreamMidErrorAbortsUncleanly(t *testing.T) {
	srv := server(t, impl{tail: func(_ TailRequest, s *TailStream) error {
		if err := s.Send("partial "); err != nil {
			return err
		}
		return errors.New("disk fell over")
	}})
	res := post(t, srv.URL+"/stream/Tail", `{"path":"/var/log"}`)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d (committed at first delta)", res.StatusCode)
	}
	// The error text must NOT leak into the raw stream, and the read
	// must end with an error (unexpected EOF), not a clean done.
	body, err := io.ReadAll(res.Body)
	if strings.Contains(string(body), "disk fell over") {
		t.Errorf("impl error text leaked into raw stream: %q", body)
	}
	if err == nil {
		t.Error("clean read of an aborted scalar stream — truncation would look like success; want a read error")
	}
}

func TestMaxRequestBytes(t *testing.T) {
	srv := server(t, impl{}, WithMaxRequestBytes(16))
	res := post(t, srv.URL+"/query/ListModes", `{"pad":"`+strings.Repeat("x", 64)+`"}`)
	if res.StatusCode != 400 {
		t.Errorf("status = %d, want 400 for over-cap body", res.StatusCode)
	}
}

func TestMiddlewareOutermostFirst(t *testing.T) {
	var order []string
	mw := func(tag string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, tag)
				next.ServeHTTP(w, r)
			})
		}
	}
	srv := server(t, impl{}, WithMiddleware(mw("outer")), WithMiddleware(mw("inner")))
	post(t, srv.URL+"/query/ListModes", `{}`)
	if len(order) != 2 || order[0] != "outer" || order[1] != "inner" {
		t.Errorf("order = %v", order)
	}
}

func TestContractHashMatchesFixture(t *testing.T) {
	c := fixtureContract(t)
	if ContractHash != c.Hash() {
		t.Errorf("committed ContractHash %s != fixture hash %s (regenerate)", ContractHash, c.Hash())
	}
}

// postWithHeaders POSTs a JSON body with extra request headers set —
// the seam tests need to plant request metadata the impl reads back.
func postWithHeaders(t *testing.T, url, body string, h http.Header) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, vs := range h {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

// TestResponseControlsCommand exercises REQUEST 13 on a command (the
// iris Login/Logout shape): the impl reads a request header and a
// request cookie through serve.ResponseFrom(ctx), then sets a
// response cookie — all over real HTTP through the generated handler.
func TestResponseControlsCommand(t *testing.T) {
	srv := server(t, impl{setModeC: func(ctx context.Context, req SetModeRequest) (bool, error) {
		rc := serve.ResponseFrom(ctx)
		if rc == nil {
			return false, errors.New("no response controls on ctx")
		}
		if got := rc.RequestHeader("Origin"); got != "https://app.example" {
			return false, fmt.Errorf("Origin = %q", got)
		}
		if c, err := rc.Cookie("prior"); err != nil || c.Value != "v1" {
			return false, fmt.Errorf("prior cookie = %v, %v", c, err)
		}
		rc.SetCookie(&http.Cookie{Name: "iris-session", Value: "id.secret", HttpOnly: true, Path: "/"})
		rc.SetHeader("X-Request-Id", "r-123")
		return true, nil
	}})
	res := postWithHeaders(t, srv.URL+"/command/SetMode", `{"mode":"fast"}`, http.Header{
		"Origin": {"https://app.example"},
		"Cookie": {"prior=v1"},
	})
	if res.StatusCode != 200 {
		t.Fatalf("status = %d: %s", res.StatusCode, readAll(t, res.Body))
	}
	if sc := res.Header.Get("Set-Cookie"); !strings.Contains(sc, "iris-session=id.secret") || !strings.Contains(sc, "HttpOnly") {
		t.Errorf("Set-Cookie = %q", sc)
	}
	if got := res.Header.Get("X-Request-Id"); got != "r-123" {
		t.Errorf("X-Request-Id = %q", got)
	}
}

// TestResponseControlsStreamBeforeFirstDelta: a stream impl can set a
// response cookie through the seam as long as it does so before the
// first delta (headers commit on first flush). Verifies the cookie
// lands AND the deltas still stream.
func TestResponseControlsStreamBeforeFirstDelta(t *testing.T) {
	srv := server(t, impl{chatC: func(ctx context.Context, req ChatRequest, s *ChatStream) error {
		if rc := serve.ResponseFrom(ctx); rc != nil {
			rc.SetCookie(&http.Cookie{Name: "stream-set", Value: "ok", Path: "/"})
		}
		return s.Answer("done")
	}})
	res := post(t, srv.URL+"/stream/Chat", `{"prompt":"hi"}`)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if sc := res.Header.Get("Set-Cookie"); !strings.Contains(sc, "stream-set=ok") {
		t.Errorf("Set-Cookie = %q (cookie set before first delta should commit)", sc)
	}
	if body := readAll(t, res.Body); !strings.Contains(body, `"text":"done"`) {
		t.Errorf("stream body = %q", body)
	}
}

// TestUnionReturnWire is the discriminated-union wire spec (L94): a
// command returning a union marshals to `{tag, value}` — the same
// shape the generated client cell holds, so a server-built variant
// lands straight in the matching `match` arm. Covers a record payload,
// a primitive payload, and a unit variant; and the impl's own decode
// (UnmarshalJSON) round-trips.
func TestUnionReturnWire(t *testing.T) {
	cases := []struct {
		name    string
		out     LoginOutcome
		wantTag string
		check   func(t *testing.T, value json.RawMessage)
	}{
		{"record payload", NewLoginOutcomeSuccess(User{Name: "Ada", Email: "ada@x"}), "success",
			func(t *testing.T, v json.RawMessage) {
				var u User
				if err := json.Unmarshal(v, &u); err != nil || u.Name != "Ada" || u.Email != "ada@x" {
					t.Errorf("value = %s (%v)", v, err)
				}
			}},
		{"primitive payload", NewLoginOutcomeInvalid("bad password"), "invalid",
			func(t *testing.T, v json.RawMessage) {
				var s string
				if err := json.Unmarshal(v, &s); err != nil || s != "bad password" {
					t.Errorf("value = %s (%v)", v, err)
				}
			}},
		{"unit variant omits value", NewLoginOutcomeLocked(), "locked",
			func(t *testing.T, v json.RawMessage) {
				if len(v) != 0 {
					t.Errorf("unit variant should omit value, got %s", v)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.out
			srv := server(t, impl{login: func(LoginRequest) (LoginOutcome, error) { return out, nil }})
			res := post(t, srv.URL+"/command/Login", `{"email":"a","password":"b"}`)
			if res.StatusCode != 200 {
				t.Fatalf("status = %d", res.StatusCode)
			}
			var w struct {
				Tag   string          `json:"tag"`
				Value json.RawMessage `json:"value"`
			}
			body := readAll(t, res.Body)
			if err := json.Unmarshal([]byte(body), &w); err != nil {
				t.Fatalf("response not {tag,value}: %s (%v)", body, err)
			}
			if w.Tag != tc.wantTag {
				t.Errorf("tag = %q, want %q", w.Tag, tc.wantTag)
			}
			tc.check(t, w.Value)

			// The generated UnmarshalJSON round-trips the same bytes back
			// into the tagged struct (the client decode path).
			var back LoginOutcome
			if err := json.Unmarshal([]byte(body), &back); err != nil {
				t.Fatalf("UnmarshalJSON: %v", err)
			}
			if back.Tag != tc.wantTag {
				t.Errorf("round-trip tag = %q, want %q", back.Tag, tc.wantTag)
			}
		})
	}
}

// TestUnionUnknownTagMarshalErrors locks the server-side safety: a
// union with an unset/garbage tag fails to marshal rather than
// emitting a malformed frame.
func TestUnionUnknownTagMarshalErrors(t *testing.T) {
	if _, err := json.Marshal(LoginOutcome{Tag: "bogus"}); err == nil {
		t.Error("marshalling an unknown union tag should error")
	}
}
