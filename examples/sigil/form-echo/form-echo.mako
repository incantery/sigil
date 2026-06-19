view FormEcho =
  state name = ""
  card
    title "Form echo"
    input name placeholder="type your name"
    text "hello, ${name}"

test "starts empty" = scenario FormEcho
  expect-cell name ""

test "typing updates the cell" = scenario FormEcho
  fill input"type your name" "Sigil"
  expect-cell name "Sigil"

test "bound text reflects the cell" = scenario FormEcho
  fill input"type your name" "world"
  expect-text "hello, world"
