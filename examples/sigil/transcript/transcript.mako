view Transcript =
  state turns = []
    user : Bool = false
    iris : Bool = false
    text : String = ""
  state draft = ""
  card
    title "Transcript"
    stack horizontal gap=1
      input draft placeholder="Say something"
      button "Send" tone=primary on click { turns.append(true, false, draft); turns.append(false, true, "noted."); draft = "" }
    for m in turns
      stack gap=1
        if m.user
          card tone=primary align=end maxwidth=480 radius=xl
            text m.text
        if m.iris
          stack horizontal gap=1 align=start
            text m.text
            button "!" radius=full on click { m.text = "noted!" }

test "user and iris turns render in their own branches" = scenario Transcript
  fill input"Say something" "hello there"
  click button "Send"
  expect-text "hello there"
  expect-text "noted."
  expect-cell draft ""

test "handler inside a row branch mutates and re-renders the row" = scenario Transcript
  fill input"Say something" "hi"
  click button "Send"
  click button "!"
  expect-text "noted!"
