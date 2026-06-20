// Package e2e generates a self-contained JS bundle for one Sigil test
// scenario. The bundle runs inside the page under test (via CDP
// Runtime.evaluate after the page navigates), executes each step in
// order, and reports progress through window.__sigilNotify — a CDP
// runtime binding the Go-side runner registers before injection.
//
// This mirrors the rest of Sigil's compiler philosophy: codegen does
// the work, the runtime is small. The Go test runner becomes a thin
// orchestrator (launch browser, inject bundle, consume events). All
// per-test logic lives in the emitted JS.
//
// v0 vocabulary: `expect-text` only. Verbs are added by extending
// emitStep here + adding parser/lower support; the helper library at
// the top of the bundle grows as new verbs need new DOM primitives.
package e2e

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/incantery/sigil/pkg/ir"
)

// Input is everything one test bundle needs. App is the resolved
// declaration the test targets (`scenario in <App>`); for the legacy
// `scenario <View>` form, App is nil and the runner serves the view
// from an embedded server (out of scope for this v0 codegen — the
// legacy runner stays for view-target tests until the codegen path
// can express them too).
//
// NameToID is the source-cell-name → runtime-cell-id map (the
// reverse of doc.CellNames). The compiled bundle uses it to resolve
// `expect-cell <name> <value>` to a window.__sigil_cells[<id>] read
// at compile time, since the JS side doesn't have access to the
// lowering tables.
//
// SlowmoMs inserts a pause between steps so a human watching headed
// Chromium can follow what's happening. 0 = no slowmo (the default).
// HoldMs holds the page open after a successful scenario_end — gives
// the watcher time to see the final state before the browser context
// tears down. Only meaningful when headed; the Go side decides when
// to use them.
type Input struct {
	Test     ir.Test
	App      ir.App
	Target   string // e.g. "web"
	NameToID map[string]string
	SlowmoMs int
	HoldMs   int
	// External marks a foreign (non-Sigil) target: the page under test is
	// a frontend Sigil never compiled (vanilla / React / …). Native-only
	// checks are suppressed for it — the root-owns-viewport layout
	// invariants (which assume Sigil's `.s-root`) don't run, and
	// introspection verbs (`expect-cell`) have no cell map to read.
	External bool
	// StartIndex is the step the segment begins at. A scenario is injected
	// in one segment normally, but a full-page navigation destroys the
	// running bundle, so the runner re-injects the tail as a fresh segment
	// starting at the step after the one that navigated. Steps are numbered
	// StartIndex+i+1 so numbers stay continuous across segments, and the
	// scenario_start banner is emitted only for the first segment.
	StartIndex int
}

// DefaultStepTimeoutMs is how long each polling step waits before
// declaring "not found." Kept high enough that local dev servers
// with cold start latency don't false-fail; low enough that real
// regressions surface within a sane CI budget. Per-step override
// will come with later verbs (e.g. `wait-for ... within 30s`).
const DefaultStepTimeoutMs = 5000

// Bundle emits the JS bundle source for a single test. The returned
// string is intended to be passed verbatim to CDP Runtime.evaluate
// after the page under test has loaded.
func Bundle(in Input) (string, error) {
	if in.Test.Name == "" {
		return "", fmt.Errorf("e2e: test has no name")
	}
	var b strings.Builder
	writeHelpers(&b, in)
	if err := writeScenario(&b, in); err != nil {
		return "", err
	}
	return b.String(), nil
}

