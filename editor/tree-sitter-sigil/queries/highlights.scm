;; Highlight queries for Sigil (L88 surface).
;;
;; Capture names follow nvim-treesitter / Helix conventions. Editors that
;; rename groups (e.g. `@keyword.control` → `keyword.control`) handle that
;; on their side. Builtin component names below must stay in sync with
;; lower.BuiltinKinds() — TestHighlightsCoverBuiltinKinds enforces it.

;; ───────────── Keywords ────────────────────────────────────────────────

[
  "view"
  "app"
  "component"
  "flow"
  "theme"
  "state"
  "test"
  "story"
  "type"
  "query"
  "command"
  "stream"
  "session"
  "extends"
  "on"
  "text"
  "italic"
  "caps"
  "tracking"
  "url"
  "auth"
  "token"
  "from"
  "invalidates"
  "backend"
] @keyword

[
  "import"
  "as"
  "icons"
  "fonts"
] @keyword.import

[
  "if"
  "for"
  "in"
  "match"
  "as"
  "navigate"
  "then"
  "code"
] @keyword.control

;; The verbatim body of a `code` block: a literal listing, shaded like a
;; string. (The external scanner reports it as a zero-width node, so this is
;; mostly future-proofing; editors fall back to the default face today.)
(raw_block) @string

;; ───────────── Operators / punctuation ────────────────────────────────

[
  "->"
  "<-"
  "="
  "+="
  "-="
  "+"
  "-"
  "!"
  "."
  ":"
  "?"
  "|"
] @operator

[
  ";"
  ","
] @punctuation.delimiter

[
  "("
  ")"
  "{"
  "}"
  "["
  "]"
  "<"
  ">"
] @punctuation.bracket

;; The `*` that marks variadic params and splice lines reads as a modifier.
(param "*" @keyword.modifier)
(splice_line "*" @keyword.modifier)

;; ───────────── Declarations ───────────────────────────────────────────

(view_decl name: (identifier) @function)
(app_decl name: (identifier) @function)
(component_decl name: (identifier) @function)
(flow_decl name: (identifier) @function)
(theme_decl name: (identifier) @type)
(theme_decl base: (identifier) @type)
(type_decl name: (identifier) @type)
(op_decl name: (identifier) @function)
(state_decl name: (identifier) @property)
(param name: (identifier) @variable.parameter)
(op_param name: (identifier) @variable.parameter)
(splice_line name: (identifier) @variable.parameter)
(field_decl name: (identifier) @property)
(variant_decl name: (identifier) @constant)
(match_arm variant: (identifier) @constant)
(match_arm bind: (identifier) @variable)
(backend_decl name: (identifier) @module)
(session_decl name: (identifier) @module)
(icons_decl name: (identifier) @module)
(fonts_decl provider: (identifier) @constant.builtin)
(import_decl alias: (identifier) @module)
(module_path) @module
(backend_clause name: (identifier) @module)
(invalidates_clause query: (identifier) @function.call)
(icon_target target: (identifier) @constant.builtin)
(backend_binding method: (identifier) @constant.builtin)
(backend_binding "same-origin" @constant.builtin)

;; ───────────── Types ───────────────────────────────────────────────────

(type_ref name: (identifier) @type)
(type_ref member: (identifier) @type)

;; ───────────── References ─────────────────────────────────────────────

;; Head of a dotted chain is a cell/value; later segments are fields.
(cell_ref (identifier) @variable)
(cell_ref (identifier) (identifier) @property)

;; `true` / `false` anywhere in reference position.
((cell_ref (identifier) @constant.builtin.boolean)
 (#any-of? @constant.builtin.boolean "true" "false"))

;; ───────────── Invocation heads ───────────────────────────────────────

;; User-defined component reference (default). The inline-body forms
;; (`view Empty = text "hi"`) surface the head ident as a `kind:` field
;; on the declaration node itself.
(invocation kind: (identifier) @function.call)
(qualified_head package: (identifier) @module)
(qualified_head name: (identifier) @function.call)
(view_decl kind: (identifier) @function.call)
(app_decl kind: (identifier) @function.call)
(component_decl kind: (identifier) @function.call)
(test_decl kind: (identifier) @function.call)
(story_decl kind: (identifier) @function.call)

;; Built-in component kinds rendered by the stdlib — listed AFTER the
;; general @function.call rule so they take precedence (tree-sitter applies
;; the last matching highlight per node). Source of truth:
;; lower.BuiltinKinds().
((invocation kind: (identifier) @type.builtin)
 (#any-of? @type.builtin
   "badge" "bar" "button" "card" "container" "divider" "icon" "iframe"
   "input" "modal" "pulse" "route" "router" "stack" "text" "title"))

;; ───────────── Kwargs, handlers, bindings ──────────────────────────────

(kwarg name: (identifier) @property)
(tone_binding tone: (identifier) @property)
(text_binding token: (identifier) @property)
(handler event: (identifier) @function.method)
(on_mount event: (identifier) @function.method)

;; Built-in tone names get a constant highlight when used as the value of
;; `tone=…` or as the LHS of a tone binding.
((kwarg
   name: (identifier) @_n
   value: (cell_ref (identifier) @constant.builtin))
 (#eq? @_n "tone")
 (#any-of? @constant.builtin
   "default" "selected" "primary" "accent"
   "danger" "success" "warning" "muted"))

((tone_binding tone: (identifier) @constant.builtin)
 (#any-of? @constant.builtin
   "primary" "accent" "danger" "success"
   "warning" "muted" "surface" "selected" "outline"))

;; ───────────── for / if ───────────────────────────────────────────────

(for_loop var: (identifier) @variable)

;; ───────────── Calls ───────────────────────────────────────────────────

;; Last segment of a call's callee is the function/method name:
;; `Refresh()` and `items.append(0)` both highlight the name before `(`.
(call callee: (cell_ref (identifier) @function.call .))

;; ───────────── Literals ───────────────────────────────────────────────

(string) @string
(string_text) @string
(string_escape) @string.escape

(interpolation) @punctuation.special
(interpolation expr: (identifier) @variable)

(integer) @number

;; ───────────── Comments ───────────────────────────────────────────────

(comment) @comment
