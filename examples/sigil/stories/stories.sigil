// Story declarations: each is a named, compile-checked component
// example. `sigil stories stories.sigil` serves the catalog; every
// story renders as its own isolated document (own cells, own bundle).

component labeled-stat -> caption -> value =
  card
    stack vertical gap=1
      title caption
      text value

component pill-counter -> name -> count =
  stack horizontal gap=1
    text name
    button "-" on click { count -= 1 }
    text count
    button "+" on click { count += 1 }

view App =
  state visits = 12
  stack vertical gap=2
    labeled-stat "Visits" visits
    pill-counter "visits" visits

story "Stat with a value" =
  labeled-stat "Total runs" "1,284"

story "Stat at zero" =
  labeled-stat "Total runs" "0"

story "Counter starting high" =
  state apples = 99
  pill-counter "apples" apples

story "Two counters share nothing" =
  state a = 1
  state b = 2
  stack vertical gap=1
    pill-counter "first" a
    pill-counter "second" b
