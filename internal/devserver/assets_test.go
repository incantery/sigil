package devserver

import (
	"strings"
	"testing"
)

func TestAgentJSEmbedded(t *testing.T) {
	for _, want := range []string{
		"window.__sigilDev",
		"new EventSource(\"/__sigil/events\")",
		"function hotSwap",
		"replaceChildren()",
		"__sigil_overlay",
	} {
		if !strings.Contains(AgentJS, want) {
			t.Errorf("agent.js missing %q", want)
		}
	}
}
