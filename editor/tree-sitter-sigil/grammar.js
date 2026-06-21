function commaSep1(rule) { return seq(rule, repeat(seq(',', rule))); }

module.exports = grammar({
  name: 'sigil',
  externals: $ => [$._newline, $._indent, $._dedent],
  extras: $ => [/[ \t\r\n]/, $.line_comment],
  word: $ => $.identifier,
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

    _expression: $ => choice($.lambda, $.if, $.application, $._atom),
    lambda: $ => prec.right(seq('fun', repeat1($.parameter), '->', $._expression)),
    if: $ => prec.right(seq(
      'if', field('condition', $._expression),
      'then', field('consequence', $._expression),
      'else', field('alternative', $._expression))),
    // application is left-associative: f x y => (f x) y
    // We model this as: atom followed by one or more atoms, left-recursive.
    // tree-sitter doesn't support direct left-recursion, but prec.left with
    // choice between application and atom allows GLR to build the left tree.
    application: $ => prec.left(1, seq(
      choice($.application, $._atom),
      $._atom)),

    _atom: $ => choice(
      $.identifier, $.uident, $.hole, $.number, $.float, $.string,
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
    string: $ => /"[^"]*"/,
    line_comment: $ => token(seq('//', /.*/)),
  }
});