// writeHelpers emits the per-bundle JS helper library: the notify
// shim, an elapsed-time helper, slow/hold pause helpers (no-ops when
// their ms is 0), and one DOM-query primitive per verb in scope.
// Keep this small — every byte ships into every test, and
// auditability is a feature.
func writeHelpers(b *strings.Builder, in Input) {
	fmt.Fprintf(b, `(async () => {
  const __notify = (e) => window.__sigilNotify(JSON.stringify(e));
  const __T0 = performance.now();
  const __at = () => Math.round(performance.now() - __T0);

  // Slowmo + hold helpers. Slowmo pauses between steps so a watcher
  // can see each one; hold pauses before scenario_end so the page
  // stays visible briefly after the last step. Both are baked-in
  // constants per bundle, no-op when 0.
  const __SLOW_MS = %d;
  const __HOLD_MS = %d;
  async function __slow() { if (__SLOW_MS > 0) await new Promise(r => setTimeout(r, __SLOW_MS)); }
  async function __hold() { if (__HOLD_MS > 0) await new Promise(r => setTimeout(r, __HOLD_MS)); }

  // __allEls returns every element under root INCLUDING those inside open
  // shadow roots — querySelectorAll alone stops at the shadow boundary, so
  // a web component's content would be invisible to the text matchers
  // without this. Closed shadow roots are unreachable by design.
  function __allEls(root) {
    const out = [];
    const walk = (node) => {
      const els = node.querySelectorAll("*");
      for (const el of els) {
        out.push(el);
        if (el.shadowRoot) walk(el.shadowRoot);
      }
    };
    if (root) walk(root);
    return out;
  }

  // __findText scans for an element whose direct text content (text
  // nodes only, joined and trimmed) equals the target. Polls every
  // 50ms until found or the deadline elapses. Direct-text matching
  // avoids false positives from parent containers that "contain" the
  // text only because a descendant does.
  async function __findText(text, timeoutMs) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const all = __allEls(document.body);
      for (const el of all) {
        let direct = "";
        for (const n of el.childNodes) {
          if (n.nodeType === 3) direct += n.textContent;
        }
        if (direct.trim() === text) return true;
      }
      await new Promise(r => setTimeout(r, 50));
    }
    return false;
  }

  // __waitTextGone is __findText's inverse with auto-waiting: it polls
  // until NO element's direct text matches, so asserting on content
  // that is mid-disappearance (a pending indicator falling after a
  // stream settles, a row leaving after a refetch) waits for the UI to
  // catch up instead of failing on the first stale frame. Absent text
  // passes on the first check; permanently present text fails at the
  // deadline.
  async function __waitTextGone(text, timeoutMs) {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      let present = false;
      const all = __allEls(document.body);
      for (const el of all) {
        let direct = "";
        for (const n of el.childNodes) {
          if (n.nodeType === 3) direct += n.textContent;
        }
        if (direct.trim() === text) { present = true; break; }
      }
      if (!present) return true;
      if (Date.now() >= deadline) return false;
      await new Promise(r => setTimeout(r, 50));
    }
  }

  // __findButton polls for a <button> whose direct-text label matches.
  // Same direct-text rule as __findText so a button containing icons +
  // a label doesn't fail-match on the parent containers around it.
  // Icon-only buttons have no text label; they carry an aria-label
  // (the icon's name, emitted by codegen), so the accessible name is
  // the fallback match: click button "send" finds an icon-only send
  // button.
  async function __findButton(name, timeoutMs) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const buttons = __allEls(document.body).filter(e => e.tagName === "BUTTON");
      for (const btn of buttons) {
        let direct = "";
        for (const n of btn.childNodes) {
          if (n.nodeType === 3) direct += n.textContent;
        }
        if (direct.trim() === name) return btn;
        if (!direct.trim() && btn.getAttribute("aria-label") === name) return btn;
      }
      await new Promise(r => setTimeout(r, 50));
    }
    return null;
  }

  // __findClickableText: finds a text element and clicks its nearest
  // clickable ancestor (parent with onclick handler).
  async function __findClickableText(label, timeoutMs) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const all = __allEls(document.body);
      for (const el of all) {
        let direct = "";
        for (const n of el.childNodes) {
          if (n.nodeType === 3) direct += n.textContent;
        }
        if (direct.trim() === label) {
          let target = el;
          while (target && !target.onclick) target = target.parentElement;
          if (target) return target;
        }
      }
      await new Promise(r => setTimeout(r, 50));
    }
    return null;
  }

  // __directText joins an element's direct text nodes (not descendants)
  // and trims — the same notion of "an element's own text" the matchers
  // use, so extract reads what the user sees on that node.
  function __directText(el) {
    let s = "";
    for (const n of el.childNodes) { if (n.nodeType === 3) s += n.textContent; }
    return s.trim();
  }

  // __waitEl polls for the first element matching a CSS selector. Used by
  // the extract verb — the value to capture may mount async, so poll to a
  // deadline rather than read once.
  async function __waitEl(sel, timeoutMs) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const el = document.querySelector(sel);
      if (el) return el;
      await new Promise(r => setTimeout(r, 50));
    }
    return null;
  }

  // __waitCount polls until exactly n elements match the selector. The
  // Observe-floor way to assert cardinality (e.g. "3 rows rendered"),
  // polling so a list that populates async settles before the check.
  async function __waitCount(sel, n, timeoutMs) {
    const deadline = Date.now() + timeoutMs;
    let last = -1;
    while (Date.now() < deadline) {
      last = document.querySelectorAll(sel).length;
      if (last === n) return {ok: true, got: last};
      await new Promise(r => setTimeout(r, 50));
    }
    return {ok: false, got: last};
  }

  // __waitForRoleName polls until an element matching the CSS selector
  // role has text equal to name. Backs the wait-for verb (an explicit
  // "wait until X appears" for async mounts).
  async function __waitForRoleName(role, name, timeoutMs) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      for (const el of __allEls(document.body)) {
        if (el.matches(role) && (el.textContent || "").trim() === name) return true;
      }
      await new Promise(r => setTimeout(r, 50));
    }
    return false;
  }

  // __waitPath polls until location.pathname equals want. After a full-
  // page navigation the new document may still be settling when the step
  // runs, so wait for the path rather than read it once.
  async function __waitPath(want, timeoutMs) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      if (location.pathname === want) return true;
      await new Promise(r => setTimeout(r, 50));
    }
    return false;
  }

  // __fill finds an element matching the CSS selector whose placeholder
  // or aria-label equals name, sets its value, and fires input + change
  // so the codegen's bind-value listener updates the bound cell. Polls
  // until found, mirroring the legacy runner's fill semantics.
  async function __fill(selector, name, value, timeoutMs) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const els = document.querySelectorAll(selector);
      for (const el of els) {
        if ((el.placeholder || "") === name || (el.getAttribute("aria-label") || "") === name) {
          el.value = value;
          el.dispatchEvent(new Event("input", {bubbles: true}));
          el.dispatchEvent(new Event("change", {bubbles: true}));
          return true;
        }
      }
      await new Promise(r => setTimeout(r, 50));
    }
    return false;
  }

`, in.SlowmoMs, in.HoldMs)
}

