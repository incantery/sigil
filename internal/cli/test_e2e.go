package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/incantery/sigil/pkg/codegen/e2e"
	"github.com/incantery/sigil/pkg/ir"
)

// e2eEvent is the in-Go shape of one event the compiled JS bundle
// emits via window.__sigilNotify. The bundle marshals JSON; the
// runner unmarshals into this. Fields are sparse — only the ones
// that matter for the event type are populated.
type e2eEvent struct {
	Type     string         `json:"type"`
	Step     int            `json:"step,omitempty"`
	Verb     string         `json:"verb,omitempty"`
	Payload  map[string]any `json:"payload,omitempty"`
	OK       *bool          `json:"ok,omitempty"`
	Failure  string         `json:"failure,omitempty"`
	At       int64          `json:"at"`
	Line     int            `json:"line,omitempty"`
	Col      int            `json:"col,omitempty"`
	Scenario string         `json:"scenario,omitempty"`
	App      string         `json:"app,omitempty"`
	Target   string         `json:"target,omitempty"`
	// bind-event fields: an `extract` step reports the captured value here
	// so the runner can thread it across a navigation and interpolate it
	// into later steps.
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

// runAppTest executes one `scenario in <App>` test against the chosen
// target. It is the compiled-bundle path: the JS test logic runs in
// the page under test; this Go side is just a thin orchestrator that
// launches the browser, registers a Runtime.addBinding channel,
// navigates, evaluates the bundle, and consumes the event stream.
//
// Events are surfaced through onEvent (called for every event the
// bundle emits, in order). onEvent may render to stdout, drive a
// bubbletea program, collect for JSON output, or do nothing — the
// caller picks. A nil onEvent is treated as "no-op."
//
// The returned testResult exists so the outer summary loop has a
// uniform shape across runAppTest and the legacy runOneTest.
func runAppTest(parent context.Context, t ir.Test, app ir.App, targetName string,
	nameToID map[string]string,
	timeout time.Duration, headed bool, slowmo, hold time.Duration,
	onEvent func(e2eEvent)) testResult {

	if onEvent == nil {
		onEvent = func(e2eEvent) {}
	}

	start := time.Now()
	res := testResult{Name: t.Name}

	// 1. Resolve the target adapter and host URL.
	target, ok := app.Targets[targetName]
	if !ok {
		res.Failure = fmt.Sprintf("app %q has no target %q", app.Name, targetName)
		res.DurMS = time.Since(start).Milliseconds()
		return res
	}
	if targetName != "web" {
		res.Failure = fmt.Sprintf("target %q is not yet supported (v0: web only)", targetName)
		res.DurMS = time.Since(start).Milliseconds()
		return res
	}
	hostAny, ok := target.Config["host"]
	if !ok {
		res.Failure = fmt.Sprintf("app %q target %q has no `host`", app.Name, targetName)
		res.DurMS = time.Since(start).Milliseconds()
		return res
	}
	host, ok := hostAny.(string)
	if !ok || host == "" {
		res.Failure = fmt.Sprintf("app %q target %q `host` must be a non-empty string", app.Name, targetName)
		res.DurMS = time.Since(start).Milliseconds()
		return res
	}

	external, _ := target.Config["external"].(bool)
	return runScenarioAt(parent, t, app, host, external, targetName, nameToID, timeout, headed, slowmo, hold, onEvent)
}

// runScenarioAt drives one scenario against an already-running page at
// `host`. Both entry points funnel through here — the app-target path
// (host from the app's config) and the view-target path (host = an
// embedded server rendering the view as a SPA) — so there is one
// segmented driver, one verb vocabulary, one event stream. `external`
// suppresses native-only checks (layout invariants, expect-cell).
func runScenarioAt(parent context.Context, t ir.Test, app ir.App, host string, external bool, targetName string,
	nameToID map[string]string,
	timeout time.Duration, headed bool, slowmo, hold time.Duration,
	onEvent func(e2eEvent)) testResult {

	if onEvent == nil {
		onEvent = func(e2eEvent) {}
	}
	start := time.Now()
	res := testResult{Name: t.Name}

	// Per-segment bundle generation. A scenario is normally one bundle, but
	// a full-page navigation destroys the running bundle, so the runner
	// re-injects the remaining steps as a fresh segment (see the driver
	// loop). `binds` carries values captured by `extract` across
	// navigations and is interpolated into each segment's steps.
	genSegment := func(startIdx int, binds map[string]string) (string, error) {
		seg := t
		seg.Steps = interpolateSteps(t.Steps, binds)
		return e2e.Bundle(e2e.Input{
			Test:       seg,
			App:        app,
			Target:     targetName,
			NameToID:   nameToID,
			SlowmoMs:   int(slowmo / time.Millisecond),
			HoldMs:     int(hold / time.Millisecond),
			External:   external,
			StartIndex: startIdx,
		})
	}

	// 3. chromedp context per test → fresh tab, fresh window state.
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", !headed),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("mute-audio", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(parent, allocOpts...)
	defer cancelAlloc()
	bctx, cancelB := chromedp.NewContext(allocCtx)
	defer cancelB()
	tctx, cancelT := context.WithTimeout(bctx, timeout)
	defer cancelT()

	// 4. Wire two channels off the CDP event stream:
	//    - events: every window.__sigilNotify(...) call from the bundle.
	//    - navs:   top-frame full-page navigations, forwarded only once
	//              `armed` (after the first segment is injected) so the
	//              initial page load isn't read as a mid-scenario nav.
	events := make(chan e2eEvent, 64)
	navs := make(chan string, 8)
	var armed atomic.Bool

	chromedp.ListenTarget(bctx, func(ev any) {
		switch e := ev.(type) {
		case *runtime.EventBindingCalled:
			if e.Name != "__sigilNotify" {
				return
			}
			var pe e2eEvent
			if err := json.Unmarshal([]byte(e.Payload), &pe); err != nil {
				// A malformed event shouldn't tank the test — synthesize a
				// diagnostic event the formatter can render.
				pe = e2eEvent{Type: "runner_error", Failure: fmt.Sprintf("decode event: %v", err)}
			}
			select {
			case events <- pe:
			case <-bctx.Done():
			}
		case *page.EventFrameNavigated:
			// Top frame only (no parent) and only after arming. A full-page
			// navigation destroys the running bundle; the driver re-injects
			// the remaining steps against the new document.
			if !armed.Load() || e.Frame == nil || e.Frame.ParentID != "" {
				return
			}
			select {
			case navs <- e.Frame.URL:
			case <-bctx.Done():
			}
		}
	})

	// 5. Enable Page events (for frame navigations), register the binding
	//    (must precede the bundle so __sigilNotify is defined at first
	//    call; it persists across navigations), navigate, wait for body.
	if err := chromedp.Run(tctx,
		page.Enable(),
		runtime.AddBinding("__sigilNotify"),
		chromedp.Navigate(host),
		chromedp.WaitReady("body", chromedp.ByQuery),
	); err != nil {
		res.Failure = fmt.Sprintf("navigate %s: %v", host, err)
		res.DurMS = time.Since(start).Milliseconds()
		return res
	}

	// 6. Inject the first segment, then arm navigation handling. Evaluate
	//    returns the IIFE's promise but we don't await it — completion is
	//    signaled by the event stream, which gives per-step granularity
	//    and (critically) lets a navigating bundle die without blocking us.
	inject := func(startIdx int, binds map[string]string) error {
		bundle, err := genSegment(startIdx, binds)
		if err != nil {
			return fmt.Errorf("codegen: %w", err)
		}
		return chromedp.Run(tctx, chromedp.Evaluate(bundle, nil))
	}
	binds := map[string]string{}
	if err := inject(0, binds); err != nil {
		res.Failure = fmt.Sprintf("inject bundle: %v", err)
		res.DurMS = time.Since(start).Milliseconds()
		return res
	}
	armed.Store(true)

	// 7. Drive the scenario. Consume events until scenario_end; on a
	//    mid-scenario navigation, synthesize a step_end for the step that
	//    triggered it (its effect — the navigation — succeeded), wait for
	//    the new document, and re-inject the tail with the bindings
	//    accumulated so far. `completed` tracks the highest step number
	//    that finished; the navigating step is the next one after it, and
	//    its 1-based number equals the 0-based index of the step to resume
	//    from — which is exactly the next segment's StartIndex.
	collected := make([]e2eEvent, 0, len(t.Steps)*2+2)
	completed := 0
	for {
		select {
		case e := <-events:
			switch e.Type {
			case "bind":
				// Internal coordination: stash the captured value Go-side so
				// it survives navigation and interpolates into later steps.
				// Forwarded to onEvent so a trace recorder can annotate the
				// extract span; the text renderers have no `bind` case and
				// ignore it.
				binds[e.Name] = e.Value
				onEvent(e)
				continue
			case "step_end":
				if e.OK != nil && *e.OK && e.Step > completed {
					completed = e.Step
				}
			}
			collected = append(collected, e)
			onEvent(e)
			if e.Type == "scenario_end" {
				res.DurMS = time.Since(start).Milliseconds()
				if e.OK != nil && *e.OK {
					res.Passed = true
					return res
				}
				res.Failure = lastFailure(collected)
				if last := lastStepEvent(collected); last != nil {
					res.StepLine = last.Line
					res.StepCol = last.Col
				}
				return res
			}
		case <-navs:
			navStep := completed + 1
			ok := true
			synthetic := e2eEvent{Type: "step_end", Step: navStep, OK: &ok, At: time.Since(start).Milliseconds()}
			collected = append(collected, synthetic)
			onEvent(synthetic)
			completed = navStep
			if err := chromedp.Run(tctx, chromedp.WaitReady("body", chromedp.ByQuery)); err != nil {
				res.Failure = fmt.Sprintf("after navigation: %v", err)
				res.DurMS = time.Since(start).Milliseconds()
				return res
			}
			if err := inject(navStep, binds); err != nil {
				res.Failure = err.Error()
				res.DurMS = time.Since(start).Milliseconds()
				return res
			}
		case <-tctx.Done():
			res.DurMS = time.Since(start).Milliseconds()
			// Surface the timeout as a synthetic event so the renderer
			// (stream or TUI) gets a chance to display it before the
			// per-test loop moves on.
			onEvent(e2eEvent{
				Type:    "runner_error",
				Failure: "timed out before scenario completion (page hung, JS error, or bundle never reached scenario_end)",
				At:      time.Since(start).Milliseconds(),
			})
			res.Failure = "timed out before scenario completion (page hung, JS error, or bundle never reached scenario_end)"
			return res
		}
	}
}

// interpolateSteps returns a copy of steps with ${name} placeholders in
// string-valued args replaced from binds. Bindings are captured by
// `extract` and held runner-side, so a value read on one page can be
// asserted on the next after a full-page navigation. With no bindings the
// input slice is returned unchanged (read-only).
func interpolateSteps(steps []ir.Step, binds map[string]string) []ir.Step {
	if len(binds) == 0 {
		return steps
	}
	out := make([]ir.Step, len(steps))
	for i, s := range steps {
		out[i] = s
		if len(s.Args) > 0 {
			na := make(map[string]any, len(s.Args))
			for k, v := range s.Args {
				if str, ok := v.(string); ok {
					na[k] = interpolateBinds(str, binds)
				} else {
					na[k] = v
				}
			}
			out[i].Args = na
		}
		// A match step's arms hold nested steps; interpolate into them too.
		if len(s.Arms) > 0 {
			arms := make([]ir.StepArm, len(s.Arms))
			for j, a := range s.Arms {
				arms[j] = ir.StepArm{Match: a.Match, Steps: interpolateSteps(a.Steps, binds)}
			}
			out[i].Arms = arms
		}
	}
	return out
}

// interpolateBinds substitutes every ${name} occurrence in s with its
// bound value. Unbound placeholders are left intact so the resulting
// assertion fails loudly (text not found) rather than silently matching.
func interpolateBinds(s string, binds map[string]string) string {
	for k, v := range binds {
		s = strings.ReplaceAll(s, "${"+k+"}", v)
	}
	return s
}

// lastFailure walks the collected events backwards looking for the
// failure message that caused scenario_end({ok:false}). Returns a
// generic message if no specific step_end carrying failure was found.
func lastFailure(events []e2eEvent) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Failure != "" {
			return events[i].Failure
		}
	}
	return "scenario failed (no failure message)"
}

