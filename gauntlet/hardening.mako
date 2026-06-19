// Gauntlet hardening — automation hazards a robust suite must survive.
// Three foreign pages, each a distinct stress on the runner.

// --- Shadow DOM: the target lives inside an open shadow root, which
// querySelectorAll does not cross. Proves the text matchers pierce open
// shadow roots (__allEls). ---
app Shadow =
  target web
    external
    host "http://localhost:7373/c/shadow/vanilla"

test "text and buttons inside an open shadow root are reachable" = scenario in Shadow
  click button "Reveal"
  expect-text "Revealed from the shadow"

// --- Debounce: results update only 300ms after the last keystroke. A
// one-shot read races the timer; auto-wait rides it out. ---
app Debounce =
  target web
    external
    host "http://localhost:7373/c/debounce/vanilla"

test "debounced results are awaited, not read on the keystroke" = scenario in Debounce
  fill input "Search" "shoes"
  expect-text "Results for: shoes"

// --- Race: two regions settle at different times. Asserting on the
// SLOWER one first proves the runner waits for the laggard regardless of
// which lands first. ---
app Race =
  target web
    external
    host "http://localhost:7373/c/race/vanilla"

test "out-of-order async updates are each awaited" = scenario in Race
  click button "Start"
  expect-text "Beta ready"
  expect-text "Alpha ready"
