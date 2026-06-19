package html

import _ "embed"

// resetCSS is the headless-primitive baseline. Emitted once per
// document at the top of <style>, before theme vars and Spec-derived
// atomic classes. See reset.css for the authoring rule.
//
//go:embed reset.css
var resetCSS string

// structuralCSS is the renderer's per-primitive geometry + behavior
// (icon wrapper, bar track/fill, button interactions, divider line,
// page gutter). These rules are HTML-target implementation details,
// not design tokens — see structural.css for the authoring rule.
//
//go:embed structural.css
var structuralCSS string
