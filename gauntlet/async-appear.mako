// Gauntlet challenge #1 — async appear.
//
// Hazard: the result mounts ~400ms AFTER the click. A test that asserts
// immediately must auto-wait; a one-shot DOM read races the timer and
// false-fails.
//
// Proves: auto-wait. `expect-text` polls to a deadline, so the scenario
// asserts with no explicit sleep and still passes.
//
// The target is a FOREIGN page — plain HTML with no Sigil inside it,
// served by the gauntlet server. This is the framework-agnostic claim
// proven on the simplest hazard: the very same scenario vocabulary that
// drives a Sigil app drives a vanilla page it never compiled.

app AsyncAppear =
  target web
    external
    host "http://localhost:7373/c/async-appear/vanilla"

test "content that mounts after a delay is awaited, not raced" = scenario in AsyncAppear
  click button "Load"
  expect-text "Done"
