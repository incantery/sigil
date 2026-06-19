# Mako core grammar (M0 expression core)

The minimal ML-flavored expression language. UI and reactivity are **not** in the
grammar — `element`, `cell`, `Button`, etc. are ordinary identifiers applied as
functions, so the grammar that parses `f x` already parses all of them.

Notation: EBNF. `( )` grouping, `|` alternation, `*` zero-or-more, `+` one-or-more,
`?` optional. Terminals from the lexer are UPPER_CASE. Layout tokens `NEWLINE`,
`INDENT`, `DEDENT` are produced by the lexer (see Layout).

## Lexical

```
INT     = digit+                                  ; 0, 42
FLOAT   = digit+ '.' digit+                       ; 3.14
STRING  = '"' ( char | '${' expr '}' )* '"'       ; interpolation; see Strings
IDENT   = (lower | '_') (alnum | '_' | "'")*      ; name, name', x_1
UIDENT  = upper (alnum | '_')*                     ; Constructor / Type name
HOLE    = '__' IDENT                              ; intrinsics: __cell, __effect
```

Keywords: `let rec pub import as type fun if then else match with of`.
A bare `_` is the wildcard, not an IDENT.

**Capitalization is significant** (the ML rule): a lowercase-initial name is a
value/function/component binding (`card`, `button`, `cell`); an uppercase-initial
name is a type or data constructor (`Color`, `Some`, `Home`). This lets the parser
tell `Some x` (constructor applied) from `f x` (function applied) with no lookahead,
and is the single most useful disambiguation for both the checker and an agent.
UI components are therefore lowercase.

Operators, by binding power (loosest → tightest); all binary, left-assoc unless noted:

```
|>                          ; pipe
||
&&
== != < > <= >=             ; non-assoc
++                          ; concat (right-assoc)
+ -
* / %
(unary) - !                 ; prefix
application  f x            ; juxtaposition, left-assoc, tighter than any binop
. field access  a.b         ; postfix, tightest
```

So `a |> f x + g.h y` parses as `a |> ((f x) + ((g.h) y))`.

## Layout

Indentation is significant and compiled to `INDENT` / `DEDENT` / `NEWLINE` by the
lexer (offside rule, Python-style: spaces only, tabs are a lex error).

- A `NEWLINE` is emitted between items at the same indentation.
- An `INDENT` opens a *block*; a matching `DEDENT` closes it.
- A block introduced after `=` , `->` (match arm body), `then`, `else`, or `with`
  may be either a single inline expression or an indented block.
- Inside `( )`, `[ ]`, `{ }` layout is **fully suspended** (no layout tokens
  emitted); newlines are plain whitespace. Elements separate on `,` only, with an
  optional trailing comma. (C/Rust/JS model — unambiguous, no continuation rules.)

A *block expression* is a run of `let` bindings followed by exactly one result
expression; its value is the result (no `in` keyword — layout delimits it).

## Module

```
module   = import* decl*
import   = 'import' STRING ( '(' importnames ')' | 'as' UIDENT )?
importnames = IDENT ( ',' IDENT )* ','?
```

A bare `import "path"` binds the package under its last path segment
(`.../std/ui` → `ui.Card`). `(names)` pulls names into scope unqualified.
`as Name` rebinds the qualifier.

## Declarations

```
decl     = 'pub'? ( letdecl | typedecl )

letdecl  = 'let' 'rec'? bindtarget '=' blockexpr
bindtarget = IDENT param*          ; function:  let f x y = ...
           | pattern               ; value/destructure:  let (a, b) = ...

typedecl = 'type' UIDENT tyvar* '=' ( variants | record )
variants = '|'? ctor ( '|' ctor )*
ctor     = UIDENT ( 'of' type )?
record   = '{' fieldtype ( ',' fieldtype )* ','? '}'
fieldtype= IDENT ':' type
```

Recursion: a `let` may refer to itself without `rec`; the checker detects
self/mutual reference (Elm-style). `rec` is accepted as an explicit hint.

## Types

```
type     = tyatom ( '->' type )?                  ; '->' right-assoc
tyatom   = UIDENT tyatom*                          ; List a, Option Int
         | tyvar                                   ; a, b   (lowercase)
         | '(' type ( ',' type )* ')'              ; tuple / grouping / unit '()'
         | '{' fieldtype ( ',' fieldtype )* '}'    ; record type
tyvar    = IDENT
```

## Expressions

```
expr     = 'fun' param+ '->' blockexpr
         | 'if' expr 'then' blockexpr 'else' blockexpr
         | 'match' expr 'with' arm+
         | letexpr
         | opexpr

letexpr  = 'let' 'rec'? bindtarget '=' blockexpr NEWLINE blockexpr   ; block-level let

arm      = '|' pattern ('if' expr)? '->' blockexpr     ; optional guard

opexpr   = opexpr BINOP opexpr
         | UNOP opexpr
         | app

app      = app postfix                              ; application (juxtaposition)
         | postfix
postfix  = postfix '.' IDENT                        ; field access
         | atom

atom     = INT | FLOAT | STRING | IDENT | UIDENT | HOLE | '_'
         | '(' ')'                                  ; unit
         | '(' expr ( ',' expr )* ')'               ; group / tuple
         | '[' ( expr ( ',' expr )* ','? )? ']'      ; list (comma-separated, trailing ok)
         | '{' ( field ( ',' field )* ','? )? '}'    ; record literal
field    = IDENT '=' expr | IDENT                   ; 'click = e' or punned 'click'

param    = IDENT
         | '_'
         | '(' pattern ')'
         | '{' patfield ( ',' patfield )* '}'       ; record-destructuring param
patfield = IDENT ( '=' expr )?                      ; field, optional default
```

`blockexpr` = an inline `expr` **or** `INDENT` (`letexpr`-chain ending in `expr`) `DEDENT`.

## Patterns

```
pattern  = UIDENT pattern*                          ; constructor, e.g. Some x
         | '(' pattern ( ',' pattern )* ')'         ; tuple / group / unit
         | '{' patfield ( ',' patfield )* '}'       ; record
         | '[' ( pattern ( ',' pattern )* ','? )? ']'   ; list (M0+: fixed-length)
         | IDENT                                     ; binding
         | '_'                                       ; wildcard
         | INT | FLOAT | STRING                      ; literal
```

`match` must be exhaustive (checked in M0.4); a non-exhaustive `match` is a
compile error naming the missing constructor(s).

## Strings & interpolation

A `STRING` lexes into an alternating sequence of literal chunks and `${ expr }`
holes. `"hi ${name ()}!"` desugars at parse time to
`strConcat ["hi "; toStr (name ()); "!"]` (a stdlib call), so interpolation adds
no node kind to the AST beyond an `Interp [segment]` sugar that lowers immediately.

## Worked example (every construct that M0 must parse)

```mako
import "github.com/incantery/mako/std/reactive" (cell)

type Tab = Home | Profile | Settings

type Theme = { surface : String, primary : String }

let tabLabel t =
  match t with
  | Home     -> "home"
  | Profile  -> "profile"
  | Settings -> "settings"

let greet name =
  let (n, setN) = cell name
  "hello, ${n ()}!"

pub let main () =
  [1, 2, 3] |> List.map (fun x -> x * x) |> List.sum
```
