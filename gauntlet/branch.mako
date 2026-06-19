// Gauntlet challenge #3 — branch on app state (+ `flow` composition).
//
// Hazard: the page renders ONE of several states (chosen here by ?plan=),
// and the right assertion depends on which. A scenario that hard-asserts
// one banner is brittle against the others.
//
// Proves: `match` in a scenario body — branch on an observed value, run
// the matching arm's assertions. Same `|`-arm shape as L93's union match,
// reused for the exogenous mood; arms here are string literals and carry
// their own nested steps.
//
// Also proves `flow`: the match block is a NAMED, reusable sequence both
// scenarios invoke by name — the composition primitive that makes named
// flows the shared vocabulary. A flow inlines at compile time (the
// component machinery, pointed at test verbs), so the runner sees plain
// steps. Two targets drive two states; both arms are exercised.
//
// Foreign target — plain HTML, no Sigil inside.

app PlanPro =
  target web
    external
    host "http://localhost:7373/c/branch/vanilla?plan=pro"

app PlanFree =
  target web
    external
    host "http://localhost:7373/c/branch/vanilla?plan=free"

// The branch logic, written once.
flow checkPlanBanner =
  match text-of "#plan"
    | "pro"
      expect-text "Pro features unlocked"
    | "free"
      expect-text "Upgrade to Pro"
    | "enterprise"
      expect-text "Contact your account manager"

test "the pro plan shows the unlocked banner" = scenario in PlanPro
  checkPlanBanner

test "the free plan shows the upgrade banner" = scenario in PlanFree
  checkPlanBanner
