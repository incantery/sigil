view Counter =
  state count = 0
  card
    title "Counter!!!"
    stack horizontal gap=1
      button "-" on click { count = count - 1 }
      text count
      button "+" on click { count = count + 1 }
      button "reset" on click { count = 0 }

test "+ increments count" = scenario Counter
  click button "+"
  expect-cell count 1
  expect-text "1"

test "- decrements count" = scenario Counter
  click button "-"
  expect-cell count -1

test "reset returns to zero" = scenario Counter
  click button "+"
  click button "+"
  click button "+"
  expect-cell count 3
  click button "reset"
  expect-cell count 0