// writeScenario emits the per-scenario IIFE body: start event, each
// step's emitter, and the final pass/fail event.
//
// Slowmo pauses go BEFORE each step (except the first) so the watcher
// sees the result of step N for the pause window before step N+1
// kicks off. A trailing HoldMs pause sits before scenario_end so the
// page stays visible briefly after the last step's effect on screen.
// Both are no-ops at zero; the compile-time check below skips
// emitting the pause line entirely when ms <= 0 to keep the bundle
// tight in CI mode.
func writeScenario(b *strings.Builder, in Input) error {
	// The scenario_start banner belongs to the first segment only; a
	// re-injected tail (StartIndex > 0) continues an already-started run.
	if in.StartIndex == 0 {
		startEvent := map[string]any{
			"type":     "scenario_start",
			"scenario": in.Test.Name,
			"app":      in.App.Name,
			"target":   in.Target,
		}
		fmt.Fprintf(b, "  __notify({...%s, at: __at()});\n", jsonObj(startEvent))
	}

	for i := in.StartIndex; i < len(in.Test.Steps); i++ {
		if i > in.StartIndex {
			fmt.Fprintln(b, "  await __slow();")
		}
		if err := emitStep(b, i+1, in.Test.Steps[i], in); err != nil {
			return err
		}
	}

	// The layout invariants assert Sigil's root-owns-viewport model
	// (.s-root spans the viewport, the page never scrolls). They are
	// meaningful only for a Sigil-rendered page; a foreign target has no
	// .s-root, so skip them rather than fail a frontend Sigil never owned.
	if !in.External {
		emitLayoutInvariants(b, len(in.Test.Steps)+1)
	}

	fmt.Fprintln(b, "  await __hold();")
	fmt.Fprintln(b, `  __notify({type: "scenario_end", ok: true, at: __at()});`)
	fmt.Fprintln(b, `})();`)
	return nil
}

