// Gauntlet challenge #2 — cross-page navigation with a carried value.
// THE KEYSTONE.
//
// Hazard: clicking Sign in performs a real full-page navigation. The
// document is torn down and rebuilt — which destroys a naively injected
// test bundle. Steps after the navigation (asserting on the new page)
// never run unless the runner survives the reload.
//
// Proves three things at once:
//   1. The session outlives the page — the runner re-establishes itself
//      on the new document and the scenario continues across the nav
//      (the R14 gap, closed here in-repo for the first time).
//   2. `extract … as <name>` captures a page-owned value (the account
//      number) that the test did NOT hard-code; the binding lives
//      runner-side, so it survives the navigation.
//   3. `expect-path` asserts the navigation actually landed, and the
//      carried binding interpolates into an assertion on the new page.
//
// Foreign target — plain HTML login + dashboard, no Sigil inside.

app CrossPage =
  target web
    external
    host "http://localhost:7373/c/cross-page/vanilla"

// A parameterized flow: the sign-in sequence, reusable with any email.
// The `email` param substitutes into the body at compile time.
flow signInWith -> email =
  fill input "Email" email
  click button "Sign in"

test "a value read before a full-page nav is asserted after it" = scenario in CrossPage
  extract text-of "#acct" as account
  signInWith "ada@acme.io"
  expect-path "/c/cross-page/dashboard"
  expect-text "Signed in as ada@acme.io"
  expect-text "Account ${account}"
