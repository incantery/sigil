// std/style — typed styling utilities over the kernel's __style primitive.
//
// The design system IS the type system: spacing utilities take a `Space`, color
// utilities take a `Color`. `p S4` type-checks; `p Sky` is a compile error. A
// utility folds to a constant at build time, so the compiler hoists it to an
// atomic CSS class (deduplicated, dead-code-free); anything dynamic falls back to
// an inline style. `raw` is the escape hatch for bespoke property/value pairs.

// --- spacing scale ---
pub type Space = S0 | S1 | S2 | S4 | S8

let space s =
  match s with
  | S0 -> "0"
  | S1 -> "0.25rem"
  | S2 -> "0.5rem"
  | S4 -> "1rem"
  | S8 -> "2rem"

// --- color palette ---
pub type Color = Slate | Sky | Rose | Ink

let color c =
  match c with
  | Slate -> "#64748b"
  | Sky -> "#38bdf8"
  | Rose -> "#f43f5e"
  | Ink -> "#0f172a"

// --- utilities ---
pub let p s = __style "padding" (space s)
pub let gap s = __style "gap" (space s)
pub let radius s = __style "border-radius" (space s)
pub let bg c = __style "background-color" (color c)
pub let fg c = __style "color" (color c)

// raw is the escape hatch: any CSS property and value, verbatim.
pub let raw prop val = __style prop val
