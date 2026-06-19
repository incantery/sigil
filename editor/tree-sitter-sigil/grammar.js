/**
 * @file Tree-sitter grammar for the Sigil language.
 *
 * Mirrors pkg/lang/parser/parser.go's surface (L88: views, components,
 * apps, themes with text scales, tests/stories, types, query/command/
 * stream ops, imports, icons, fonts, backends, sessions, on-mount,
 * stream-assign). The hand-written Go parser is authoritative — this
 * grammar exists to give editors structural awareness (highlighting,
 * folding, locals, motions). It does NOT need to enforce the structural
 * rules (e.g. "state only inside a view") that lower applies, and it is
 * deliberately a little laxer where strictness buys editors nothing.
 *
 * Indent-sensitivity is handled by the external scanner in src/scanner.c:
 * INDENT / DEDENT / NEWLINE tokens delimit blocks the way Python's grammar
 * does. Blank lines and `//` comment-only lines are skipped before computing
 * the next significant line's indent.
 *
 * Drift guard: `make tree-sitter-verify` parses every example app and
 * fails on ERROR nodes; TestGrammarCoversDeclKeywords in pkg/lang/parser
 * cross-checks the keyword list against the Go parser.
 */

function commaSep1(rule) {
  return seq(rule, repeat(seq(',', rule)));
}

