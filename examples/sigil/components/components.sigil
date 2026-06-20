component named-counter -> name -> count =
  stack horizontal gap=1
    text name
    button "-" on click { count -= 1 }
    text count
    button "+" on click { count += 1 }

component my-card -> heading -> *body =
  card
    title heading
    *body

view App =
  state apples = 5
  state oranges = 3
  state bananas = 2
  my-card "Counters"
    named-counter "apples" apples
    named-counter "oranges 1" oranges
    named-counter "bananas 1" bananas
    named-counter "oranges 2" oranges
    named-counter "bananas 2" bananas
