// KEYWORDS — kept in sync with internal/token (TestGrammarKeywordsMatch).
const KEYWORDS = ["as","effect","else","fun","if","import","let","match","of","pub","rec","then","type","with"];

function commaSep1(rule) { return seq(rule, repeat(seq(',', rule))); }

module.exports = grammar({
  name: 'sigil',
  externals: $ => [$._newline, $._indent, $._dedent],
  extras: $ => [/[ \t\r\n]/, $.line_comment],
  word: $ => $.identifier,
  conflicts: $ => [
    // pattern `uident repeat(pattern)` is genuinely ambiguous at ( — GLR needed
    [$.pattern],
  ],
  rules: {
    source_file: $ => repeat(seq($._statement, $._newline)),
    _statement: $ => choice($.let_decl, $.import_decl, $.type_decl, $._expression),

    let_decl: $ => seq(
      optional($.pub), 'let', optional($.rec),
      choice(
        // function / value binding: a name followed by zero or more params
        seq(field('name', $.identifier), repeat($.parameter)),
        // destructuring binding: `let (a, b) = …` (no params)
        field('name', $.tuple_pattern)),
      '=', $._body),
    pub: $ => 'pub',
    rec: $ => 'rec',
    // A parameter is an identifier, the unit pattern `()`, a wildcard, or a
    // parenthesised / tuple pattern (`fun () -> …`, `fun (a, b) -> …`).
    parameter: $ => choice(
      $.identifier, $.unit, $.wildcard,
      seq('(', $.pattern, ')'), $.tuple_pattern),

    import_decl: $ => seq(
      'import', field('path', $.string),
      optional(seq('(', commaSep1($.import_name), ')')),
      optional(seq('as', field('alias', $.identifier)))),
    import_name: $ => choice($.identifier, $.uident),

    type_decl: $ => seq(
      optional($.pub), 'type', field('name', $.uident),
      repeat(field('param', $.type_param)), '=',
      $.constructor, repeat(seq('|', $.constructor))),
    type_param: $ => $.identifier,
    // A constructor optionally carries a payload type after `of`:
    //   Ok of a   |   Guard of (Unit -> Bool)
    constructor: $ => seq(
      field('name', $.uident),
      optional(seq('of', field('payload', $._type)))),

    // --- type expressions (structural; laxer than the real checker) ---
    // arrows are right-associative and loosest; application is tighter.
    _type: $ => choice($.type_arrow, $._type_app),
    type_arrow: $ => prec.right(seq($._type_app, '->', $._type)),
    _type_app: $ => choice($.type_apply, $._type_atom),
    type_apply: $ => prec.left(seq($._type_app, $._type_atom)),
    _type_atom: $ => choice(
      $.type_var, $.type_con, $.type_paren, $.type_tuple),
    type_var: $ => $.identifier,            // lowercase = type variable
    type_con: $ => $.uident,                // uppercase = type constructor
    type_paren: $ => seq('(', $._type, ')'),
    type_tuple: $ => seq('(', $._type, repeat1(seq(',', $._type)), ')'),

    // loosest → tightest; matches docs/grammar.md
    _expression: $ => choice($.lambda, $.if, $.match, $.effect_block, $.binop, $.unop, $.application, $._postfix),

    // A body is an inline expression OR an indented `block`. Blocks are allowed
    // ONLY in binding positions (after `=`, `->`, `then`, `else`), never as a
    // function argument or list element, so INDENT is never valid inside
    // brackets — which keeps the layout scanner from injecting a spurious block
    // into a multi-line list.
    _body: $ => choice($._expression, $.block),

    // effect { e1; e2; ... } — the brace block suspends layout; ; separates stmts
    effect_block: $ => seq('effect', '{', seq($._expression, repeat(seq(';', $._expression)), optional(';')), '}'),

    lambda: $ => prec.right(seq('fun', repeat1($.parameter), '->', $._expression)),
    if: $ => prec.right(seq(
      'if', field('condition', $._expression),
      'then', field('consequence', $._expression),
      'else', field('alternative', $._expression))),

    binop: $ => choice(
      prec.left(1, seq(field('left', $._expression), '|>', field('right', $._expression))),
      prec.left(2, seq(field('left', $._expression), '||', field('right', $._expression))),
      prec.left(3, seq(field('left', $._expression), '&&', field('right', $._expression))),
      prec.left(4, seq(field('left', $._expression), choice('==', '!=', '<', '>', '<=', '>='), field('right', $._expression))),
      prec.right(5, seq(field('left', $._expression), '++', field('right', $._expression))),
      prec.left(6, seq(field('left', $._expression), choice('+', '-'), field('right', $._expression))),
      prec.left(7, seq(field('left', $._expression), choice('*', '/'), field('right', $._expression)))),
    unop: $ => prec(8, seq('-', $._expression)),

    // application is left-associative: f x y => (f x) y
    // Use left-recursive choice to produce left-nested trees.
    application: $ => prec.left(9, seq(choice($.application, $._postfix), $._postfix)),
    _postfix: $ => choice($.field, $._atom),
    field: $ => prec.left(10, seq(field('record', $._atom), '.', field('field', $.identifier))),

    // match arms are a layout block introduced by `with`. They may be indented
    // under a mid-line `match` (→ INDENT/DEDENT), or aligned with a `match` that
    // sits on its own line (→ same-indent NEWLINEs). The external scanner emits
    // a MATCH_INDENT before the first arm in BOTH cases (see scanner.c), so the
    // grammar sees a uniform block here.
    match: $ => prec.right(seq('match', field('scrutinee', $._expression), 'with',
      $._indent, $.match_arm, repeat(seq($._newline, $.match_arm)),
      optional($._newline), $._dedent)),
    match_arm: $ => seq('|', field('pattern', $.pattern),
      optional(seq('if', field('guard', $._expression))), '->', field('body', $._expression)),
    pattern: $ => choice(
      seq($.uident, repeat($.pattern)), $.identifier, $.wildcard, $.number, $.string,
      seq('(', $.pattern, ')'),
      $.tuple_pattern),
    tuple_pattern: $ => seq('(', $.pattern, repeat1(seq(',', $.pattern)), ')'),
    wildcard: $ => '_',

    _atom: $ => choice(
      $.identifier, $.uident, $.hole, $.float, $.number, $.string,
      $.unit, $.list, $.paren, $.tuple),
    paren: $ => seq('(', $._expression, ')'),
    tuple: $ => seq('(', $._expression, repeat1(seq(',', $._expression)), ')'),
    unit: $ => seq('(', ')'),
    list: $ => seq('[', optional(commaSep1($._expression)), ']'),

    block: $ => seq($._indent, repeat(seq($._statement, $._newline)), $._dedent),

    identifier: $ => /[a-z_][A-Za-z0-9_']*/,
    uident: $ => /[A-Z][A-Za-z0-9_]*/,
    hole: $ => token(prec(1, /__[A-Za-z_][A-Za-z0-9_']*/)),
    number: $ => /\d+/,
    float: $ => /\d+\.\d+/,
    string: $ => seq('"', repeat(choice($.interpolation, $._string_text)), '"'),
    _string_text: $ => token.immediate(prec(1, /[^"$\\]+|\$[^{]/)),
    interpolation: $ => seq('${', $._expression, '}'),
    line_comment: $ => token(seq('//', /.*/)),
  }
});
