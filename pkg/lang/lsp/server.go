package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"unicode/utf16"
)

// Server is one LSP session over a reader/writer pair (stdio in
// production, in-memory pipes in tests). One server serves one editor;
// state is the set of open documents.
type Server struct {
	in  *bufio.Reader
	out io.Writer
	log io.Writer // optional protocol trace target (stderr)

	mu   sync.Mutex
	docs map[string]*document // keyed by URI

	version string // sigil version string for serverInfo

	shutdown bool
}

// document is one open editor buffer plus everything derived from it.
type document struct {
	uri     string
	path    string // filesystem path ("" when the URI isn't file://)
	version int
	text    string
	lines   []string
	an      *analysis // parse + lower results; never nil after update
}

// NewServer wires a server to its transport. log receives one-line
// protocol traces when non-nil (the CLI passes stderr under --verbose).
func NewServer(in io.Reader, out io.Writer, log io.Writer, version string) *Server {
	return &Server{
		in:      bufio.NewReader(in),
		out:     out,
		log:     log,
		docs:    map[string]*document{},
		version: version,
	}
}

// Run serves until the client disconnects or sends exit.
func (s *Server) Run() error {
	for {
		body, err := readMessage(s.in)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			s.trace("bad message: %v", err)
			continue
		}
		if req.Method == "exit" {
			return nil
		}
		s.handle(&req)
	}
}

func (s *Server) trace(format string, args ...any) {
	if s.log != nil {
		fmt.Fprintf(s.log, "sigil lsp: "+format+"\n", args...)
	}
}

func (s *Server) reply(id *json.RawMessage, result any) {
	if id == nil {
		return // notifications get no response
	}
	s.send(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) replyErr(id *json.RawMessage, code int, msg string) {
	if id == nil {
		return
	}
	s.send(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *Server) notify(method string, params any) {
	s.send(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
}

var writeMu sync.Mutex

func (s *Server) send(v any) {
	writeMu.Lock()
	defer writeMu.Unlock()
	if err := writeMessage(s.out, v); err != nil {
		s.trace("write: %v", err)
	}
}

// handle dispatches one request/notification. A panic in a handler is
// converted into an error response so one bad request can't take the
// whole session down with it.
func (s *Server) handle(req *rpcRequest) {
	defer func() {
		if r := recover(); r != nil {
			s.trace("panic in %s: %v\n%s", req.Method, r, debug.Stack())
			s.replyErr(req.ID, codeInvalidParams, fmt.Sprintf("internal error: %v", r))
		}
	}()

	switch req.Method {
	case "initialize":
		s.reply(req.ID, initializeResult{
			Capabilities: serverCapabilities{
				TextDocumentSync: 1, // full document sync
				SemanticTokensProvider: semanticTokensOpts{
					Legend: semanticTokensLegend{
						TokenTypes:     tokenTypeNames,
						TokenModifiers: tokenModifierNames,
					},
					Full: true,
				},
				DocumentSymbolProvider: true,
				DefinitionProvider:     true,
				HoverProvider:          true,
			},
			ServerInfo: serverInfo{Name: "sigil", Version: s.version},
		})

	case "initialized", "workspace/didChangeConfiguration", "$/cancelRequest",
		"$/setTrace", "textDocument/didSave", "workspace/didChangeWatchedFiles":
		// Notifications we accept and don't act on.

	case "shutdown":
		s.shutdown = true
		s.reply(req.ID, nil)

	case "textDocument/didOpen":
		var p didOpenParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return
		}
		s.updateDoc(p.TextDocument.URI, p.TextDocument.Version, p.TextDocument.Text)

	case "textDocument/didChange":
		var p didChangeParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return
		}
		if len(p.ContentChanges) == 0 {
			return
		}
		// Full sync: the last change carries the whole document.
		s.updateDoc(p.TextDocument.URI, p.TextDocument.Version,
			p.ContentChanges[len(p.ContentChanges)-1].Text)

	case "textDocument/didClose":
		var p didCloseParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return
		}
		s.mu.Lock()
		delete(s.docs, p.TextDocument.URI)
		s.mu.Unlock()
		// Clear stale squiggles for the closed buffer.
		s.notify("textDocument/publishDiagnostics",
			publishDiagnosticsParams{URI: p.TextDocument.URI, Diagnostics: []Diagnostic{}})

	case "textDocument/semanticTokens/full":
		var p docParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			s.replyErr(req.ID, codeInvalidParams, err.Error())
			return
		}
		doc := s.doc(p.TextDocument.URI)
		if doc == nil {
			s.reply(req.ID, semanticTokens{Data: []int{}})
			return
		}
		s.reply(req.ID, semanticTokens{Data: encodeTokens(doc)})

	case "textDocument/documentSymbol":
		var p docParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			s.replyErr(req.ID, codeInvalidParams, err.Error())
			return
		}
		doc := s.doc(p.TextDocument.URI)
		if doc == nil {
			s.reply(req.ID, []documentSymbol{})
			return
		}
		s.reply(req.ID, documentSymbols(doc))

	case "textDocument/definition":
		var p docPositionParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			s.replyErr(req.ID, codeInvalidParams, err.Error())
			return
		}
		doc := s.doc(p.TextDocument.URI)
		if doc == nil {
			s.reply(req.ID, nil)
			return
		}
		s.reply(req.ID, definition(doc, p.Position))

	case "textDocument/hover":
		var p docPositionParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			s.replyErr(req.ID, codeInvalidParams, err.Error())
			return
		}
		doc := s.doc(p.TextDocument.URI)
		if doc == nil {
			s.reply(req.ID, nil)
			return
		}
		s.reply(req.ID, hoverAt(doc, p.Position))

	default:
		if req.ID != nil {
			s.replyErr(req.ID, codeMethodNotFound, "unsupported method "+req.Method)
		}
	}
}

