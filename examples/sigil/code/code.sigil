// Demonstrates the `code` primitive: a verbatim monospace block whose body
// is captured raw — braces, `=`, and `${…}` all survive untouched.
view CodeDemo =
  stack vertical gap=2 padding=4
    title "The code primitive"
    text "A counter, written in Sigil:"
    code
      view Counter =
        state count = 0
        card
          title "Counter"
          stack horizontal gap=1
            button "-" on click { count -= 1 }
            text "value: ${count}"
            button "+" on click { count += 1 }
    text "The interpolation above is literal — code is never interpolated."
