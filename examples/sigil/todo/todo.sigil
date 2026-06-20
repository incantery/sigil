view Todos =
  state items = []
    label : String
    done  : Bool = false
  state draft = ""
  card
    title "Todos"
    stack horizontal gap=1
      input draft placeholder="What needs doing?"
      button "Add" tone=primary on click { items.append(draft); draft = "" }
    for item in items
      stack horizontal gap=1
        button "toggle" on click { item.done = !item.done }
        if item.done
          text "✓" tone=success
        text item.label
        button "✕" tone=danger on click { items.remove(item) }

test "starts empty" = scenario Todos
  expect-cell draft ""

test "Add appends a row and clears the input" = scenario Todos
  fill input"What needs doing?" "buy milk"
  expect-cell draft "buy milk"
  click button "Add"
  expect-cell draft ""
  expect-text "buy milk"

test "toggle reveals the checkmark" = scenario Todos
  fill input"What needs doing?" "ship sigil"
  click button "Add"
  click button "toggle"
  expect-text "✓"
