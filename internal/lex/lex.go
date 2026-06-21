// Package lex turns sigil core source into a token stream.
//
// It implements the offside rule from core/grammar.md: indentation is compiled
// into NEWLINE / INDENT / DEDENT tokens. Inside brackets ( ) [ ] { } layout is
// fully suspended — newlines become plain whitespace and only commas separate
// elements.
package lex

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/incantery/sigil/internal/token"
)

// Error is a lexical error with 1-based source position.
type Error struct {
	Line, Col int
	Msg       string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Msg)
}

type lexer struct {
	src    []rune
	pos    int
	line   int
	col    int
	indent []int // indentation stack, always starts {0}
	bdepth int   // bracket nesting depth
	atBOL  bool  // at beginning of (logical) line
	toks   []token.Token
}

// Lex scans src and returns its tokens, terminated by EOF. Layout (NEWLINE /
// INDENT / DEDENT) is synthesized from indentation.
func Lex(src string) ([]token.Token, error) {
	l := &lexer{
		src:    []rune(src),
		line:   1,
		col:    1,
		indent: []int{0},
		atBOL:  true,
	}
	if err := l.run(); err != nil {
		return nil, err
	}
	return l.toks, nil
}

func (l *lexer) run() error {
	for l.pos < len(l.src) {
		if l.atBOL && l.bdepth == 0 {
			done, err := l.handleLineStart()
			if err != nil {
				return err
			}
			if done { // blank or comment-only line fully consumed
				continue
			}
			l.atBOL = false
		}

		r := l.peek()
		switch {
		case r == '\n':
			l.advanceNewline()
			if l.bdepth == 0 {
				l.atBOL = true
			}
		case r == ' ' || r == '\r':
			l.advance()
		case r == '\t':
			return l.errf("tabs are not allowed; use spaces")
		case r == '/' && l.peekAt(1) == '/':
			l.skipLineComment()
		default:
			if err := l.scanToken(); err != nil {
				return err
			}
		}
	}
	// Unwind any open indentation blocks, then EOF. EOF doubles as a statement
	// terminator, so no trailing NEWLINE is synthesized.
	for len(l.indent) > 1 {
		l.indent = l.indent[:len(l.indent)-1]
		l.emit(token.DEDENT, "")
	}
	l.emit(token.EOF, "")
	return nil
}

// handleLineStart measures the indentation of the upcoming line and emits the
// appropriate layout tokens. It returns done=true if the line was blank or a
// comment, in which case it has been fully consumed.
func (l *lexer) handleLineStart() (done bool, err error) {
	indent := 0
	for l.pos < len(l.src) {
		switch l.peek() {
		case ' ':
			indent++
			l.advance()
		case '\t':
			return false, l.errf("tabs are not allowed; use spaces")
		case '\n':
			l.advanceNewline() // blank line
			return true, nil
		case '\r':
			l.advance()
		default:
			if l.peek() == '/' && l.peekAt(1) == '/' {
				l.skipLineComment()
				// fall through to consume the newline (if any) as a blank line
				if l.pos < len(l.src) && l.peek() == '\n' {
					l.advanceNewline()
				}
				return true, nil
			}
			return false, l.applyLayout(indent)
		}
	}
	return true, nil // trailing whitespace at EOF
}

func (l *lexer) applyLayout(indent int) error {
	top := l.indent[len(l.indent)-1]
	switch {
	case indent > top:
		l.indent = append(l.indent, indent)
		l.emit(token.INDENT, "")
	case indent == top:
		if len(l.toks) > 0 {
			l.emit(token.NEWLINE, "")
		}
	default: // indent < top: close blocks down to a matching level
		for len(l.indent) > 1 && l.indent[len(l.indent)-1] > indent {
			l.indent = l.indent[:len(l.indent)-1]
			l.emit(token.DEDENT, "")
		}
		if l.indent[len(l.indent)-1] != indent {
			return l.errf("inconsistent dedent")
		}
		l.emit(token.NEWLINE, "")
	}
	return nil
}

func (l *lexer) scanToken() error {
	r := l.peek()
	switch {
	case unicode.IsDigit(r):
		l.scanNumber()
		return nil
	case r == '"':
		return l.scanString()
	case isIdentStart(r):
		l.scanWord()
		return nil
	default:
		return l.scanOperator()
	}
}

func (l *lexer) scanNumber() {
	startLine, startCol := l.line, l.col
	start := l.pos
	for l.pos < len(l.src) && unicode.IsDigit(l.peek()) {
		l.advance()
	}
	kind := token.INT
	// A '.' is part of the number only if followed by a digit (else it is field
	// access: `1.field`, or DOT).
	if l.peek() == '.' && unicode.IsDigit(l.peekAt(1)) {
		kind = token.FLOAT
		l.advance() // '.'
		for l.pos < len(l.src) && unicode.IsDigit(l.peek()) {
			l.advance()
		}
	}
	l.emitAt(kind, string(l.src[start:l.pos]), startLine, startCol)
}

func (l *lexer) scanWord() {
	startLine, startCol := l.line, l.col
	start := l.pos
	for l.pos < len(l.src) && isIdentPart(l.peek()) {
		l.advance()
	}
	word := string(l.src[start:l.pos])
	var kind token.Kind
	switch {
	case word == "_":
		kind = token.UNDERSCORE
	case strings.HasPrefix(word, "__"):
		kind = token.HOLE
	case unicode.IsUpper([]rune(word)[0]):
		kind = token.UIDENT
	default:
		kind = token.LookupIdent(word)
	}
	l.emitAt(kind, word, startLine, startCol)
}