// emitLayoutInvariants appends an automatic final step to every
// scenario. Under the root-owns-viewport model the invariants are
// universal — not just for app shells:
//
//   - the page never scrolls (document scrollHeight fits the
//     viewport; scrolling is always an interior property)
//   - the .s-root element spans the viewport exactly
//   - an app shell (.s-h-screen), when present, spans it too
//
// Three shipped layout bugs — the unsuppressed body gutter, mobile
// vh overflow, and the :has() selector miss — all had the same
// signature (page scrollable by a fixed delta, composer below the
// fold) and any one run of these assertions would have caught each
// pre-ship.
func emitLayoutInvariants(b *strings.Builder, idx int) {
	emitStepStart(b, idx, "expect-layout", map[string]any{"auto": true}, ir.Step{})
	fmt.Fprintf(b, "  {\n")
	fmt.Fprintf(b, "    const errs = [];\n")
	fmt.Fprintf(b, "    if (document.documentElement.scrollHeight > window.innerHeight + 1)\n")
	fmt.Fprintf(b, "      errs.push(\"page scrolls: doc scrollHeight \" + document.documentElement.scrollHeight + \" > viewport \" + window.innerHeight);\n")
	fmt.Fprintf(b, "    const root = document.querySelector(\".s-root\");\n")
	fmt.Fprintf(b, "    if (!root) {\n")
	fmt.Fprintf(b, "      errs.push(\"no .s-root element mounted\");\n")
	fmt.Fprintf(b, "    } else {\n")
	fmt.Fprintf(b, "      const rr = root.getBoundingClientRect();\n")
	fmt.Fprintf(b, "      if (Math.abs(rr.top) > 1 || Math.abs(rr.bottom - window.innerHeight) > 1)\n")
	fmt.Fprintf(b, "        errs.push(\"root rect \" + Math.round(rr.top) + \"->\" + Math.round(rr.bottom) + \" != viewport 0->\" + window.innerHeight);\n")
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "    const shell = document.querySelector(\".s-h-screen\");\n")
	fmt.Fprintf(b, "    if (shell) {\n")
	fmt.Fprintf(b, "      const r = shell.getBoundingClientRect();\n")
	fmt.Fprintf(b, "      if (Math.abs(r.top) > 1 || Math.abs(r.bottom - window.innerHeight) > 1)\n")
	fmt.Fprintf(b, "        errs.push(\"shell rect \" + Math.round(r.top) + \"->\" + Math.round(r.bottom) + \" != viewport 0->\" + window.innerHeight);\n")
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "    if (!errs.length) {\n")
	emitStepOK(b, idx)
	fmt.Fprintf(b, "    } else {\n")
	emitStepFail(b, idx, `"layout invariant violated: " + errs.join("; ")`)
	fmt.Fprintf(b, "    }\n")
	fmt.Fprintf(b, "  }\n")
}

