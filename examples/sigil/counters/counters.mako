view Counters =
  state items = [0, 0, 0]
  card
    title "Counters"
    for item in items
      stack horizontal gap=1
        button "-" on click { item -= 1 }
        text item
        button "+" on click { item += 1 }
        button "x" on click { items.remove(item) }
    button "+ add counter" on click { items.append(0) }

test "starts with three zero counters" = scenario Counters
  expect-cell c1 0
  expect-cell c2 0
  expect-cell c3 0

test "+ on first counter increments only that counter" = scenario Counters
  click button "+"
  expect-cell c1 1
  expect-cell c2 0
  expect-cell c3 0
  expect-text "1"

test "append adds a new row" = scenario Counters
  click button "+ add counter"
  expect-text "0"

test "minus button updates the rendered text" = scenario Counters
  click button "-"
  expect-cell c1 -1
  expect-text "-1"
