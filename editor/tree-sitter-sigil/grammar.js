module.exports = grammar({
  name: 'sigil',
  externals: $ => [$._newline, $._indent, $._dedent],
  extras: $ => [/[ \t\r\n]/, $.line_comment],
  word: $ => $.identifier,
  rules: {
    source_file: $ => repeat(seq($._statement, $._newline)),
    _statement: $ => choice($.let_decl, $._expression),
    let_decl: $ => seq('let', field('name', $.identifier), '=', $._expression),
    block: $ => seq($._indent, repeat(seq($._statement, $._newline)), $._dedent),
    _expression: $ => choice($.identifier, $.number, $.block),
    identifier: $ => /[a-z_][A-Za-z0-9_']*/,
    number: $ => /\d+/,
    line_comment: $ => token(seq('//', /.*/)),
  }
});
