// std/html — a semantic-neutral element layer over the kernel's host
// intrinsics. These are the thinnest possible wrappers: they give the DOM
// primitives ordinary names so higher layers (std/ui) never touch `__`-builtins.

// el builds an element node from a tag, its attributes, and its children.
pub let el tag attrs kids = __elem tag attrs kids

// dynText is a reactive text node: the thunk re-runs (and the text updates)
// whenever a cell it read changes.
pub let dynText f = __text f

// attr is a static attribute.
pub let attr name v = __attr name v

// bindAttr is a reactive attribute, recomputed from a thunk on dependency change.
pub let bindAttr name f = __bindAttr name f

// onClick runs an effectful handler when the element is clicked.
pub let onClick f = __on "click" (fun e -> effect { f () })

// onInput runs a handler with the input's decoded string value on every input
// event. The decode happens at fire time (inside the deferred effect), so the
// handler always sees the field's current text.
pub let onInput f = __on "input" (fun e -> effect { f (__eventValue e) })

// each renders one node per element of a reactive list, reconciling nodes as the
// list changes (keyed by value, with node reuse).
pub let each src render = __each src render

// mount attaches a node under the element matched by a CSS selector. The mount
// effect is run immediately (build-and-run), so callers use it as a plain
// function.
pub let mount node sel = (effect { __mount node sel }) ()
