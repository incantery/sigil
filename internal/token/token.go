// Package token defines the lexical tokens of the sigil core language.
//
// The token set mirrors docs/grammar.md. Layout tokens (NEWLINE, INDENT, DEDENT)
// are synthesized by the lexer from indentation; the parser consumes them as
// ordinary block delimiters.
package token

import (
	"fmt"
	"sort"
)

// Kind enumerates the lexical categories.
type Kind int

const (
	EOF Kind = iota

	// Layout (synthesized from indentation).
	NEWLINE
	INDENT
	DEDENT

	// Literals & names.
	INT
	FLOAT
	STRING
	IDENT      // lowercase-initial identifier: name, x_1, name'
	UIDENT     // uppercase-initial: Constructor / Type name
	HOLE       // intrinsic: __cell, __effect
	UNDERSCORE // _

	// Keywords. NOTE: this block must stay contiguous (LET..EXPECT) —
	// internal/analysis/semantic.go classifies keywords by range.
	LET
	REC
	PUB
	IMPORT
	AS
	TYPE
	FUN
	IF
	THEN
	ELSE
	MATCH
	WITH
	OF
	EFFECT
	TEST
	EXPECT

	// Punctuation.
	LPAREN
	RPAREN
	LBRACK
	RBRACK
	LBRACE
	RBRACE
	COMMA
	EQ    // =
	ARROW // ->
	PIPE  // |
	DOT   // .
	COLON // :
	SEMI  // ;

	// Operators. NOTE: this block must stay contiguous (PIPEFWD..BANG) —
	// internal/analysis/semantic.go classifies operators by range.
	PIPEFWD // |>
	OROR    // ||
	ANDAND  // &&
	EQEQ    // ==
	NEQ     // !=
	LT      // <
	GT      // >
	LE      // <=
	GE      // >=
	CONCAT  // ++
	PLUS    // +
	MINUS   // -
	STAR    // *
	SLASH   // /
	PERCENT // %
	BANG    // !
)

// StrSeg is one segment of a (possibly interpolated) string literal. A segment is
// either a literal chunk (IsExpr=false, Lit set) or an interpolation hole
// (IsExpr=true, Expr holds the raw source between ${ and }).
type StrSeg struct {
	Lit    string
	Expr   string
	IsExpr bool
}

// Token is a single lexical token with source position (1-based line/col of its
// first rune).
type Token struct {
	Kind     Kind
	Lit      string   // raw text for INT/FLOAT/IDENT/UIDENT/HOLE
	Segments []StrSeg // for STRING
	Line     int
	Col      int
}

var keywords = map[string]Kind{
	"let":    LET,
	"rec":    REC,
	"pub":    PUB,
	"import": IMPORT,
	"as":     AS,
	"type":   TYPE,
	"fun":    FUN,
	"if":     IF,
	"then":   THEN,
	"else":   ELSE,
	"match":  MATCH,
	"with":   WITH,
	"of":     OF,
	"effect": EFFECT,
	"test":   TEST,
	"expect": EXPECT,
}

// Keywords returns the language's reserved words, sorted. Editor tooling
// (the tree-sitter grammar) cross-checks against this set.
func Keywords() []string {
	ks := make([]string, 0, len(keywords))
	for k := range keywords {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// LookupIdent maps an identifier spelling to its keyword Kind, or IDENT if it is
// not a keyword. (Used only for lowercase-initial words; UIDENT/HOLE are decided
// by the lexer before calling this.)
func LookupIdent(s string) Kind {
	if k, ok := keywords[s]; ok {
		return k
	}
	return IDENT
}

func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

var kindNames = map[Kind]string{
	EOF: "EOF", NEWLINE: "NEWLINE", INDENT: "INDENT", DEDENT: "DEDENT",
	INT: "INT", FLOAT: "FLOAT", STRING: "STRING", IDENT: "IDENT", UIDENT: "UIDENT",
	HOLE: "HOLE", UNDERSCORE: "UNDERSCORE",
	LET: "let", REC: "rec", PUB: "pub", IMPORT: "import", AS: "as", TYPE: "type",
	FUN: "fun", IF: "if", THEN: "then", ELSE: "else", MATCH: "match", WITH: "with", OF: "of",
	EFFECT: "effect", TEST: "test", EXPECT: "expect",
	LPAREN: "(", RPAREN: ")", LBRACK: "[", RBRACK: "]", LBRACE: "{", RBRACE: "}",
	COMMA: ",", EQ: "=", ARROW: "->", PIPE: "|", DOT: ".", COLON: ":", SEMI: ";",
	PIPEFWD: "|>", OROR: "||", ANDAND: "&&", EQEQ: "==", NEQ: "!=", LT: "<", GT: ">",
	LE: "<=", GE: ">=", CONCAT: "++", PLUS: "+", MINUS: "-", STAR: "*", SLASH: "/",
	PERCENT: "%", BANG: "!",
}

// String renders a token compactly for tests and diagnostics.
func (t Token) String() string {
	switch t.Kind {
	case INT, FLOAT, IDENT, UIDENT, HOLE:
		return fmt.Sprintf("%s(%s)", t.Kind, t.Lit)
	case STRING:
		return fmt.Sprintf("STRING(%d segs)", len(t.Segments))
	default:
		return t.Kind.String()
	}
}
