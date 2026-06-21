package lsp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
)

// Server is a sigil LSP server speaking JSON-RPC over a Conn.
type Server struct {
	conn        *Conn
	root        string // workspace root = load Root (where std/ lives)
	gotShutdown bool
	docs        *docStore
}

func NewServer(r io.Reader, w io.Writer) *Server {
	return &Server{conn: NewConn(r, w), docs: newDocStore()}
}

// Run reads and dispatches messages until exit (or stream EOF).
func (s *Server) Run() error {
	for {
		msg, err := s.conn.Read()
		if err != nil {
			return nil // EOF or broken pipe: stop cleanly
		}
		if stop := s.dispatch(msg); stop {
			if !s.gotShutdown {
				// LSP: an exit without a prior shutdown is abnormal termination.
				return fmt.Errorf("exit received without shutdown")
			}
			return nil
		}
	}
}

// dispatch handles one message; returns true to stop the run loop (on exit).
func (s *Server) dispatch(msg *Message) (stop bool) {
	switch msg.Method {
	case "initialize":
		var p InitializeParams
		_ = json.Unmarshal(msg.Params, &p)
		s.root = s.resolveRoot(p)
		_ = s.conn.Reply(msg.ID, InitializeResult{Capabilities: ServerCapabilities{
			TextDocumentSync:       TextDocumentSyncFull,
			DocumentSymbolProvider: true,
		}})
	case "initialized":
		// no-op
	case "shutdown":
		s.gotShutdown = true
		_ = s.conn.Reply(msg.ID, nil)
	case "exit":
		return true
	case "textDocument/didOpen":
		var p DidOpenParams
		_ = json.Unmarshal(msg.Params, &p)
		s.docs.set(p.TextDocument.URI, p.TextDocument.Text)
		s.publishDiagnostics(p.TextDocument.URI)
	case "textDocument/didChange":
		var p DidChangeParams
		_ = json.Unmarshal(msg.Params, &p)
		if n := len(p.ContentChanges); n > 0 {
			s.docs.set(p.TextDocument.URI, p.ContentChanges[n-1].Text) // full sync: last wins
		}
		s.publishDiagnostics(p.TextDocument.URI)
	case "textDocument/didSave":
		var p DidSaveParams
		_ = json.Unmarshal(msg.Params, &p)
		s.publishDiagnostics(p.TextDocument.URI)
	case "textDocument/didClose":
		var p DidCloseParams
		_ = json.Unmarshal(msg.Params, &p)
		s.docs.remove(p.TextDocument.URI)
		_ = s.conn.Notify("textDocument/publishDiagnostics", PublishDiagnosticsParams{URI: p.TextDocument.URI, Diagnostics: []Diagnostic{}})
	case "textDocument/documentSymbol":
		var p DocumentSymbolParams
		_ = json.Unmarshal(msg.Params, &p)
		text, _ := s.docs.get(p.TextDocument.URI)
		_ = s.conn.Reply(msg.ID, documentSymbols(text))
	default:
		if !msg.IsNotification() {
			_ = s.conn.ReplyError(msg.ID, CodeMethodNotFound, "method not found: "+msg.Method)
		}
	}
	return false
}

// publishDiagnostics analyzes the open document at uri and sends its diagnostics.
func (s *Server) publishDiagnostics(uri string) {
	text, ok := s.docs.get(uri)
	if !ok {
		return
	}
	path := uriToPath(uri)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	root := s.root
	if root == "" {
		root = filepath.Dir(path)
	}
	err := analyze(path, root, s.docs.overlay())
	_ = s.conn.Notify("textDocument/publishDiagnostics", PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnosticsFor(err, text),
	})
}

// resolveRoot picks the load Root: rootUri, else first workspace folder.
func (s *Server) resolveRoot(p InitializeParams) string {
	if p.RootURI != "" {
		return uriToPath(p.RootURI)
	}
	if len(p.WorkspaceFolders) > 0 {
		return uriToPath(p.WorkspaceFolders[0].URI)
	}
	return "."
}

// uriToPath converts a file:// URI to a filesystem path (percent-decoded).
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	return u.Path
}