// scanString lexes a double-quoted string with ${...} interpolation into a single
// STRING token carrying its segments.
func (l *lexer) scanString() error {
	startLine, startCol := l.line, l.col
	l.advance() // opening quote
	var segs []token.StrSeg
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			segs = append(segs, token.StrSeg{Lit: lit.String()})
			lit.Reset()
		}
	}
	for {
		if l.pos >= len(l.src) {
			return l.errfAt(startLine, startCol, "unterminated string")
		}
		r := l.peek()
		switch r {
		case '"':
			l.advance()
			flush()
			t := token.Token{Kind: token.STRING, Segments: segs, Line: startLine, Col: startCol}
			l.toks = append(l.toks, t)
			return nil
		case '\n':
			return l.errfAt(startLine, startCol, "unterminated string (newline in literal)")
		case '\\':
			l.advance()
			if l.pos >= len(l.src) {
				return l.errfAt(startLine, startCol, "unterminated string")
			}
			lit.WriteRune(unescape(l.peek()))
			l.advance()
		case '$':
			if l.peekAt(1) == '{' {
				flush()
				raw, err := l.scanInterp()
				if err != nil {
					return err
				}
				segs = append(segs, token.StrSeg{Expr: raw, IsExpr: true})
			} else {
				lit.WriteRune(r)
				l.advance()
			}
		default:
			lit.WriteRune(r)
			l.advance()
		}
	}
}

// scanInterp consumes `${ ... }` and returns the raw source between the braces,
// tracking nested braces so records/lambdas inside an interpolation work.
func (l *lexer) scanInterp() (string, error) {
	openLine, openCol := l.line, l.col
	l.advance() // $
	l.advance() // {
	start := l.pos
	depth := 1
	for l.pos < len(l.src) {
		switch l.peek() {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				raw := string(l.src[start:l.pos])
				l.advance() // closing }
				return raw, nil
			}
		case '\n':
			return "", l.errfAt(openLine, openCol, "unterminated interpolation")
		}
		l.advance()
	}
	return "", l.errfAt(openLine, openCol, "unterminated interpolation")
}

func (l *lexer) scanOperator() error {
	startLine, startCol := l.line, l.col
	r := l.peek()
	two := func(k token.Kind) {
		l.advance()
		l.advance()
		l.emitAt(k, "", startLine, startCol)
	}
	one := func(k token.Kind) {
		l.advance()
		l.emitAt(k, "", startLine, startCol)
	}
	n := l.peekAt(1)
	switch r {
	case '(':
		l.bdepth++
		one(token.LPAREN)
	case ')':
		if l.bdepth > 0 {
			l.bdepth--
		}
		one(token.RPAREN)
	case '[':
		l.bdepth++
		one(token.LBRACK)
	case ']':
		if l.bdepth > 0 {
			l.bdepth--
		}
		one(token.RBRACK)
	case '{':
		l.bdepth++
		one(token.LBRACE)
	case '}':
		if l.bdepth > 0 {
			l.bdepth--
		}
		one(token.RBRACE)
	case ',':
		one(token.COMMA)
	case '.':
		one(token.DOT)
	case ':':
		one(token.COLON)
	case ';':
		one(token.SEMI)
	case '|':
		switch n {
		case '>':
			two(token.PIPEFWD)
		case '|':
			two(token.OROR)
		default:
			one(token.PIPE)
		}
	case '&':
		if n == '&' {
			two(token.ANDAND)
		} else {
			return l.errf("unexpected '&'")
		}
	case '=':
		if n == '=' {
			two(token.EQEQ)
		} else {
			one(token.EQ)
		}
	case '!':
		if n == '=' {
			two(token.NEQ)
		} else {
			one(token.BANG)
		}
	case '<':
		if n == '=' {
			two(token.LE)
		} else {
			one(token.LT)
		}
	case '>':
		if n == '=' {
			two(token.GE)
		} else {
			one(token.GT)
		}
	case '+':
		if n == '+' {
			two(token.CONCAT)
		} else {
			one(token.PLUS)
		}
	case '-':
		if n == '>' {
			two(token.ARROW)
		} else {
			one(token.MINUS)
		}
	case '*':
		one(token.STAR)
	case '/':
		one(token.SLASH)
	case '%':
		one(token.PERCENT)
	default:
		return l.errf("unexpected character %q", r)
	}
	return nil
}

func (l *lexer) skipLineComment() {
	for l.pos < len(l.src) && l.peek() != '\n' {
		l.advance()
	}
}

// --- primitives ---

func (l *lexer) peek() rune {
	if l.pos >= len(l.src) {
		return 0
	}
	return l.src[l.pos]
}

func (l *lexer) peekAt(n int) rune {
	if l.pos+n >= len(l.src) {
		return 0
	}
	return l.src[l.pos+n]
}

func (l *lexer) advance() {
	l.pos++
	l.col++
}

func (l *lexer) advanceNewline() {
	l.pos++
	l.line++
	l.col = 1
}

func (l *lexer) emit(k token.Kind, lit string) {
	l.toks = append(l.toks, token.Token{Kind: k, Lit: lit, Line: l.line, Col: l.col})
}

func (l *lexer) emitAt(k token.Kind, lit string, line, col int) {
	l.toks = append(l.toks, token.Token{Kind: k, Lit: lit, Line: line, Col: col})
}

func (l *lexer) errf(format string, a ...any) error {
	return &Error{Line: l.line, Col: l.col, Msg: fmt.Sprintf(format, a...)}
}

func (l *lexer) errfAt(line, col int, format string, a ...any) error {
	return &Error{Line: line, Col: col, Msg: fmt.Sprintf(format, a...)}
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return r == '_' || r == '\'' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func unescape(r rune) rune {
	switch r {
	case 'n':
		return '\n'
	case 't':
		return '\t'
	case 'r':
		return '\r'
	default:
		return r // \" \\ \$ \{ and any other escape map to the literal char
	}
}