// emitStep generates one step's JS: a step_start event with the
// verb's intent payload, the verb-specific action, and a step_end
// event with ok/failure. On failure the scenario short-circuits with
// a scenario_end({ok:false}) and returns from the IIFE — subsequent
// steps don't run, matching the "first failure stops the test"
// semantic users expect from playwright/cypress.
//
// Each verb's helper emit shares the same start/end notify shape;
// emitStepFooter centralizes the not-found / mismatch tail so the
// step bodies focus on the verb-specific check.
func emitStep(b *strings.Builder, idx int, step ir.Step, in Input) error {
	switch step.Kind {
	case "expect_text":
		text, _ := step.Args["text"].(string)
		emitStepStart(b, idx, "expect-text", map[string]any{"text": text}, step)
		fmt.Fprintf(b, "  {\n")
		fmt.Fprintf(b, "    const ok = await __findText(%s, %d);\n", jsString(text), DefaultStepTimeoutMs)
		fmt.Fprintf(b, "    if (ok) {\n")
		emitStepOK(b, idx)
		fmt.Fprintf(b, "    } else {\n")
		emitStepFail(b, idx,
			fmt.Sprintf("\"text \" + %s + \" not found within %dms\"", jsString(`"`+text+`"`), DefaultStepTimeoutMs))
		fmt.Fprintf(b, "    }\n")
		fmt.Fprintf(b, "  }\n")
		return nil

	case "click":
		role, _ := step.Args["role"].(string)
		name, _ := step.Args["name"].(string)
		if role != "button" && role != "text" {
			return fmt.Errorf("e2e: click role %q not yet supported (compiled-bundle vocab: button, text)", role)
		}
		if role == "text" {
			emitStepStart(b, idx, "click", map[string]any{"role": role, "name": name}, step)
			fmt.Fprintf(b, "  {\n")
			fmt.Fprintf(b, "    const el = await __findClickableText(%s, %d);\n", jsString(name), DefaultStepTimeoutMs)
			fmt.Fprintf(b, "    if (el) {\n")
			fmt.Fprintf(b, "      el.click();\n")
			emitStepOK(b, idx)
			fmt.Fprintf(b, "    } else {\n")
			emitStepFail(b, idx,
				fmt.Sprintf("\"clickable text \" + %s + \" not found within %dms\"", jsString(`"`+name+`"`), DefaultStepTimeoutMs))
			fmt.Fprintf(b, "    }\n")
			fmt.Fprintf(b, "  }\n")
			return nil
		}
		emitStepStart(b, idx, "click", map[string]any{"role": role, "name": name}, step)
		fmt.Fprintf(b, "  {\n")
		fmt.Fprintf(b, "    const btn = await __findButton(%s, %d);\n", jsString(name), DefaultStepTimeoutMs)
		fmt.Fprintf(b, "    if (btn) {\n")
		fmt.Fprintf(b, "      btn.click();\n")
		emitStepOK(b, idx)
		fmt.Fprintf(b, "    } else {\n")
		emitStepFail(b, idx,
			fmt.Sprintf("\"button \" + %s + \" not found within %dms\"", jsString(`"`+name+`"`), DefaultStepTimeoutMs))
		fmt.Fprintf(b, "    }\n")
		fmt.Fprintf(b, "  }\n")
		return nil

	case "expect_no_text":
		text, _ := step.Args["text"].(string)
		emitStepStart(b, idx, "expect-no-text", map[string]any{"text": text}, step)
		// Inverse of expect-text, with the same auto-waiting: the step
		// passes the moment the text is absent and only fails if it is
		// STILL present at the deadline. Disappearance is usually the
		// tail of an async settle (stream close, refetch swap), so the
		// assertion waits for the UI to catch up rather than failing on
		// the first stale frame.
		fmt.Fprintf(b, "  {\n")
		fmt.Fprintf(b, "    const gone = await __waitTextGone(%s, %d);\n", jsString(text), DefaultStepTimeoutMs)
		fmt.Fprintf(b, "    if (gone) {\n")
		emitStepOK(b, idx)
		fmt.Fprintf(b, "    } else {\n")
		emitStepFail(b, idx,
			fmt.Sprintf("\"text \" + %s + \" still present after %dms\"", jsString(`"`+text+`"`), DefaultStepTimeoutMs))
		fmt.Fprintf(b, "    }\n")
		fmt.Fprintf(b, "  }\n")
		return nil

	case "fill":
		role, _ := step.Args["role"].(string)
		name, _ := step.Args["name"].(string)
		value, _ := step.Args["value"].(string)
		emitStepStart(b, idx, "fill", map[string]any{"role": role, "name": name, "value": value}, step)
		fmt.Fprintf(b, "  {\n")
		fmt.Fprintf(b, "    const ok = await __fill(%s, %s, %s, %d);\n",
			jsString(role), jsString(name), jsString(value), DefaultStepTimeoutMs)
		fmt.Fprintf(b, "    if (ok) {\n")
		emitStepOK(b, idx)
		fmt.Fprintf(b, "    } else {\n")
		emitStepFail(b, idx,
			fmt.Sprintf("\"fill target \" + %s + \" not found within %dms\"", jsString(`"`+name+`"`), DefaultStepTimeoutMs))
		fmt.Fprintf(b, "    }\n")
		fmt.Fprintf(b, "  }\n")
		return nil

	case "extract":
		sel, _ := step.Args["sel"].(string)
		as, _ := step.Args["as"].(string)
		emitStepStart(b, idx, "extract", map[string]any{"sel": sel, "as": as}, step)
		// Capture the element's direct text into a binding and report it to
		// the runner via a `bind` event. The runner (Go) holds the binding
		// — that is what lets it survive a full-page navigation and be
		// interpolated into a later step on the next page.
		fmt.Fprintf(b, "  {\n")
		fmt.Fprintf(b, "    const el = await __waitEl(%s, %d);\n", jsString(sel), DefaultStepTimeoutMs)
		fmt.Fprintf(b, "    if (el) {\n")
		fmt.Fprintf(b, "      const __v = __directText(el);\n")
		fmt.Fprintf(b, "      __notify({type: \"bind\", name: %s, value: __v, step: %d, at: __at()});\n", jsString(as), idx)
		emitStepOK(b, idx)
		fmt.Fprintf(b, "    } else {\n")
		emitStepFail(b, idx,
			fmt.Sprintf("\"extract: selector \" + %s + \" not found within %dms\"", jsString(`"`+sel+`"`), DefaultStepTimeoutMs))
		fmt.Fprintf(b, "    }\n")
		fmt.Fprintf(b, "  }\n")
		return nil

	case "expect_count":
		sel, _ := step.Args["sel"].(string)
		count, _ := step.Args["count"].(int)
		emitStepStart(b, idx, "expect-count", map[string]any{"sel": sel, "count": count}, step)
		fmt.Fprintf(b, "  {\n")
		fmt.Fprintf(b, "    const r = await __waitCount(%s, %d, %d);\n", jsString(sel), count, DefaultStepTimeoutMs)
		fmt.Fprintf(b, "    if (r.ok) {\n")
		emitStepOK(b, idx)
		fmt.Fprintf(b, "    } else {\n")
		emitStepFail(b, idx,
			fmt.Sprintf("\"expect-count \" + %s + \": got \" + r.got + \", want %d\"", jsString(sel), count))
		fmt.Fprintf(b, "    }\n")
		fmt.Fprintf(b, "  }\n")
		return nil

	case "match":
		// Branch on observed DOM state: read the selector's direct text and
		// run the matching arm's assertions. The whole match is one step —
		// it passes if the taken arm's assertions all pass, and fails on a
		// missing selector, a failed arm assertion, or an unmatched value.
		sel, _ := step.Args["sel"].(string)
		lits := make([]string, 0, len(step.Arms))
		for _, a := range step.Arms {
			lits = append(lits, a.Match)
		}
		emitStepStart(b, idx, "match", map[string]any{"sel": sel, "arms": lits}, step)
		fmt.Fprintf(b, "  {\n")
		fmt.Fprintf(b, "    const __el = await __waitEl(%s, %d);\n", jsString(sel), DefaultStepTimeoutMs)
		fmt.Fprintf(b, "    const __v = __el ? __directText(__el) : null;\n")
		fmt.Fprintf(b, "    if (__v === null) {\n")
		emitStepFail(b, idx, fmt.Sprintf("\"match: selector \" + %s + \" not found within %dms\"", jsString(sel), DefaultStepTimeoutMs))
		fmt.Fprintf(b, "    }")
		for _, arm := range step.Arms {
			fmt.Fprintf(b, " else if (__v === %s) {\n", jsString(arm.Match))
			emitArmBody(b, idx, arm)
			fmt.Fprintf(b, "    }")
		}
		fmt.Fprintf(b, " else {\n")
		emitStepFail(b, idx, "\"match: no arm for value '\" + __v + \"'\"")
		fmt.Fprintf(b, "    }\n")
		fmt.Fprintf(b, "  }\n")
		return nil

	case "expect_path":
		path, _ := step.Args["path"].(string)
		emitStepStart(b, idx, "expect-path", map[string]any{"path": path}, step)
		fmt.Fprintf(b, "  {\n")
		fmt.Fprintf(b, "    const ok = await __waitPath(%s, %d);\n", jsString(path), DefaultStepTimeoutMs)
		fmt.Fprintf(b, "    if (ok) {\n")
		emitStepOK(b, idx)
		fmt.Fprintf(b, "    } else {\n")
		emitStepFail(b, idx,
			fmt.Sprintf("\"expect-path: at \" + location.pathname + \", want \" + %s", jsString(path)))
		fmt.Fprintf(b, "    }\n")
		fmt.Fprintf(b, "  }\n")
		return nil

	case "expect_cell":
		name, _ := step.Args["name"].(string)
		want := step.Args["value"]
		// `expect-cell` reads window.__sigil_cells — it requires the
		// Introspect capability, which only a Sigil-native target has. A
		// foreign page has no cell map, so this is a capability violation,
		// not a runtime miss. (Task #3 promotes this to a source-positioned
		// compile diagnostic in lower.)
		if in.External {
			return fmt.Errorf("e2e: `expect-cell` needs a Sigil-native target; %q is external (no cell map). Assert on rendered output (expect-text / expect-count) instead", in.App.Name)
		}
		cellID, ok := in.NameToID[name]
		if !ok {
			// Fall through to literal id — supports list-child cells
			// (cN / rN) addressed by raw id, same convention the
			// legacy runner uses.
			cellID = name
		}
		emitStepStart(b, idx, "expect-cell", map[string]any{"name": name, "value": want}, step)
		// Poll the cell value Playwright-style. Click handlers in the
		// served bundle are `async` (they await op calls), and the
		// DOM `.click()` we just fired doesn't itself await — so a
		// one-shot read here would race the fetch. Polling for the
		// expected value (or timeout) gives async ops time to land
		// without forcing test authors to sprinkle `wait` everywhere.
		fmt.Fprintf(b, "  {\n")
		fmt.Fprintf(b, "    const want = %s;\n", jsLiteral(want))
		fmt.Fprintf(b, "    let got;\n")
		fmt.Fprintf(b, "    const deadline = Date.now() + %d;\n", DefaultStepTimeoutMs)
		fmt.Fprintf(b, "    while (Date.now() < deadline) {\n")
		fmt.Fprintf(b, "      got = window.__sigil_cells && window.__sigil_cells[%s];\n", jsString(cellID))
		fmt.Fprintf(b, "      if (got === want) break;\n")
		fmt.Fprintf(b, "      await new Promise(r => setTimeout(r, 50));\n")
		fmt.Fprintf(b, "    }\n")
		fmt.Fprintf(b, "    if (got === want) {\n")
		emitStepOK(b, idx)
		fmt.Fprintf(b, "    } else {\n")
		emitStepFail(b, idx,
			fmt.Sprintf(`"expect-cell %s: got " + JSON.stringify(got) + ", want " + JSON.stringify(want)`, name))
		fmt.Fprintf(b, "    }\n")
		fmt.Fprintf(b, "  }\n")
		return nil

	case "wait":
		// An explicit fixed pause. Rarely needed (assertions auto-wait), but
		// kept for parity with the source vocabulary.
		ms, _ := step.Args["ms"].(int)
		emitStepStart(b, idx, "wait", map[string]any{"ms": ms}, step)
		fmt.Fprintf(b, "  await new Promise(r => setTimeout(r, %d));\n", ms)
		emitStepOK(b, idx)
		return nil

	case "wait_for":
		role, _ := step.Args["role"].(string)
		name, _ := step.Args["name"].(string)
		emitStepStart(b, idx, "wait-for", map[string]any{"role": role, "name": name}, step)
		fmt.Fprintf(b, "  {\n")
		fmt.Fprintf(b, "    const ok = await __waitForRoleName(%s, %s, %d);\n", jsString(role), jsString(name), DefaultStepTimeoutMs)
		fmt.Fprintf(b, "    if (ok) {\n")
		emitStepOK(b, idx)
		fmt.Fprintf(b, "    } else {\n")
		emitStepFail(b, idx,
			fmt.Sprintf("\"wait-for %s \" + %s + \" not found within %dms\"", role, jsString(`"`+name+`"`), DefaultStepTimeoutMs))
		fmt.Fprintf(b, "    }\n")
		fmt.Fprintf(b, "  }\n")
		return nil

	default:
		return fmt.Errorf("e2e: step verb %q is not yet supported by the compiled-bundle codegen", step.Kind)
	}
}

