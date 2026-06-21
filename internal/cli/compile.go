package cli

import (
	"fmt"

	"github.com/incantery/sigil/internal/load"
)

// shell is the minimal HTML page that hosts a sigil bundle.
const shell = `<!doctype html>
<html>
  <head><meta charset="utf-8"><title>%s</title></head>
  <body>
    <div id="app"></div>
    <script>%s</script>
  </body>
</html>`

// htmlPage wraps a JS bundle in the host page shell.
func htmlPage(title, js string) string {
	return fmt.Sprintf(shell, title, js)
}

// bundle type-checks the entry module against the standard library under root
// and returns the linked, npm-free JS bundle.
func bundle(entry, root string) (string, error) {
	prog, err := load.Load(entry, load.Options{Root: root})
	if err != nil {
		return "", err
	}
	return prog.Bundle()
}
