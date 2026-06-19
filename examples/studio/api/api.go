package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// agentsMu guards mockAgents. The query and command handlers run on
// separate goroutines (one per request), and the command handlers
// mutate the slice, so reads and writes are serialized.
var agentsMu sync.Mutex

type Agent struct {
	Name    string `json:"name"`
	Model   string `json:"model"`
	Tools   int    `json:"tools"`
	Runs    int    `json:"runs"`
	Latency string `json:"latency"`
	Success string `json:"success"`
	Status  string `json:"status"`
}

type Run struct {
	ID      string `json:"id"`
	Agent   string `json:"agent"`
	Status  string `json:"status"`
	Model   string `json:"model"`
	Tokens  int    `json:"tokens"`
	Cost    string `json:"cost"`
	Latency string `json:"latency"`
	Summary string `json:"summary"`
	Time    string `json:"time"`
}

type TimelineEvent struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Detail string `json:"detail"`
	AtMs   int    `json:"at_ms"`
}

var mockAgents = []Agent{
	{Name: "recruiter-screener", Model: "claude-sonnet-4.5", Tools: 4, Runs: 1247, Latency: "2.3s", Success: "91%", Status: "active"},
	{Name: "code-reviewer", Model: "claude-sonnet-4.5", Tools: 6, Runs: 3421, Latency: "8.7s", Success: "94%", Status: "active"},
	{Name: "research-assistant", Model: "claude-opus-4.5", Tools: 3, Runs: 487, Latency: "12s", Success: "89%", Status: "active"},
	{Name: "summarizer-v2", Model: "claude-haiku-4.5", Tools: 1, Runs: 18432, Latency: "1.4s", Success: "97%", Status: "active"},
	{Name: "blog-writer", Model: "gpt-4o", Tools: 3, Runs: 89, Latency: "6.2s", Success: "85%", Status: "draft"},
	{Name: "interview-coach", Model: "claude-sonnet-4.5", Tools: 2, Runs: 156, Latency: "4.1s", Success: "92%", Status: "active"},
	{Name: "support-triage", Model: "claude-haiku-4.5", Tools: 4, Runs: 8921, Latency: "0.9s", Success: "96%", Status: "active"},
	{Name: "planner-strategic", Model: "claude-opus-4.5", Tools: 5, Runs: 14, Latency: "18s", Success: "78%", Status: "draft"},
	{Name: "doc-extractor", Model: "gemini-2.0-flash", Tools: 2, Runs: 5873, Latency: "1.1s", Success: "99%", Status: "active"},
	{Name: "qa-tester-e2e", Model: "claude-sonnet-4.5", Tools: 5, Runs: 234, Latency: "9.8s", Success: "88%", Status: "active"},
}

var mockRuns = []Run{
	{ID: "run_a4f9c2", Agent: "code-reviewer", Status: "success", Model: "claude-sonnet-4.5", Tokens: 4821, Cost: "$0.41", Latency: "8.7s", Summary: "Review PR #2847 — refactor auth middleware", Time: "2m ago"},
	{ID: "run_c1a992", Agent: "code-reviewer", Status: "success", Model: "claude-sonnet-4.5", Tokens: 3104, Cost: "$0.28", Latency: "6.1s", Summary: "Review PR #2845 — billing dispute", Time: "8m ago"},
	{ID: "run_f7d2e6", Agent: "code-reviewer", Status: "failed", Model: "claude-sonnet-4.5", Tokens: 892, Cost: "$0.08", Latency: "2.1s", Summary: "Review PR #2845 — timeout", Time: "15m ago"},
	{ID: "run_b8c3d1", Agent: "support-triage", Status: "success", Model: "claude-haiku-4.5", Tokens: 1247, Cost: "$0.02", Latency: "0.8s", Summary: "Classify and route inbound support ticket", Time: "18m ago"},
	{ID: "run_e5f4a3", Agent: "recruiter-screener", Status: "success", Model: "claude-sonnet-4.5", Tokens: 2891, Cost: "$0.24", Latency: "2.3s", Summary: "Screen inbound candidate resume", Time: "22m ago"},
	{ID: "run_d9e7b2", Agent: "doc-extractor", Status: "success", Model: "gemini-2.0-flash", Tokens: 5104, Cost: "$0.05", Latency: "1.1s", Summary: "Extract structured data from PDF invoice", Time: "31m ago"},
	{ID: "run_a2b1c4", Agent: "research-assistant", Status: "success", Model: "claude-opus-4.5", Tokens: 8921, Cost: "$1.12", Latency: "14s", Summary: "Deep research on competitive landscape", Time: "45m ago"},
	{ID: "run_f3c2d5", Agent: "summarizer-v2", Status: "success", Model: "claude-haiku-4.5", Tokens: 1891, Cost: "$0.03", Latency: "1.4s", Summary: "Q3 board meeting minutes — 47 pages", Time: "52m ago"},
}