// emitArmBody emits the assertions of one match arm as a fail-fast
// conjunction: each assertion is checked, the first failure ends the
// step (and the scenario), and reaching the end means the arm — and so
// the match step — passed. The arm is reported under the match's step
// index, not its own, so a match is a single step.
func emitArmBody(b *strings.Builder, idx int, arm ir.StepArm) {
	for _, s := range arm.Steps {
		expr, failExpr := armCheckJS(s)
		fmt.Fprintf(b, "      if (!(%s)) {\n", expr)
		emitStepFail(b, idx, failExpr)
		fmt.Fprintf(b, "      }\n")
	}
	emitStepOK(b, idx)
}

// armCheckJS maps one assertion step (the only kinds lower allows inside a
// match arm) to a JS boolean expression and a JS failure-message
// expression. Reuses the same DOM helpers the top-level verbs use.
func armCheckJS(s ir.Step) (expr string, failExpr string) {
	switch s.Kind {
	case "expect_text":
		text, _ := s.Args["text"].(string)
		return fmt.Sprintf("await __findText(%s, %d)", jsString(text), DefaultStepTimeoutMs),
			fmt.Sprintf("\"match arm: text \" + %s + \" not found\"", jsString(text))
	case "expect_no_text":
		text, _ := s.Args["text"].(string)
		return fmt.Sprintf("await __waitTextGone(%s, %d)", jsString(text), DefaultStepTimeoutMs),
			fmt.Sprintf("\"match arm: text \" + %s + \" still present\"", jsString(text))
	case "expect_count":
		sel, _ := s.Args["sel"].(string)
		count, _ := s.Args["count"].(int)
		return fmt.Sprintf("(await __waitCount(%s, %d, %d)).ok", jsString(sel), count, DefaultStepTimeoutMs),
			fmt.Sprintf("\"match arm: count of \" + %s + \" != %d\"", jsString(sel), count)
	case "expect_path":
		path, _ := s.Args["path"].(string)
		return fmt.Sprintf("await __waitPath(%s, %d)", jsString(path), DefaultStepTimeoutMs),
			fmt.Sprintf("\"match arm: path != \" + %s", jsString(path))
	}
	return "false", "\"match arm: unsupported assertion\""
}

