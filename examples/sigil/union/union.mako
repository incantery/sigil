// Discriminated unions + exhaustive `match`. `Fetch` is a Rust-flavored
// tagged union: `loaded` / `failed` carry a String payload, `idle` /
// `loading` are unit variants. `match` must handle every variant — drop
// an arm and the compiler refuses to build.
type Fetch =
  | idle
  | loading
  | loaded : String
  | failed : String

view Union =
  state status : Fetch
  state note = "the answer is 42"
  card
    title "Fetch status"
    stack horizontal gap=1
      button "load" on click { status = loading }
      button "ok" tone=primary on click { status = loaded(note) }
      button "fail" on click { status = failed("network down") }
      button "reset" on click { status = idle }
    match status
      | idle
        text "Press load to begin"
      | loading
        text "Loading…"
      | loaded as body
        card tone=primary
          text body
      | failed as err
        text err

test "match starts on the idle arm" = scenario Union
  expect-text "Press load to begin"
  expect-no-text "Loading…"

test "transitions swap arms and bind the payload" = scenario Union
  click button "load"
  expect-text "Loading…"
  expect-no-text "Press load to begin"
  click button "ok"
  expect-text "the answer is 42"
  expect-no-text "Loading…"
  click button "fail"
  expect-text "network down"
  expect-no-text "the answer is 42"
  click button "reset"
  expect-text "Press load to begin"
