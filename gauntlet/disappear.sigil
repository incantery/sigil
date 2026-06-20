// Gauntlet challenge #4 — disappearance under async.
//
// Hazard: clicking Save shows "Saving…" for ~400ms, then replaces it with
// "Saved". The transient indicator is PRESENT when a naive test would
// assert it gone — a one-shot read right after the click sees it and
// false-fails.
//
// Proves: `expect-no-text` auto-waits for absence. It passes the moment
// the text is gone, not on the first frame, so asserting on content
// that's mid-disappearance waits for the UI to catch up. The bracketing
// `expect-text` calls make the disappearance explicit: present, then
// awaited-gone, then the terminal state.
//
// Foreign target — plain HTML, no Sigil inside.

app Disappear =
  target web
    external
    host "http://localhost:7373/c/disappear/vanilla"

test "a transient status is awaited out, not raced" = scenario in Disappear
  click button "Save"
  expect-text "Saving…"
  expect-no-text "Saving…"
  expect-text "Saved"