// emitStepStart writes the step_start event with a verb-specific
// payload bag. Centralizing means changing the event schema is a
// one-line edit per direction.
func emitStepStart(b *strings.Builder, idx int, verb string, payload map[string]any, step ir.Step) {
	ev := map[string]any{
		"type":    "step_start",
		"step":    idx,
		"verb":    verb,
		"payload": payload,
		"line":    step.Line,
		"col":     step.Col,
	}
	fmt.Fprintf(b, "  __notify({...%s, at: __at()});\n", jsonObj(ev))
}

// emitStepOK writes the success-path step_end. Indented to match
// the inner block depth callers always write at.
func emitStepOK(b *strings.Builder, idx int) {
	fmt.Fprintf(b, "      __notify({type: \"step_end\", step: %d, ok: true, at: __at()});\n", idx)
}

// emitStepFail writes the failure-path step_end + scenario_end +
// return, short-circuiting the scenario. failureExpr is a JS
// expression that evaluates to the failure-message string; callers
// pass either a quoted literal or a `"…" + var + "…"` concat.
func emitStepFail(b *strings.Builder, idx int, failureExpr string) {
	fmt.Fprintf(b, "      __notify({type: \"step_end\", step: %d, ok: false, failure: %s, at: __at()});\n",
		idx, failureExpr)
	fmt.Fprintf(b, "      await __hold();\n")
	fmt.Fprintf(b, "      __notify({type: \"scenario_end\", ok: false, at: __at()});\n")
	fmt.Fprintf(b, "      return;\n")
}

// jsLiteral renders a Go value as a JS literal. Handles the value
// types `stepExpectedValue` emits (int / int64 / string / bool) and
// falls through to json.Marshal for anything else.
func jsLiteral(v any) string {
	switch x := v.(type) {
	case string:
		return jsString(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case nil:
		return "null"
	}
	out, _ := json.Marshal(v)
	return string(out)
}

// jsonObj serializes a Go map/struct as a JS object literal. Used for
// the bag of step metadata that goes into __notify events; JSON is a
// strict subset of JS object literal syntax so this is safe to splice.
func jsonObj(v any) string {
	out, _ := json.Marshal(v)
	return string(out)
}

// jsString quotes a Go string as a JS string literal using JSON
// quoting rules. JSON's quoting handles all the escapes JS needs for
// a double-quoted string (\n, \\, \", control chars), so this is
// safe for arbitrary text payloads.
func jsString(s string) string {
	out, _ := json.Marshal(s)
	return string(out)
}
