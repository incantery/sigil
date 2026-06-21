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
      field('name', $.identifier), repeat($.parameter), '=', $._expression),
    pub: $ => 'pub',
    rec: $ => 'rec',
    parameter: $ => $.identifier,

    import_decl: $ => seq(
      'import', field('path', $.string),
      optional(seq('(', commaSep1($.import_name), ')')),
      optional(seq('as', field('alias', $.identifier)))),
    import_name: $ => choice($.identifier, $.uident),

    type_decl: $ => seq(
      optional($.pub), 'type', field('name', $.uident), '=',
      $.constructor, repeat(seq('|', $.constructor))),
    constructor: $ => seq(field('name', $.uident), repeat($._atom)),

    // loosest → tightest; matches docs/grammar.md
    _expression: $ => choice($.lambda, $.if, $.match, $.binop, $.unop, $.application, $._postfix),

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

    // match arms are indented (layout block); _indent/_dedent are hidden nodes
    match: $ => prec.right(seq('match', field('scrutinee', $._expression), 'with',
      $._indent, repeat1(seq($.match_arm, $._newline)), $._dedent)),
    match_arm: $ => seq('|', field('pattern', $.pattern),
      optional(seq('if', field('guard', $._expression))), '->', field('body', $._expression)),
    pattern: $ => choice(
      seq($.uident, repeat($.pattern)), $.identifier, $.wildcard, $.number, $.string,
      seq('(', $.pattern, ')')),
    wildcard: $ => '_',

    _atom: $ => choice(
      $.identifier, $.uident, $.hole, $.float, $.number, $.string,
      $.unit, $.list, $.paren, $.block),
    paren: $ => seq('(', $._expression, ')'),
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
