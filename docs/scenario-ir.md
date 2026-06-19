# Scenario IR — the exogenous mood of Sigil

*Status: in progress. The seed was `ir.Test` / `ir.Step` + the
`pkg/codegen/e2e` bundle emitter; this doc is their generalization into a
first-class, AI-native browser-automation surface that shares Sigil's
substrate.*

**Landed so far** (see `../gauntlet/`):
- The **gauntlet** harness — foreign (Sigil-free) reference pages driven
  by Sigil scenarios, proving the suite is framework-agnostic. The full v0
  gauntlet is green: #1 (auto-wait), #2 (cross-page, the keystone),
  #3 (branch on app state), #4 (disappearance), #5 (capability contrast).
- **`match` in scenarios** (challenge #3): `match text-of "<sel>"` branches
  on an observed value and runs the matching arm's assertions — the same
  `|`-arm parse shape as L93 union match, extended to string-literal arms.
  Arms hold their own nested steps (`ir.Step.Arms` / `ir.StepArm`) — the
  first concrete piece of the "a step contains steps" IR generalization.
  tree-sitter + corpus updated (parity held).
- **`flow`** (§3) — the named composition primitive, the test-mood analog
  of `component`. `flow name -> param =` declares a reusable step
  sequence; invoking it by name inlines its body (param → arg
  substitution) at lower time, **reusing the exact component inlining
  machinery** (`substChildren`). The runner is unchanged — it sees a flat
  step list. Recursion-guarded; flows compose (flow-calls-flow); verb
  shadowing rejected. `branch.mako` shares one `match` flow; `cross-page`
  uses a parameterized `signInWith`. tree-sitter `flow_decl` + highlights
  + corpus. This is the cleaner half of the §1–3 generalization.
- **OTel spine** (§6) — a run is a trace: scenario = root span, step =
  child span, with status, timing, intent attributes, and `extract`
  captures as `bind` span events. `pkg/trace` is a dependency-free model +
  OTLP/JSON serializer (no OpenTelemetry SDK); `internal/cli` records spans
  off the existing event stream. `sigil test --trace <file>` writes
  OTLP/JSON (the AI's queryable artifact); `--otlp <url>` POSTs to any
  OTLP/HTTP collector (Tempo/Grafana-ready); `--trace-lean` is the
  prod-lean verbosity. Optional, vendor-neutral, off by default. Batteries:
  a `Tiltfile` + `local-k8s/lgtm.yaml` run `grafana/otel-lgtm` via Tilt
  (k8s port-forwards work with a remote docker host, where compose can't);
  `make gauntlet-suite` runs the suite, pushes traces, prints a Grafana URL.
- **Foreign targets** via an `external` flag: native-only checks
  (root-owns-viewport layout invariants) are suppressed.
- **Navigation continuity** (§5): the runner injects the scenario in
  segments and re-establishes itself on the new document after a full-page
  navigation — **closing the R14 e2e gap in-repo**. Verbs: `extract
  text-of "<sel>" as <name>` (binding held runner-side, survives the nav,
  interpolates as `${name}`) and `expect-path`.
- **Capability gate in `lower`** (§4): a verb's required capability is
  checked at compile time. `expect-cell` (Introspect) against an `external`
  (Observe-floor) target is a source-positioned compile error, not a
  runtime miss — locked by `TestCapabilityGate`. `expect-count` is the
  Observe-floor cardinality verb that works against any target.

**Runner unification (done)**: the legacy `scenario <View>` path
(`runOneTest`/`runStep`, inline per-step `chromedp.Evaluate`) is gone. Both
target shapes funnel through `runScenarioAt` — view-target serves the view
as a SPA on an embedded server and drives it `external=false`; app-target
resolves the host from config. One segmented driver, one verb vocabulary
(the bundle emitter gained `wait`/`wait-for`), full event + trace coverage
for view tests too.

**Not yet built**: first-class `Mood`/`Cap` IR (Arms is the only
nested-step piece so far; flow inlines at the AST level rather than being
modeled in IR). Match v0 is assertions-only inside arms (no
drive/extract/nav in a branch) and reports as one step; granular per-arm
steps can follow.

## Thesis

Sigil is **one language with one substrate, expressed in two verb moods.**

- **Endogenous** (the app) — statements that mutate the system's *own*
  world: `count = count + 1`, `CreateAgent(...)`, `<-`, `navigate`.
- **Exogenous** (the test) — statements that drive the system *from the
  outside, as a user would*, and observe it with a verdict: `click`,
  `fill`, `expect-text`, `extract`.

They share everything below the verb line — the value/expression
language, the type system (records, discriminated unions, optionals),
`match` exhaustiveness, string interpolation, and the named-composable-
sequence primitive. They diverge only in their instruction sets, because
they stand on opposite sides of the same interface:

> The app is **what the system does.** The test is **what a user does to
> it and expects back.** Same world, same types, same composition,
> opposite side of the interface. Implementation and specification in one
> language.

This is the same split Sigil already runs between the UI IR and the
contract IR (R12): carved apart because they move at different speeds,
joined because they share types. The scenario verbs are a third member
of that family.

---

## 1. The shared spine: `Session -> Result<Session>`

Every exogenous verb compiles to a **Step**: a function from a live
session to a result.

```
Session = {
  drive:  Channel             // page handle (CDP today); capability-scoped
  binds:  map[Name]Value      // values captured by `extract ... as name`
  cap:    Capability          // what verbs are legal against this target
  span:   SpanContext         // OTel: the step's place in the trace
}

Result<Session> = | ok   : Session
                  | fail : Failure        // <- the existing L93 union machinery

Step = Session -> Result<Session>
```

Sequencing is **railway-oriented**: short-circuit on the first failure.
This is just `match` over the `Result` union — the exact primitive the app
side uses for op returns, reused verbatim.

```
seq(a, b) = s =>
  match a(s)
    | ok   as s2  -> b(s2)
    | fail as f   -> fail(f)
```

`Failure` is a record, and it is the **AI-consumable artifact** — not a
screenshot, structured data:

```
Failure = {
  step:     StepRef           // verb + source position (file:line:col)
  reason:   String            // "text 'Done' not found within 5000ms"
  expected: String
  actual:   String
  snapshot: SemanticSnapshot  // accessibility-tree view of the page at failure
  net:      [NetEvent]        // requests in flight / completed
  at:       Duration
}
```

Crucially, the Session lives **Go-side, not in page JS.** That is what
lets it survive a full-page navigation (see §5) — the page bundle can be
destroyed and re-injected while the Session value, including every
extracted binding, persists.

---

## 2. The verb split

Same statement-sequence shape as an app handler (which already lowers to a
`sequence` action, L8). Different `Mood`, different `Kind` set, different
World type.

### Substrate (shared — no mood)
- expressions, literals, refs, interpolation
- `match` / branching over unions
- `as name` bindings
- the named-composable-sequence primitive (`component` ≅ `flow`, §3)

### Endogenous verbs (the app — already shipped)
`assign` · `op-call` · `<-` stream-into · `then navigate` · list
`.append`/`.remove` · `navigate`

### Exogenous verbs (the test — the new set)

| Group | Verbs | Required capability |
|---|---|---|
| **Drive** | `click <role> "name"`, `fill <field> "ph" "v"`, `type`, `press <key>`, `hover`, `select`, `upload` | Observe (floor) |
| **Observe / assert** | `expect-text`, `expect-no-text`, `expect-path`, `expect-count`, `expect-visible`/`-hidden`, `expect-attr` | Observe (floor) |
| **Introspect (assert)** | `expect-cell`, `expect-op-called`, `expect-route` | **Introspect (native only)** |
| **Extract** | `extract text-of <sel> as n`, `extract attr <a> of <sel> as n`, `extract path as n` | Observe (floor) |
| **Synchronize** | `wait <dur>`, `wait-for <predicate>` (mostly implicit — every observe auto-waits to a deadline) | Observe (floor) |
| **Navigate-and-continue** | `visit "/path"`, or a `click` that triggers full-page nav | Observe (floor) |

The two moods lower to the **same node shape** — the difference is data,
not a separate IR:

```
StepNode = {
  Mood:     Exogenous | Endogenous | Neutral
  Kind:     string                 // "click" | "fill" | "expect_text" | "extract" | "match" | "call_flow"
  Args:     map[Name]Value
  Cap:      Capability             // required target capability; default = Observe
  Children: [StepNode]             // match arms, nested flow bodies
  Pos:      Pos
}
```

`ir.Step` today is exactly this minus `Mood`, `Cap`, and `Children`.

---

## 3. `flow` — the unified composition primitive

A `flow` is to the exogenous mood what `component` is to the endogenous
mood: a **named, parameterized, composable sequence.** Implement it *as
the same mechanism* (L7 inlining), just producing a `Step` value instead
of UI nodes.

```
flow login -> user -> pass =
  visit "/login"
  fill input "Email" user
  fill input "Password" pass
  click button "Sign in"

flow archiveFirstAgent =
  click text first-agent-name
  click button "Archive"
```

A flow **is** a Step, so flows compose — by sequence (newline) or, where
an inline read is clearer, by pipe:

```
scenario "archived agent leaves the list" in Studio =
  login("admin@acme.io", "hunter2")
  |> archiveFirstAgent
  expect-no-text "Researcher"
```

```
ir.Flow = { Name: string, Params: [Param], Body: [StepNode], Pos: Pos }
ir.Scenario = { Name: string, Target: TargetRef, Body: [StepNode], Pos: Pos }
```

`ir.Test` becomes `ir.Scenario`. The body is a flow; the difference is a
Scenario is bound to a target and is the top-level *reported* unit (one
trace, §6).

**Surface note.** The newline-under-block sequencer *is* the pipe — it
already threads the Session implicitly and is the most AI-legible form.
Explicit `|>` earns its place only for inline sub-flow composition. Keep
the surface flat (named steps + `as` bindings); resist point-free
combinator soup — flat pipelines are what an AI emits reliably.

---

## 4. Capability gating — targets as types

A target's capability is **resolved from its declaration** and verbs
declare the capability they require. Using an Introspect verb against an
Observe-only target is a **compile error**, not a runtime undefined.

```
app Studio = target web host "http://localhost:9090"   // native  → Observe + Introspect
app Acme   = external host "https://acme.example"        // foreign → Observe only
```

The lattice (small on purpose):

```
Observe     ⊑ Introspect
  │              │
  │ any URL:     │ sigil-compiled only:
  drive,         expect-cell, expect-op-called,
  semantic       expect-route, deterministic-clock hooks
  assertions
```

- **Foreign** (React/Vue/anything): the Observe floor — drive + semantic
  assertions + extract + navigate. The whole automation suite works here;
  this is the "valuable even if your frontend is React" claim, and it's a
  *narrower capability set the type system already knows how to express*,
  not a bolt-on.
- **Native** (sigil-compiled): Observe **+** Introspect. `expect-cell`
  reaches the real cell map; `expect-op-called` knows the contract IR;
  `expect-route` knows the router. This is the superpower you only get by
  testing the same language from the other side.

```
scenario … in Studio:  expect-cell agents 3       // ✓ compiler knows the cell id
scenario … in Acme:    expect-cell agents 3       // ✗ compile error:
                                                    //   `expect-cell` needs Introspect;
                                                    //   target Acme is external (Observe only).
                                                    //   Use `expect-count` on the rendered list.
```

This is *the* place Sigil's type discipline buys something Playwright
structurally cannot: the available vocabulary is a typed function of what
is known about the target.

---

## 5. Navigation continuity — closing the R14 gap

The reason `navigate` (R14) has no e2e today: the runner injects an IIFE +
`__sigilNotify` binding, and a full-page `location.assign` destroys both
mid-scenario. The functional model fixes this *by construction*:

- The Session lives Go-side; bindings survive the reload.
- The runner re-establishes the drive channel on every new document
  (`Page.addScriptToEvaluateOnNewDocument`) instead of one post-navigate
  inject.
- The flow simply **continues at the next step** after the new document is
  ready.

```
flow signsUpAndLands =
  visit "/signup"
  fill input "Email" "new@acme.io"
  click button "Create account"        // full-page nav to /welcome
  extract text-of ".user-greeting" as greeting
  expect-path "/welcome"
  expect-text "Welcome, new@acme.io"
```

`extract ... as greeting` on the *post-nav* page works because the Session
that holds `binds` is the same value threaded across the reload. Multi-
page flows — the entire auth workstream — become expressible *and*
testable. This is the concrete payoff of "the Session outlives the page."

---

## 6. OTel mapping — the telemetry spine (pillar 3)

The `Session -> Result` shape *is* a trace; no separate observability
design needed.

| Scenario concept | OTel |
|---|---|
| Scenario | root span (one trace) |
| Flow | child span grouping its steps |
| Step (verb) | span; verb + args = attributes |
| `ok` | span status OK |
| `fail` | span status ERROR; `Failure` record = attributes + events |
| `extract … as n` | span event recording the binding |
| `SemanticSnapshot` on failure | span attribute / linked log |

Two **modes**, same instrument at two verbosities:

- **local-test**: full semantic snapshot per step, network + console
  capture, every span.
- **prod-lean**: sampled, errors-only — the *same flows* can run as
  synthetic monitors in prod, leaner. Test observability and prod
  observability become one vocabulary.

Exporter is **pluggable** (file/OTLP out). Grafana/LGTM is a
*batteries-included, removable* consumer: a committed Tiltfile +
`grafana/otel-lgtm` manifest (Tilt port-forwards survive a remote docker
host). Works headless with no
Grafana; lights up beautifully with it.

---

## 7. The conformance gauntlet (pillar 2, TDD-first)

The gauntlet **defines the product by proving it.** Each challenge is a
reference page exhibiting one automation hazard, shipped in **vanilla /
React / sigil** variants, with a flow that must go green against all
three (Observe floor) — that is how "framework-agnostic" becomes a fact,
not a claim. Write the page + the desired flow + result *before* the
runner can pass it.

| # | Challenge | Proves | Shape |
|---|---|---|---|
| 1 | **Async appear** — content mounts after a delay | auto-wait (no explicit sleeps) | `click "Load" ⇒ expect-text "Done"` |
| 2 | **Cross-page + carried value** — login, extract, full-page nav, assert on new page | Session survives reload; binding threading (§5) | the `signsUpAndLands` flow |
| 3 | **Branch on app state** — page renders one of N variants | branching reuses L93 `match` | `match (extract …) \| ok -> … \| down -> …` |
| 4 | **Disappearance under async** — a toast auto-dismisses | `expect-no-text` waits it out, not first-frame | `click "Save" ⇒ expect-no-text "Saving…"` |
| 5 | **Capability contrast** — same assertion, native vs foreign | the §4 compile-time gate + the agnostic floor | `expect-cell` (Studio) vs `expect-count` (Acme) |

Future gauntlet (don't build yet, but reserve the slots): iframe / shadow-
DOM boundary crossing, debounced input, virtualized list, focus-trap
modal, file upload, scroll-into-view, two racing async updates.

---

## Build order (TDD)

1. **Gauntlet first** (§7 1–5, vanilla variants) — the executable
   definition of the vocabulary. Forces the verbs to be designed against
   real hazards.
2. **Scenario IR + `flow`** (§1–3) — generalize `ir.Test`/`ir.Step`; add
   `Mood`, `Cap`, `Children`, the `Result` short-circuit, `flow` as the
   shared composition primitive.
3. **Capability gating** (§4) — `external host` app form; verb capability
   requirements; the compile-time gate + its diagnostic.
4. **Navigation continuity** (§5) — re-inject-on-new-document; Session-
   survives-reload; unifies the two runners onto one path while we're in
   there.
5. **OTel spine** (§6) — span emission from the existing event stream;
   in-memory exporter; the Grafana dashboards as a committed artifact.

Editor-tooling parity (tree-sitter + LSP) rides each surface change, per
the standing rule.
