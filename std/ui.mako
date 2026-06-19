// std/ui — semantic UI components, written in mako over std/html. This is the
// whole point of the kernel redesign: `card`, `column`, `button`, `text` are
// ordinary library functions, not compiler-built-in `ir.Kind`s. New components
// are added here, in mako — never by changing the compiler.

import "std/html" (el, dynText, attr, onClick, onInput)

// text is a static text leaf.
pub let text s = dynText (fun () -> s)

// label is a reactive text leaf driven by a thunk (e.g. one that reads a cell).
pub let label f = dynText f

// column lays its children out vertically.
pub let column kids =
  el "div" [ attr "style" "display:flex;flex-direction:column;gap:8px;align-items:flex-start" ] kids

// row lays its children out horizontally.
pub let row kids =
  el "div" [ attr "style" "display:flex;flex-direction:row;gap:8px;align-items:center" ] kids

// card is a bordered surface around its children.
pub let card kids =
  el "div" [ attr "style" "border:1px solid #ccc;border-radius:8px;padding:16px" ] kids

// button is a clickable button with a static text label and a click handler.
pub let button lbl click =
  el "button" [ onClick click ] [ text lbl ]

// input is an (uncontrolled) text field; write is called with the field's
// current value on every keystroke.
pub let input write =
  el "input" [ onInput write ] []
