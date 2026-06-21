package lsp

import "sync"

// docStore holds the text of open documents, keyed by LSP URI.
type docStore struct {
	mu   sync.RWMutex
	docs map[string]string // uri -> text
}

func newDocStore() *docStore { return &docStore{docs: map[string]string{}} }

func (d *docStore) set(uri, text string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.docs[uri] = text
}

func (d *docStore) get(uri string) (string, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	t, ok := d.docs[uri]
	return t, ok
}

func (d *docStore) remove(uri string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.docs, uri)
}

// overlay returns a load.Options.Overlay: filesystem path -> text for every
// open doc, so the type checker sees unsaved buffers.
func (d *docStore) overlay() map[string]string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	ov := make(map[string]string, len(d.docs))
	for uri, text := range d.docs {
		ov[uriToPath(uri)] = text
	}
	return ov
}
