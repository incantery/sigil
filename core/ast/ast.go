// Package ast defines the abstract syntax tree for the mako core language,
// matching core/grammar.md. UI and reactivity are not represented here: element,
// cell, Button, etc. are ordinary names applied via App.
package ast

import "github.com/incantery/mako/core/token"

// Pos is a 1-based source position.
type Pos struct {
	Line, Col int
}

// Module is a whole compilation unit.
type Module struct {
	Imports []*Import
	Decls   []Decl
}

// Import is `import "path" (names)` / `as Alias` / bare.
type Import struct {
	Pos   Pos
	Path  string
	Names []string // selective import; empty if none
	Alias string   // rebind qualifier; empty if none
}

// --- declarations ---

// Decl is a top-level declaration.
type Decl interface{ declNode() }

// LetDecl binds a value, function, or destructuring pattern at top level.
//
//	let x = e            -> Name="x", Params=nil, Pat=nil
//	let f a b = e        -> Name="f", Params=[a b]
//	let (a, b) = e       -> Name="",  Pat=tuple
type LetDecl struct {
	Pos    Pos
	Pub    bool
	Rec    bool
	Name   string
	Params []Param
	Pat    Pattern // used only when Name==""
	Body   Expr
}

// TypeDecl declares an ADT or record type.
type TypeDecl struct {
	Pos      Pos
	Pub      bool
	Name     string
	Params   []string // type variables
	Variants []*Variant
	Record   []*FieldType // non-nil for record types
}

// Variant is one constructor of an ADT.
type Variant struct {
	Name string
	Arg  TypeExpr // nil for nullary constructors
}

// FieldType is one field of a record type.
type FieldType struct {
	Name string
	Type TypeExpr
}

func (*LetDecl) declNode()  {}
func (*TypeDecl) declNode() {}

// --- type expressions ---

// TypeExpr is a type-level expression.
type TypeExpr interface{ typeNode() }

type (
	// TyCon is a (possibly applied) type constructor: Int, List a, Option Int.
	TyCon struct {
		Name string
		Args []TypeExpr
	}
	// TyVar is a lowercase type variable.
	TyVar struct{ Name string }
	// TyArrow is a function type a -> b.
	TyArrow struct{ From, To TypeExpr }
	// TyTuple is (a, b, ...); the empty tuple is unit.
	TyTuple struct{ Elems []TypeExpr }
	// TyRecord is a structural record type.
	TyRecord struct{ Fields []*FieldType }
)

func (*TyCon) typeNode()    {}
func (*TyVar) typeNode()    {}
func (*TyArrow) typeNode()  {}
func (*TyTuple) typeNode()  {}
func (*TyRecord) typeNode() {}

// --- expressions ---

// Expr is an expression node.
type Expr interface{ exprNode() }

