;; Locals query — defines variable scopes and definition/reference points so
;; editors can offer go-to-definition, rename-symbol, and stale-binding
;; warnings within a buffer.

;; ───────────── Scopes ─────────────────────────────────────────────────

[
  (view_decl)
  (app_decl)
  (component_decl)
  (session_decl)
  (test_decl)
  (story_decl)
  (handler)
  (on_mount)
  (for_loop)
] @local.scope

;; ───────────── Definitions ────────────────────────────────────────────

(state_decl name: (identifier) @local.definition.var)
(param name: (identifier) @local.definition.parameter)
(op_param name: (identifier) @local.definition.parameter)
(for_loop var: (identifier) @local.definition.var)
(field_decl name: (identifier) @local.definition.field)
(view_decl name: (identifier) @local.definition.function)
(app_decl name: (identifier) @local.definition.function)
(component_decl name: (identifier) @local.definition.function)
(type_decl name: (identifier) @local.definition.type)
(op_decl name: (identifier) @local.definition.function)

;; ───────────── References ─────────────────────────────────────────────

(invocation kind: (identifier) @local.reference)
(for_loop list: (cell_ref (identifier) @local.reference))
(if_block condition: (cell_ref (identifier) @local.reference))
(assignment target: (cell_ref (identifier) @local.reference))
(stream_assign target: (cell_ref (identifier) @local.reference))
(call callee: (cell_ref (identifier) @local.reference))
(binop (cell_ref (identifier) @local.reference))
(unary_not (cell_ref (identifier) @local.reference))
(interpolation expr: (identifier) @local.reference)
(kwarg value: (cell_ref (identifier) @local.reference))
(type_ref name: (identifier) @local.reference)
