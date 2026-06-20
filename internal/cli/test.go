package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/incantery/sigil/pkg/ir"
	"github.com/incantery/sigil/pkg/render/html"
)

var (
	testJSON      bool
	testTimeout   time.Duration
	testHeaded    bool
	testTarget    string
	testSlowmo    time.Duration
	testHold      time.Duration
	testTraceFile string
	testOTLP      string
	testTraceLean bool
)

var testCmd = &cobra.Command{
	Use:   "test <file.sigil>",
	Short: "Run Sigil-authored tests against a real browser",
	Long: `Compiles a .sigil source, then runs each declared test in headless
Chromium via the Chrome DevTools Protocol. Each test spins up its own
isolated HTTP server + browser context — there is no shared state across
tests.

Tests are declared in source:

  test "+ increments count" = scenario Counter
    click button "+"
    expect-cell count 1
    expect-text "1"

Reporting: one line per test (✓ / ✗) by default, plus a final summary.
With --json, emit a structured per-test record on stdout for LLM
consumption. Exits nonzero on any failure.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		doc, err := compileFile(path)
		if err != nil {
			return err
		}
		if len(doc.Tests) == 0 {
			fmt.Fprintf(os.Stderr, "sigil: no `test` declarations in %s\n", path)
			return ErrSilent
		}

		// Reverse the doc's id→name cell map so expect-cell can address
		// cells by their source name. Multiple ids per name shouldn't
		// happen for top-level cells; for structured rows the runtime
		// uses `<row>.<field>` ids which we don't index here (v0).
		nameToID := make(map[string]string, len(doc.CellNames))
		for id, name := range doc.CellNames {
			nameToID[name] = id
		}

		// Index apps for O(1) lookup when dispatching scenario-in-app tests.
		appByName := make(map[string]ir.App, len(doc.Apps))
		for _, a := range doc.Apps {
			appByName[a.Name] = a
		}

		// Pick the rendering mode for app-target tests. JSON wins (CI /
		// LLM consumption); else if stdout is a TTY, launch the TUI; else
		// streaming text (works in CI logs, pipes, redirected files).
		// Headed mode + TUI is fine — Chrome opens in its own window
		// independent of the terminal.
		mode := pickRenderMode()

		// Tracing: when --trace or --otlp is set, a recorder observes every
		// event (regardless of render mode) and builds one OTLP trace per
		// scenario. Composed into runOne so the TUI/stream/json paths all
		// feed it.
		var rec *traceRecorder
		if testTraceFile != "" || testOTLP != "" {
			rec = newTraceRecorder("sigil-test", testTraceLean)
		}

		runOne := func(t ir.Test, onEvent func(e2eEvent)) testResult {
			if rec != nil {
				userSink := onEvent
				onEvent = func(e e2eEvent) {
					rec.observe(t, e)
					if userSink != nil {
						userSink(e)
					}
				}
			}
			// Headed runs hold the page open briefly so a watcher sees the
			// final state; both target paths share the default.
			hold := testHold
			if hold == 0 && testHeaded {
				hold = 1500 * time.Millisecond
			}
			switch {
			case t.App != "":
				app, ok := appByName[t.App]
				if !ok {
					return testResult{
						Name:    t.Name,
						Failure: fmt.Sprintf("test targets undeclared app %q", t.App),
					}
				}
				return runAppTest(cmd.Context(), t, app, testTarget, nameToID,
					testTimeout, testHeaded, testSlowmo, hold, onEvent)
			default:
				// View-target tests run on the SAME segmented driver, served
				// from an embedded SPA page — one runner, one vocabulary,
				// full event/trace coverage.
				return runViewTest(cmd.Context(), doc, t, nameToID,
					testTimeout, testHeaded, testSlowmo, hold, onEvent)
			}
		}

		var results []testResult
		switch mode {
		case renderTUI:
			results = runTestsTUI(doc.Tests, testTarget, path, runOne)
		default:
			// Stream / JSON share the same loop; JSON suppresses the
			// per-event narrative and emits its structured report below.
			results = make([]testResult, 0, len(doc.Tests))
			for _, t := range doc.Tests {
				var sink func(e2eEvent)
				if mode == renderStream {
					sink = renderEvent
				}
				results = append(results, runOne(t, sink))
			}
		}

		sort.SliceStable(results, func(i, j int) bool { return results[i].Name < results[j].Name })

		// Emit the trace before reporting results, so an exporter failure
		// surfaces even on an otherwise-passing run.
		if rec != nil {
			if err := emitTrace(rec, testTraceFile, testOTLP); err != nil {
				fmt.Fprintf(os.Stderr, "sigil: trace export: %v\n", err)
			}
		}

		if testJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			out := struct {
				File    string       `json:"file"`
				View    string       `json:"view"`
				Total   int          `json:"total"`
				Passed  int          `json:"passed"`
				Failed  int          `json:"failed"`
				Results []testResult `json:"results"`
			}{
				File:    path,
				View:    doc.Name,
				Total:   len(results),
				Results: results,
			}
			for _, r := range results {
				if r.Passed {
					out.Passed++
				} else {
					out.Failed++
				}
			}
			_ = enc.Encode(out)
			if out.Failed > 0 {
				return ErrSilent
			}
			return nil
		}

		// Human output. The TUI already printed its own per-test rows
		// and summary; only the stream path needs an aggregate line
		// after the per-event narrative. Both paths compute the exit
		// code from the result count.
		passed := 0
		for _, r := range results {
			if r.Passed {
				passed++
			}
		}
		if mode == renderStream {
			fmt.Printf("\n%d/%d passed\n", passed, len(results))
		}
		if passed != len(results) {
			return ErrSilent
		}
		return nil
	},
}

// renderMode picks how per-test events surface in the terminal. JSON
// wins when --json is set (machine consumption). Otherwise we use the
// TUI on interactive terminals and the streaming text path everywhere
// else (CI logs, pipes, redirected files).
type renderMode int

const (
	renderJSON renderMode = iota
	renderTUI
	renderStream
)

// pickRenderMode reads testJSON + stdout's TTY state to decide which
// renderer the test command uses for the run. Kept here so the
// dispatch in RunE stays one line.
func pickRenderMode() renderMode {
	if testJSON {
		return renderJSON
	}
	if isStdoutTTY() {
		return renderTUI
	}
	return renderStream
}

// isStdoutTTY returns true when os.Stdout is connected to a terminal.
// Uses the file-mode bit check (no external dep). False when stdout
// is a pipe, a regular file, or otherwise non-character-device.
func isStdoutTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// testResult is one row of the run report. Failure is empty on pass;
// on fail it carries a human-readable reason plus the step's source
// position for editor click-through.
type testResult struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Failure  string `json:"failure,omitempty"`
	StepLine int    `json:"step_line,omitempty"`
	StepCol  int    `json:"step_col,omitempty"`
	DurMS    int64  `json:"duration_ms"`
}

// runViewTest serves the view as a SPA on an ephemeral local server, then
// drives the scenario against it through the shared segmented runner
// (runScenarioAt). The server is torn down when the scenario finishes. A
// view target is native (not external), so layout invariants and
// expect-cell apply.
func runViewTest(parent context.Context, doc ir.Document, t ir.Test, nameToID map[string]string,
	timeout time.Duration, headed bool, slowmo, hold time.Duration, onEvent func(e2eEvent)) testResult {
	res := testResult{Name: t.Name}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		res.Failure = fmt.Sprintf("listen: %v", err)
		return res
	}
	title := "Sigil"
	if doc.Name != "" {
		title = "Sigil — " + doc.Name
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = html.WriteDoc(w, title, doc)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	host := "http://" + ln.Addr().String() + "/"
	return runScenarioAt(parent, t, ir.App{Name: t.View}, host, false, "web",
		nameToID, timeout, headed, slowmo, hold, onEvent)
}

func init() {
	testCmd.Flags().BoolVar(&testJSON, "json", false,
		"emit structured JSON results on stdout")
	testCmd.Flags().DurationVar(&testTimeout, "timeout", 15*time.Second,
		"per-test timeout")
	testCmd.Flags().BoolVar(&testHeaded, "headed", false,
		"run Chromium with a visible window (for debugging)")
	testCmd.Flags().StringVar(&testTarget, "target", "web",
		"target adapter for `scenario in <App>` tests (today: web)")
	testCmd.Flags().DurationVar(&testSlowmo, "slowmo", 0,
		"pause between steps so a watcher can follow each one (e.g. 500ms)")
	testCmd.Flags().DurationVar(&testHold, "hold", 0,
		"hold the page open before scenario_end so a watcher can see the final state (e.g. 2s). Defaults to 1.5s when --headed is set, 0 otherwise.")
	testCmd.Flags().StringVar(&testTraceFile, "trace", "",
		"write the run as OTLP/JSON traces to this file (scenario=trace, step=span)")
	testCmd.Flags().StringVar(&testOTLP, "otlp", "",
		"POST the run's traces to this OTLP/HTTP endpoint (e.g. http://localhost:4318) — Tempo/Grafana-ready")
	testCmd.Flags().BoolVar(&testTraceLean, "trace-lean", false,
		"prod-lean tracing: keep span structure/timing/status, drop per-step detail attributes")
}
