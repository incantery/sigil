view Disclosure =
  state expanded = false
  state count = 0
  card
    title "Disclosure"
    stack horizontal gap=1
      button "toggle" on click { expanded = !expanded }
      text "count: ${count}"
      button "+" on click { count += 1 }
    if expanded
      stack gap=1
        text "Hidden content revealed!"
        text "count is bound here too: ${count}"

test "starts collapsed" = scenario Disclosure
  expect-cell expanded false
  expect-cell count 0

test "toggle reveals content" = scenario Disclosure
  click button "toggle"
  expect-cell expanded true
  expect-text "Hidden content revealed!"

test "toggle twice hides content" = scenario Disclosure
  click button "toggle"
  click button "toggle"
  expect-cell expanded false

test "count updates inside and outside the if" = scenario Disclosure
  click button "+"
  click button "+"
  click button "+"
  expect-cell count 3
  expect-text "count: 3"
  click button "toggle"
  expect-text "count is bound here too: 3"
