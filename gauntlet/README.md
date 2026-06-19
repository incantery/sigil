# The Sigil automation gauntlet

A conformance suite that **defines the AI-native browser-automation
product by proving it.** Each challenge is a reference page exhibiting one
automation hazard. A Sigil `scenario` drives it; the challenge is "done"
only when the scenario goes green.

Two things make this a gauntlet rather than a demo:

1. **Framework-agnostic by proof.** Every challenge ships in `vanilla`,
   and (as they land) `react` / `sigil` variants of the *same* page. The
   identical Observe-floor scenario must pass against all variants — that
   is how "works regardless of your frontend" becomes a fact instead of a
   claim. The pages under `server/pages/<challenge>/<variant>.html` are
   plain frontends with **no Sigil inside them**.

2. **TDD / capability-first.** A challenge is written page-first: the
   hazard page and the desired scenario exist before the runner can pass
   them. The set of challenges *is* the executable definition of the
   automation vocabulary.

See `../docs/scenario-ir.md` for the design this implements.

## Layout

```
gauntlet/
  server/                 a tiny static server for the reference pages
    main.go
    pages/<challenge>/<variant>.html
  <challenge>.mako       the Sigil scenario(s) that drive the challenge
```

## Running

```sh
# the whole suite, self-contained (boots + tears down the page server):
make gauntlet-test

# the suite WITH tracing → review the run in Grafana afterward:
make observability-up          # optional: brings up Tempo + Grafana
make gauntlet-suite            # runs every challenge, pushes traces if Tempo is
                               # up, and prints a Grafana review URL on completion
make observability-down

# or drive one file directly (compiles + runs the whole package):
go run ./gauntlet/server &
sigil test gauntlet/async-appear.mako --trace run.json
```

`gauntlet-suite` works with no observability stack — it always writes the
trace artifact and only pushes to Tempo / prints the Grafana URL when the
collector is reachable on `:4318`. See `../docs/observability/`.

## Challenges

| # | File | Hazard | Proves |
|---|------|--------|--------|
| 1 | `async-appear.mako` | content mounts after a delay | auto-wait (no sleeps) |
| 2 | `cross-page.mako` | full-page navigation tears down the document | session outlives the page; `extract`+carried binding survives the nav; `expect-path` (the R14 e2e gap, closed) |
| 3 | `branch.mako` | the page renders one of several states | `match text-of "<sel>"` branches on an observed value and runs the matching arm's assertions — the same `\|`-arm shape as L93 union match, reused for scenarios |
| 4 | `disappear.mako` | transient indicator that auto-dismisses | `expect-no-text` waits for absence, not first frame |
| 5 | `capability.mako` | a foreign list with no cell map | the vocabulary is typed to the target: `expect-count` (Observe floor) works; `expect-cell` (Introspect) is a **compile error** against an external target |

Hardening hazards (`hardening.mako`):

| Hazard | Page | Proves |
|--------|------|--------|
| Open shadow DOM | `shadow/vanilla` | the text matchers **pierce open shadow roots** (`__allEls`) — `querySelectorAll` alone stops at the boundary |
| Debounced input | `debounce/vanilla` | results 300ms after the last keystroke are awaited, not read on the keystroke |
| Racing async updates | `race/vanilla` | the slower region, asserted first, is awaited regardless of which lands first |

Still to add (see `../docs/scenario-ir.md` §7): iframe boundaries,
virtualized lists, focus-trap modals, file upload.

## Verbs in play

`click` · `fill` · `expect-text` · `expect-no-text` · `expect-path` ·
`expect-count "<selector>" <n>` · `extract text-of "<selector>" as <name>`
(binding interpolates as `${name}` in later steps, surviving a full-page
navigation) · `match text-of "<selector>"` with `| "value"` arms of
assertions · `expect-cell` (Sigil-**native** targets only — refused at
compile time against an `external` target; locked by `TestCapabilityGate`
in `pkg/lang/lower`).

## Composition — `flow`

A `flow` is a named, reusable sequence of steps (the test-mood analog of a
`component`), inlined at compile time so the runner sees plain steps:

```
flow signInWith -> email =
  fill input "Email" email
  click button "Sign in"

test "…" = scenario in App
  signInWith "ada@acme.io"
  expect-path "/dashboard"
```

`branch.mako` shares one `match` flow across both its scenarios;
`cross-page.mako` uses a parameterized `signInWith`. Flows are the unit
of reuse — the shared vocabulary the manager reads and the agent composes.