var mockTimeline = []TimelineEvent{
	{Kind: "system", Label: "System prompt composed", Detail: "2,184 tokens", AtMs: 0},
	{Kind: "model", Label: "Model request", Detail: "claude-sonnet-4.5", AtMs: 12},
	{Kind: "tool", Label: "github.get_diff", Detail: "auth/middleware.ts +47 -12", AtMs: 1840},
	{Kind: "model", Label: "Model response (partial)", Detail: "analyzing changes...", AtMs: 3200},
	{Kind: "tool", Label: "grep", Detail: "session-based verification", AtMs: 4100},
	{Kind: "tool", Label: "fs.read", Detail: "auth/middleware.test.ts", AtMs: 4800},
	{Kind: "model", Label: "Model response (partial)", Detail: "reviewing test coverage...", AtMs: 5900},
	{Kind: "tool", Label: "github.post_review", Detail: "3 comments, 1 suggestion", AtMs: 7200},
	{Kind: "model", Label: "Final response", Detail: "Review complete", AtMs: 8400},
}

type Prompt struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Tokens   int    `json:"tokens"`
	UsedBy   string `json:"usedBy"`
	Versions int    `json:"versions"`
	Updated  string `json:"updated"`
}

var mockPrompts = []Prompt{
	{Name: "code-review/system", Kind: "system", Tokens: 2184, UsedBy: "code-reviewer", Versions: 8, Updated: "2d ago"},
	{Name: "screening-criteria-v3", Kind: "system", Tokens: 1420, UsedBy: "recruiter-screener", Versions: 3, Updated: "5d ago"},
	{Name: "compress", Kind: "user", Tokens: 312, UsedBy: "summarizer-v2", Versions: 2, Updated: "1w ago"},
	{Name: "DAN-decompose", Kind: "system", Tokens: 890, UsedBy: "research-assistant", Versions: 4, Updated: "3d ago"},
	{Name: "ticket-triage", Kind: "system", Tokens: 1102, UsedBy: "support-triage", Versions: 6, Updated: "12h ago"},
	{Name: "decompose", Kind: "system", Tokens: 2401, UsedBy: "planner-strategic", Versions: 2, Updated: "2w ago"},
	{Name: "doc-extraction", Kind: "system", Tokens: 678, UsedBy: "doc-extractor", Versions: 5, Updated: "4d ago"},
	{Name: "staff-eng-rubric", Kind: "system", Tokens: 1876, UsedBy: "interview-coach", Versions: 3, Updated: "1w ago"},
	{Name: "contracts", Kind: "user", Tokens: 4170, UsedBy: "doc-extractor", Versions: 1, Updated: "3w ago"},
}

type Provider struct {
	Name    string `json:"name"`
	Models  int    `json:"models"`
	Status  string `json:"status"`
	Spend   string `json:"spend"`
	LastUse string `json:"lastUse"`
}

type RoutingRule struct {
	Pattern string `json:"pattern"`
	Target  string `json:"target"`
}

var mockProviders = []Provider{
	{Name: "Anthropic", Models: 3, Status: "connected", Spend: "$847.22", LastUse: "2m ago"},
	{Name: "OpenAI", Models: 3, Status: "connected", Spend: "$14.86", LastUse: "8m ago"},
	{Name: "Google AI", Models: 1, Status: "connected", Spend: "$2.10", LastUse: "45m ago"},
	{Name: "Ollama", Models: 4, Status: "connected", Spend: "$0.00", LastUse: "12m ago"},
	{Name: "Groq", Models: 2, Status: "connected", Spend: "$0.44", LastUse: "1h ago"},
}