func (s *Server) doc(uri string) *document {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs[uri]
}

// updateDoc replaces a document's content, re-analyzes, and pushes
// fresh diagnostics.
func (s *Server) updateDoc(uri string, version int, text string) {
	doc := &document{
		uri:     uri,
		path:    uriToPath(uri),
		version: version,
		text:    text,
		lines:   strings.Split(text, "\n"),
	}
	doc.an = analyze(doc)

	s.mu.Lock()
	s.docs[uri] = doc
	s.mu.Unlock()

	s.notify("textDocument/publishDiagnostics", publishDiagnosticsParams{
		URI:         uri,
		Diagnostics: doc.an.diagnostics(doc),
	})
}

// uriToPath converts a file:// URI to a filesystem path. Non-file URIs
// (untitled buffers) return "".
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	p, err := url.PathUnescape(u.Path)
	if err != nil {
		p = u.Path
	}
	// Windows drive-letter paths arrive as /C:/…; not a target platform
	// today, but normalize anyway.
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

// ───────────── position conversion ───────────────────────────────────

// utf16Col converts a 1-based byte column on line (parser coords) to a
// 0-based UTF-16 character offset (LSP coords).
func utf16Col(line string, byteCol int) int {
	if byteCol <= 1 {
		return 0
	}
	end := byteCol - 1
	if end > len(line) {
		end = len(line)
	}
	n := 0
	for _, r := range line[:end] {
		n += utf16.RuneLen(r)
	}
	return n
}

// byteCol converts a 0-based UTF-16 character offset (LSP coords) on
// line to a 1-based byte column (parser coords).
func byteCol(line string, utf16Off int) int {
	n := 0
	for i, r := range line {
		if n >= utf16Off {
			return i + 1
		}
		n += utf16.RuneLen(r)
	}
	return len(line) + 1
}

// protoRange builds an LSP Range from parser coords: 1-based line,
// 1-based byte start column, and a byte length on that single line.
func protoRange(lines []string, line, col, length int) Range {
	l := line - 1
	if l < 0 {
		l = 0
	}
	var src string
	if l < len(lines) {
		src = lines[l]
	}
	start := utf16Col(src, col)
	end := utf16Col(src, col+length)
	return Range{
		Start: Position{Line: l, Character: start},
		End:   Position{Line: l, Character: end},
	}
}