type (
	// IntLit / FloatLit keep the raw spelling; conversion happens downstream.
	IntLit struct {
		Pos Pos
		Raw string
	}
	FloatLit struct {
		Pos Pos
		Raw string
	}
	// StrLit is a string with no interpolation.
	StrLit struct {
		Pos   Pos
		Value string
	}
	// Interp is an interpolated string; Parts alternate StrLit and arbitrary exprs.
	Interp struct {
		Pos   Pos
		Parts []Expr
	}

	// Var references a lowercase identifier or a __intrinsic hole.
	Var struct {
		Pos  Pos
		Name string
	}
	// Ctor references an uppercase constructor.
	Ctor struct {
		Pos  Pos
		Name string
	}

	// Unit is ().
	Unit struct{ Pos Pos }
	// Tuple is (a, b, ...) with len >= 2.
	Tuple struct {
		Pos   Pos
		Elems []Expr
	}
	// ListLit is [a, b, ...].
	ListLit struct {
		Pos   Pos
		Elems []Expr
	}
	// RecordLit is { f = e, g } (g is punned to { g = g }).
	RecordLit struct {
		Pos    Pos
		Fields []*FieldVal
	}

	// Lambda is fun params -> body.
	Lambda struct {
		Pos    Pos
		Params []Param
		Body   Expr
	}
	// App is a single-argument application; f x y nests as App(App(f,x),y).
	App struct {
		Pos Pos
		Fn  Expr
		Arg Expr
	}
	// Field is record/module access a.b.
	Field struct {
		Pos  Pos
		Recv Expr
		Name string
	}
	// Binop / Unop are operator applications.
	Binop struct {
		Pos  Pos
		Op   token.Kind
		L, R Expr
	}
	Unop struct {
		Pos Pos
		Op  token.Kind
		X   Expr
	}

	// If is if/then/else (an expression).
	If struct {
		Pos              Pos
		Cond, Then, Else Expr
	}
	// Match is match scrut with | arm...
	Match struct {
		Pos   Pos
		Scrut Expr
		Arms  []*Arm
	}
	// Let is a block-level binding whose value is Body evaluated then In.
	Let struct {
		Pos    Pos
		Rec    bool
		Name   string
		Params []Param
		Pat    Pattern // used only when Name==""
		Body   Expr
		In     Expr
	}
	// Effect is a deferred effect context: `effect { s1; s2 }`. Building it is
	// pure; the runtime runs it. Effect operations are legal only inside one.
	Effect struct {
		Pos   Pos
		Stmts []Expr
	}
)

// FieldVal is one field of a record literal.
type FieldVal struct {
	Name  string
	Value Expr
}

// Arm is one case of a match.
type Arm struct {
	Pat   Pattern
	Guard Expr // nil if none
	Body  Expr
}

func (*IntLit) exprNode()    {}
func (*FloatLit) exprNode()  {}
func (*StrLit) exprNode()    {}
func (*Interp) exprNode()    {}
func (*Var) exprNode()       {}
func (*Ctor) exprNode()      {}
func (*Unit) exprNode()      {}
func (*Tuple) exprNode()     {}
func (*ListLit) exprNode()   {}
func (*RecordLit) exprNode() {}
func (*Lambda) exprNode()    {}
func (*App) exprNode()       {}
func (*Field) exprNode()     {}
func (*Binop) exprNode()     {}
func (*Unop) exprNode()      {}
func (*If) exprNode()        {}
func (*Match) exprNode()     {}
func (*Let) exprNode()       {}
func (*Effect) exprNode()    {}

// --- parameters ---

// Param is a function/lambda parameter.
type Param interface{ paramNode() }

type (
	// VarParam binds a name.
	VarParam struct{ Name string }
	// WildParam is _.
	WildParam struct{}
	// PatParam is a parenthesized destructuring pattern.
	PatParam struct{ Pat Pattern }
	// RecordParam destructures a record argument, with optional per-field defaults.
	RecordParam struct{ Fields []*RecordParamField }
)

// RecordParamField is one field of a RecordParam.
type RecordParamField struct {
	Name    string
	Default Expr // nil if no default
}

func (VarParam) paramNode()    {}
func (WildParam) paramNode()   {}
func (PatParam) paramNode()    {}
func (RecordParam) paramNode() {}

// --- patterns ---

// Pattern is a match/binding pattern.
type Pattern interface{ patNode() }

type (
	VarPat  struct{ Name string }
	WildPat struct{}
	CtorPat struct {
		Name string
		Args []Pattern
	}
	TuplePat  struct{ Elems []Pattern }
	ListPat   struct{ Elems []Pattern }
	RecordPat struct{ Fields []*PatField }
	IntPat    struct{ Raw string }
	FloatPat  struct{ Raw string }
	StrPat    struct{ Value string }
)

// PatField is one field of a record pattern; Pat==nil means pun (bind Name).
type PatField struct {
	Name string
	Pat  Pattern
}

func (VarPat) patNode()    {}
func (WildPat) patNode()   {}
func (CtorPat) patNode()   {}
func (TuplePat) patNode()  {}
func (ListPat) patNode()   {}
func (RecordPat) patNode() {}
func (IntPat) patNode()    {}
func (FloatPat) patNode()  {}
func (StrPat) patNode()    {}