var mockRoutes = []RoutingRule{
	{Pattern: "agent.tags includes \"production\"", Target: "anthropic/claude-sonnet-4.5"},
	{Pattern: "agent.tags includes \"local\"", Target: "local/open-2.5-32b"},
	{Pattern: "estimated_tokens > 180k", Target: "google/gemini-2.0-flash"},
	{Pattern: "agent == \"summarizer-v2\"", Target: "anthropic/claude-haiku-4.5"},
	{Pattern: "* (fallback)", Target: "anthropic/claude-sonnet-4.5"},
}

func Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /query/ListAgents", func(w http.ResponseWriter, r *http.Request) {
		agentsMu.Lock()
		defer agentsMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockAgents)
	})

	// CreateAgent appends a new draft agent. The Sigil source declares
	// `command CreateAgent -> name -> model = Bool`, so the request body
	// is {"name": ..., "model": ...} and the response is a bare `true`.
	mux.HandleFunc("POST /command/CreateAgent", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if in.Name == "" {
			in.Name = "untitled-agent"
		}
		if in.Model == "" {
			in.Model = "claude-sonnet-4.5"
		}
		agentsMu.Lock()
		mockAgents = append(mockAgents, Agent{
			Name: in.Name, Model: in.Model, Tools: 0, Runs: 0,
			Latency: "—", Success: "—", Status: "draft",
		})
		agentsMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(true)
	})

	// ArchiveAgent removes the named agent. Body is {"name": ...}.
	mux.HandleFunc("POST /command/ArchiveAgent", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		agentsMu.Lock()
		kept := mockAgents[:0]
		for _, a := range mockAgents {
			if a.Name != in.Name {
				kept = append(kept, a)
			}
		}
		mockAgents = kept
		agentsMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(true)
	})

	mux.HandleFunc("POST /query/ListRuns", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockRuns)
	})

	mux.HandleFunc("POST /query/GetTimeline", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockTimeline)
	})

	mux.HandleFunc("POST /query/ListPrompts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockPrompts)
	})

	mux.HandleFunc("POST /query/ListProviders", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockProviders)
	})

	mux.HandleFunc("POST /query/ListRoutes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockRoutes)
	})

	mux.HandleFunc("POST /query/GetStats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode("847 runs | $24.18 | 3.4s avg | 94.7%")
	})

	// The assistant stream. `stream Assist -> prompt = String` means the
	// body is {"prompt": ...} and the response is a stream of String
	// deltas. We pick a canned, Studio-domain reply by keyword and drip
	// it out one whitespace token at a time so the client's transcript
	// fills in live. Stateless — no server mutation, so it's idempotent.
	mux.HandleFunc("POST /stream/Assist", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")

		// A beat of dead air before the first token — the reasoning pause
		// a real model has. This is the window `Assist.pending` exists
		// for: the UI shows "thinking…" instead of looking frozen.
		time.Sleep(400 * time.Millisecond)

		reply := assistantReply(in.Prompt)
		for i, tok := range strings.Fields(reply) {
			if i > 0 {
				tok = " " + tok
			}
			if _, err := w.Write([]byte(tok)); err != nil {
				return
			}
			flusher.Flush()
			time.Sleep(40 * time.Millisecond)
		}
	})
}

// assistantReply returns a canned, domain-aware response for the mock
// Studio assistant, chosen by keyword. A real build would swap this for
// an LLM behind the same `stream Assist` contract — the frontend
// wouldn't change.
func assistantReply(prompt string) string {
	p := strings.ToLower(prompt)
	switch {
	case strings.Contains(p, "agent"):
		return "You have 10 agents configured. code-reviewer is the busiest with 3421 runs, and summarizer-v2 has the highest success rate at 97%."
	case strings.Contains(p, "run"):
		return "There have been 847 runs today at a total cost of $24.18, averaging 3.4s latency with a 94.7% success rate."
	case strings.Contains(p, "prompt"):
		return "You have 9 prompts. code-review/system is the largest at 2,184 tokens across 8 versions."
	case strings.Contains(p, "cost"), strings.Contains(p, "spend"):
		return "Total spend today is $24.18. Anthropic accounts for $847.22 lifetime, the largest of your providers."
	default:
		return "I'm the Sigil Studio assistant. Ask me about your agents, runs, prompts, or costs."
	}
}