module.exports = grammar({
  name: 'sigil',

  externals: $ => [
    $._newline,
    $._indent,
    $._dedent,
    // The verbatim body of a `code` block — every line indented deeper than
    // the `code` keyword, as one opaque token. The `code` keyword itself is a
    // normal grammar keyword (so it reduces a preceding sibling and promotes
    // cleanly, exactly like `for`/`if`); only the raw body needs the scanner.
    $.raw_block,
  ],

  extras: $ => [
    /[ \t]+/,
    $.comment,
  ],

  word: $ => $.identifier,

  conflicts: $ => [],

  rules: {
    source_file: $ => repeat(choice(
      $.view_decl,
      $.app_decl,
      $.component_decl,
      $.flow_decl,
      $.theme_decl,
      $.test_decl,
      $.story_decl,
      $.type_decl,
      $.op_decl,
      $.import_decl,
      $.icons_decl,
      $.fonts_decl,
      $.backend_decl,
      $.session_decl,
      $._newline,
    )),

    // ───────────── view / app / component ───────────────────────────────

    view_decl: $ => seq('view', $._decl_tail),
    app_decl: $ => seq('app', $._decl_tail),
    component_decl: $ => seq('component', $._decl_tail),
    // A named, reusable sequence of scenario steps (the test-mood analog
    // of a component); shares the param-list `_decl_tail`.
    flow_decl: $ => seq('flow', $._decl_tail),

    _decl_tail: $ => seq(
      field('name', $.identifier),
      repeat($.param),
      '=',
      choice(
        $._block_or_eol,
        seq($._inline_body, $._newline, optional($._indented_children)),
      ),
    ),

    param: $ => seq(
      '->',
      optional(field('variadic', '*')),
      field('name', $.identifier),
      optional(seq(':', field('type', $.type_ref))),
    ),

    // ───────────── theme ─────────────────────────────────────────────────

    theme_decl: $ => seq(
      'theme',
      field('name', $.identifier),
      optional(seq('extends', field('base', $.identifier))),
      '=',
      $._newline,
      optional(seq(
        $._indent,
        repeat1(seq(choice($.text_binding, $.tone_binding), $._newline)),
        $._dedent,
      )),
    ),

    // `primary = "#bg" on "#fg"` (paired) or `outline = "#hex"` (single).
    tone_binding: $ => seq(
      field('tone', $.identifier),
      '=',
      field('bg', $.string),
      optional(seq('on', field('fg', $.string))),
    ),

    // `text heading-xl = "Fraunces" italic caps 44 700 tracking 10`
    text_binding: $ => seq(
      'text',
      field('token', $.identifier),
      '=',
      repeat1(choice($.string, $.integer, 'italic', 'caps', 'tracking')),
    ),

    // ───────────── test / story ──────────────────────────────────────────

    test_decl: $ => seq('test', $._quoted_decl_tail),
    story_decl: $ => seq('story', $._quoted_decl_tail),

    _quoted_decl_tail: $ => seq(
      field('name', $.string),
      '=',
      choice(
        $._block_or_eol,
        seq($._inline_body, $._newline, optional($._indented_children)),
      ),
    ),

    // ───────────── type ──────────────────────────────────────────────────

    type_decl: $ => seq(
      'type',
      field('name', $.identifier),
      '=',
      $._newline,
      optional(seq(
        $._indent,
        repeat1(seq(choice($.field_decl, $.variant_decl), $._newline)),
        $._dedent,
      )),
    ),

    field_decl: $ => seq(
      field('name', $.identifier),
      ':',
      field('type', $.type_ref),
      optional(seq('=', field('default', $._expr))),
    ),

    // `| name` (unit) or `| name : Type` (discriminated-union payload).
    variant_decl: $ => seq(
      '|',
      field('name', $.identifier),
      optional(seq(':', field('payload', $.type_ref))),
    ),

    // `List<types.Pokemon>?` — head ident, optional dotted member,
    // optional generic args (no space before `<`), optional `?`.
    type_ref: $ => seq(
      field('name', $.identifier),
      optional(seq(token.immediate('.'), field('member', $.identifier))),
      optional(seq(token.immediate('<'), commaSep1($.type_ref), '>')),
      optional(token.immediate('?')),
    ),

    // ───────────── query / command / stream ──────────────────────────────

    op_decl: $ => seq(
      choice('query', 'command', 'stream'),
      field('name', $.identifier),
      repeat($.op_param),
      '=',
      field('returns', $.type_ref),
      optional($.backend_clause),
      optional($.invalidates_clause),
      $._newline,
    ),

    op_param: $ => seq(
      '->',
      field('name', $.identifier),
      ':',
      field('type', $.type_ref),
    ),

    backend_clause: $ => seq('backend', field('name', $.identifier)),

    invalidates_clause: $ => seq('invalidates', repeat1(field('query', $.identifier))),

    // ───────────── import / icons / fonts / backend / session ───────────

    import_decl: $ => seq(
      'import',
      field('path', $.module_path),
      optional(seq('as', field('alias', $.identifier))),
      $._newline,
    ),

    module_path: $ => token(/[A-Za-z0-9_-]+(\.[A-Za-z0-9_-]+|\/[A-Za-z0-9._-]+)*/),

    icons_decl: $ => seq(
      'icons',
      field('name', $.identifier),
      '=',
      $._newline,
      optional(seq(
        $._indent,
        repeat1(seq($.icon_target, $._newline)),
        $._dedent,
      )),
    ),

    icon_target: $ => seq(
      field('target', $.identifier),
      field('path', $.string),
    ),

    fonts_decl: $ => seq(
      'fonts',
      field('provider', $.identifier),
      '=',
      repeat1($.string),
      $._newline,
    ),

    backend_decl: $ => seq(
      'backend',
      field('name', $.identifier),
      '=',
      $._newline,
      optional(seq(
        $._indent,
        repeat1(seq($.backend_binding, $._newline)),
        $._dedent,
      )),
    ),

    backend_binding: $ => choice(
      seq('url', field('url', choice($.string, 'same-origin'))),
      seq('auth', field('method', $.identifier)),
      seq('token', 'from', field('cell', $.cell_ref)),
    ),

    session_decl: $ => seq(
      'session',
      field('name', $.identifier),
      '=',
      $._newline,
      optional(seq(
        $._indent,
        repeat1($.state_decl),
        $._dedent,
      )),
    ),

    // ───────────── state ─────────────────────────────────────────────────

    // A state line, its newline, and (for structured list states) the
    // indented `field : Type = default` block.
    state_decl: $ => seq(
      'state',
      field('name', $.identifier),
      optional(seq(':', field('type', $.type_ref))),
      optional(seq('=', field('value', $._expr))),
      $._newline,
      optional(seq(
        $._indent,
        repeat1(seq($.field_decl, $._newline)),
        $._dedent,
      )),
    ),

    // ───────────── body lines ────────────────────────────────────────────

    _block_or_eol: $ => seq(
      $._newline,
      optional($._indented_children),
    ),

    _indented_children: $ => seq(
      $._indent,
      repeat1($._body_line),
      $._dedent,
    ),

    _body_line: $ => choice(
      $.state_decl,
      seq($.splice_line, $._newline),
      $.for_loop,
      $.if_block,
      $.match_block,
      $.on_mount,
      $.code_block,
      $.invocation,
    ),

    // `code` heads a verbatim block, structured like for/if: keyword, the
    // line's newline, then the body. The body is the external raw_block token
    // (every deeper-indented line); prec(2) lets the `code` keyword win over a
    // generic invocation head, the same way for/if/match do.
    code_block: $ => prec(2, choice(
      // Block form: `code` then a verbatim indented body. The trailing
      // _newline closes the last body line so a following sibling parses.
      seq('code', $._newline, $.raw_block, $._newline),
      // Inline form: `code "snippet"` — a one-line literal.
      seq('code', field('inline', $.string), $._newline),
    )),

    splice_line: $ => seq('*', field('name', $.identifier)),

    _inline_body: $ => choice(
      $._inline_invocation,
      $.splice_line,
    ),

    // Inline invocation: same shape as a full invocation line, but without
    // the trailing block — the indented children attach via the surrounding
    // decl's `_indented_children`.
    _inline_invocation: $ => seq(
      field('kind', $._invocation_head),
      repeat($._invocation_part),
    ),

    // An invocation head is a bare component/builtin name, or a
    // package-qualified component from an imported package
    // (`components.Pill "hi"`). The loader's merge pass rewrites the
    // qualified form to its bare name before lowering.
    _invocation_head: $ => choice(
      $.identifier,
      $.qualified_head,
    ),

    qualified_head: $ => seq(
      field('package', $.identifier),
      token.immediate('.'),
      field('name', $.identifier),
    ),

    // ───────────── for / if / on mount ───────────────────────────────────

    for_loop: $ => prec(2, seq(
      'for',
      field('var', $.identifier),
      'in',
      field('list', $.cell_ref),
      repeat($.kwarg), // `for agent in agents filter=searchText`
      $._block_or_eol,
    )),

    if_block: $ => prec(2, seq(
      'if',
      field('condition', $.cell_ref),
      $._block_or_eol,
    )),

    // `match <cell>` over a discriminated union (view mood), or
    // `match text-of "<selector>"` over an observed value (scenario mood).
    // One `| variant [as bind]` / `| "value"` arm per line; each arm
    // parents its own indented body.
    match_block: $ => prec(2, seq(
      'match',
      field('subject', choice($.cell_ref, $.match_source)),
      $._newline,
      $._indent,
      repeat1($.match_arm),
      $._dedent,
    )),

    // The scenario-mood match subject: a value read from the page.
    match_source: $ => seq('text-of', field('selector', $.string)),

    match_arm: $ => seq(
      '|',
      choice(
        field('variant', $.identifier),
        field('value', $.string),
      ),
      optional(seq('as', field('bind', $.identifier))),
      $._newline,
      optional($._indented_children),
    ),

    // View-level lifecycle handler: `on mount { … }`.
    on_mount: $ => prec(2, seq(
      'on',
      field('event', $.identifier),
      '{',
      $._stmt,
      repeat(seq(';', $._stmt)),
      '}',
      $._newline,
    )),

    // ───────────── general invocation ────────────────────────────────────

    invocation: $ => seq(
      field('kind', $._invocation_head),
      repeat($._invocation_part),
      $._block_or_eol,
    ),

    _invocation_part: $ => choice(
      $.kwarg,
      $.handler,
      $.string,
      $.integer,
      $.cell_ref,
    ),

    kwarg: $ => seq(
      field('name', $.identifier),
      token.immediate('='),
      field('value', choice($.string, $.integer, $.cell_ref)),
    ),

    handler: $ => seq(
      'on',
      field('event', $.identifier),
      '{',
      $._stmt,
      repeat(seq(';', $._stmt)),
      '}',
    ),

    // ───────────── statements (inside `{ … }`) ──────────────────────────

    _stmt: $ => choice(
      $.assignment,
      $.stream_assign,
      $.tuple_stream_assign,
      $.navigate_stmt,
      $.call_stmt,
    ),

    // `navigate "/path"` — full-page navigation.
    navigate_stmt: $ => prec(1, seq('navigate', field('path', $.string))),

    // A call in statement position, optionally with a `then navigate
    // "<path>"` success hook (runs only after the op resolves).
    call_stmt: $ => seq(
      $.call,
      optional(seq('then', 'navigate', field('then_path', $.string))),
    ),

    assignment: $ => seq(
      field('target', $.cell_ref),
      field('op', choice('=', '+=', '-=')),
      field('value', $._expr),
    ),

    // `reply <- Chat(prompt)` — progressive stream-into.
    stream_assign: $ => seq(
      field('target', $.cell_ref),
      '<-',
      field('source', $.call),
    ),

    // `(thinking, answer) <- Chat(prompt)` — multi-channel stream-into.
    tuple_stream_assign: $ => seq(
      '(',
      commaSep1(field('target', $.cell_ref)),
      ')',
      '<-',
      field('source', $.call),
    ),

    // One node for both op calls (`Refresh()`) and method calls
    // (`items.append(0)`): a dotted callee directly followed by `(`.
    // Highlight queries pick the last path segment as the method name.
    call: $ => seq(
      field('callee', $.cell_ref),
      token.immediate('('),
      optional(commaSep1($._expr)),
      ')',
    ),

    // ───────────── expressions ──────────────────────────────────────────

    _expr: $ => choice(
      $.binop,
      $._atom,
    ),

    binop: $ => prec.left(seq(
      $._atom,
      field('op', choice('+', '-')),
      $._atom,
    )),

    _atom: $ => choice(
      $.unary_not,
      $.string,
      $.integer,
      $.list_literal,
      $.call,
      $.cell_ref,
    ),

    unary_not: $ => seq('!', $._atom),

    list_literal: $ => seq(
      '[',
      optional(commaSep1($._atom)),
      ']',
    ),

    // `count`, `item.done`, `Session.user.name` — an identifier plus
    // dotted field segments (no whitespace around the dots in source).
    cell_ref: $ => seq(
      $.identifier,
      repeat(seq(token.immediate('.'), $.identifier)),
    ),

    // ───────────── lexical ───────────────────────────────────────────────

    string: $ => seq(
      '"',
      repeat(choice(
        $.interpolation,
        $.string_escape,
        $.string_text,
      )),
      token.immediate('"'),
    ),
    // `${name}` or a dotted cell ref `${item.field}` / `${s.region}` —
    // the dots are immediate (no spaces inside `${…}`).
    interpolation: $ => seq(
      token.immediate('${'),
      field('expr', seq(
        $.identifier,
        repeat(seq(token.immediate('.'), $.identifier)),
      )),
      token.immediate('}'),
    ),
    string_escape: $ => token.immediate(seq('\\', /["\\nt]/)),
    // Run of safe content chars OR a lone `$`. The lexer prefers the longer
    // `${` token (interpolation start) over `$` when both can match.
    string_text: $ => token.immediate(/[^"\\$]+|\$/),

    integer: $ => /-?\d+/,
    identifier: $ => /[A-Za-z_][A-Za-z0-9_-]*/,
    comment: $ => token(seq('//', /.*/)),
  },
});