// lastStepEvent returns the most recent step_end event with source
// position info, used to populate StepLine/StepCol on the result.
func lastStepEvent(events []e2eEvent) *e2eEvent {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Line > 0 {
			return &events[i]
		}
	}
	return nil
}

// renderEvent prints one event as narrative text. Used by the
// streaming (non-TUI, non-JSON) mode — CI logs, pipes, redirected
// files. Per-step "at Nms" labels match the TUI's convention: the
// time is elapsed-from-scenario-start, not per-step duration.
// Pillar-3 diagnosis blocks land later as the codegen gains the
// observability to populate them.
func renderEvent(e e2eEvent) {
	switch e.Type {
	case "scenario_start":
		fmt.Printf("\n  %s · %s\n", e.App, e.Scenario)
	case "step_start":
		intent := e.Verb
		if text, ok := e.Payload["text"].(string); ok && e.Verb == "expect-text" {
			intent = fmt.Sprintf(`%s %q`, e.Verb, text)
		}
		fmt.Printf("    · %s\n", intent)
	case "step_end":
		mark := "✓"
		if e.OK == nil || !*e.OK {
			mark = "✗"
		}
		if e.Failure != "" {
			fmt.Printf("    %s step %d at %s — %s\n", mark, e.Step, formatStreamDuration(e.At), e.Failure)
		} else {
			fmt.Printf("    %s step %d at %s\n", mark, e.Step, formatStreamDuration(e.At))
		}
	case "scenario_end":
		if e.OK != nil && *e.OK {
			fmt.Printf("    PASSED in %s\n", formatStreamDuration(e.At))
		} else {
			fmt.Printf("    FAILED after %s\n", formatStreamDuration(e.At))
		}
	case "runner_error":
		fmt.Printf("    ! runner error: %s\n", e.Failure)
	}
}

// formatStreamDuration is the stream-mode twin of formatDuration in
// test_tui.go — duplicated to avoid a package-internal helpers file
// for two tiny formatters. Sub-second stays in ms, otherwise one
// decimal of seconds.
func formatStreamDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}
