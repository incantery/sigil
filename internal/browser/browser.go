// Package browser drives a headless Chrome over CDP (control/capture/injection)
// and a localhost websocket to an in-page agent (the DOM hot path). A browser
// test's primitives call a Session; each call blocks on a round-trip and returns
// synchronously, so the Sigil/goja layer stays synchronous.
package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

type reply struct {
	ID    int    `json:"id"`
	OK    bool   `json:"ok"`
	Value string `json:"value"`
	Error string `json:"error"`
}

type intent struct {
	ID   int    `json:"id"`
	Op   string `json:"op"`
	Sel  string `json:"sel,omitempty"`
	Text string `json:"text,omitempty"`
	Ms   int    `json:"ms,omitempty"`
}

// Session is one driven browser.
type Session struct {
	allocCancel context.CancelFunc
	ctxCancel   context.CancelFunc
	ctx         context.Context

	httpSrv *http.Server
	wsURL   string

	mu      sync.Mutex
	conn    net.Conn      // current agent connection
	nextID  int
	pending map[int]chan reply
	ready   chan struct{} // closed when an agent says hello
}

// New launches headless Chrome and the agent websocket server. It returns an
// error (not a panic) when Chrome is unavailable.
func New() (*Session, error) {
	s := &Session{pending: map[int]chan reply{}, ready: make(chan struct{})}

	// 1. websocket server on an ephemeral localhost port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.wsURL = "ws://" + ln.Addr().String() + "/agent"
	mux := http.NewServeMux()
	mux.HandleFunc("/agent", s.handleWS)
	s.httpSrv = &http.Server{Handler: mux}
	go s.httpSrv.Serve(ln) //nolint:errcheck

	// 2. headless Chrome.
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(),
		chromedp.DefaultExecAllocatorOptions[:]...)
	ctx, ctxCancel := chromedp.NewContext(allocCtx)
	s.allocCancel, s.ctxCancel, s.ctx = allocCancel, ctxCancel, ctx

	// 3. inject the agent + the ws URL into every document, and bypass CSP so
	//    the agent's websocket isn't blocked by a target page.
	script := "window.__SIGIL_WS_URL__ = " + jsonString(s.wsURL) + ";\n" + agentJS
	if err := chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			if err := page.SetBypassCSP(true).Do(ctx); err != nil {
				return err
			}
			_, err := page.AddScriptToEvaluateOnNewDocument(script).Do(ctx)
			return err
		}),
	); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }

// handleWS upgrades the agent connection and reads replies/hellos until close.
func (s *Session) handleWS(w http.ResponseWriter, r *http.Request) {
	// UpgradeHTTP returns (net.Conn, *bufio.ReadWriter, ws.Handshake, error).
	// We use the net.Conn directly; ReadClientText/WriteServerText accept io.ReadWriter.
	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		return
	}
	s.mu.Lock()
	var oldConn net.Conn
	if s.conn != nil {
		oldConn = s.conn
	}
	s.conn = conn
	s.mu.Unlock()
	if oldConn != nil {
		oldConn.Close() //nolint:errcheck
	}
	for {
		data, err := wsutil.ReadClientText(conn)
		if err != nil {
			return
		}
		// hello?
		var probe map[string]json.RawMessage
		if json.Unmarshal(data, &probe) == nil {
			if _, ok := probe["hello"]; ok {
				s.mu.Lock()
				select {
				case <-s.ready:
				default:
					close(s.ready)
				}
				s.mu.Unlock()
				continue
			}
		}
		var rep reply
		if json.Unmarshal(data, &rep) != nil {
			continue
		}
		s.mu.Lock()
		ch := s.pending[rep.ID]
		delete(s.pending, rep.ID)
		s.mu.Unlock()
		if ch != nil {
			ch <- rep
		}
	}
}

// send issues an intent and blocks for its reply (or a timeout).
func (s *Session) send(it intent) (reply, error) {
	s.mu.Lock()
	s.nextID++
	it.ID = s.nextID
	ch := make(chan reply, 1)
	s.pending[it.ID] = ch
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return reply{}, fmt.Errorf("agent not connected")
	}
	b, _ := json.Marshal(it)
	if err := wsutil.WriteServerText(conn, b); err != nil {
		return reply{}, err
	}
	select {
	case rep := <-ch:
		if !rep.OK {
			return rep, fmt.Errorf("%s", rep.Error)
		}
		return rep, nil
	case <-time.After(15 * time.Second):
		s.mu.Lock()
		delete(s.pending, it.ID)
		s.mu.Unlock()
		return reply{}, fmt.Errorf("intent %s timed out", it.Op)
	}
}

// Navigate goes to url (a CDP op) and waits for the re-injected agent to redial.
func (s *Session) Navigate(url string) error {
	s.mu.Lock()
	s.ready = make(chan struct{}) // reset readiness for the new document
	ready := s.ready
	s.conn = nil
	s.mu.Unlock()
	if err := chromedp.Run(s.ctx, chromedp.Navigate(url)); err != nil {
		return err
	}
	select {
	case <-ready:
		return nil
	case <-time.After(15 * time.Second):
		return fmt.Errorf("agent did not connect after navigating to %s", url)
	}
}

// DomText returns the textContent of the first element matching sel.
func (s *Session) DomText(sel string) (string, error) {
	rep, err := s.send(intent{Op: "domText", Sel: sel})
	if err != nil {
		return "", err
	}
	return rep.Value, nil
}

// Close tears down Chrome and the ws server.
func (s *Session) Close() error {
	if s.ctxCancel != nil {
		s.ctxCancel()
	}
	if s.allocCancel != nil {
		s.allocCancel()
	}
	if s.httpSrv != nil {
		s.httpSrv.Close()
	}
	return nil
}
