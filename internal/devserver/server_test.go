package devserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func okBuild(entry, root string) (string, error) {
	return `window.__built = "yes";`, nil
}

func TestShellServesAgentAndBundle(t *testing.T) {
	s := New("entry.sigil", ".", okBuild)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// Shell page references the agent and inlines the initial bundle.
	body := get(t, srv.URL+"/")
	if !strings.Contains(body, `<script src="/__sigil/agent.js"></script>`) {
		t.Error("shell missing agent script tag")
	}
	if !strings.Contains(body, `window.__built = "yes";`) {
		t.Error("shell missing inlined initial bundle")
	}
	if !strings.Contains(body, `id="app"`) {
		t.Error("shell missing #app mount node")
	}

	if strings.Index(body, `<script src="/__sigil/agent.js"></script>`) >= strings.Index(body, `window.__built`) {
		t.Error("agent script must appear before the inline bundle")
	}

	// Agent asset is served as JS.
	agent := get(t, srv.URL+"/__sigil/agent.js")
	if !strings.Contains(agent, "window.__sigilDev") {
		t.Error("agent.js not served")
	}
}

func TestRebuildBroadcastsReload(t *testing.T) {
	s := New("entry.sigil", ".", okBuild)
	ch, cancel := s.Hub().Subscribe()
	defer cancel()
	s.Rebuild()
	got := <-ch
	if !strings.Contains(got, `"type":"reload"`) || !strings.Contains(got, "__built") {
		t.Errorf("rebuild did not broadcast a reload: %s", got)
	}
}

func TestRebuildBroadcastsErrorOnBadBuild(t *testing.T) {
	bad := func(entry, root string) (string, error) { return "", io.EOF }
	s := New("entry.sigil", ".", bad)
	ch, cancel := s.Hub().Subscribe()
	defer cancel()
	s.Rebuild()
	got := <-ch
	if !strings.Contains(got, `"type":"error"`) {
		t.Errorf("bad build did not broadcast an error: %s", got)
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
