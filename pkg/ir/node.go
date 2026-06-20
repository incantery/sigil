// Package ir is the semantic intermediate representation between the
// author-facing ui package and any concrete renderer. It is the single
// contract every renderer (HTML, SwiftUI, terminal, …) consumes.
//
// IR is pure data: no closures, no Go values that can't survive a JSON
// round-trip. Event handlers are declarative Actions that target reactive
// cells by id.
package ir

// Kind enumerates the semantic primitives. Renderers branch on Kind to choose
// a concrete implementation (e.g. Stack -> flexbox div in HTML, HStack/VStack
// in SwiftUI).
type Kind string

const (
	KindStack     Kind = "stack"
	KindGroup     Kind = "group"
	KindText      Kind = "text"
	KindTitle     Kind = "title"
	KindCode      Kind = "code" // verbatim monospace code block (no interpolation)
	KindCard      Kind = "card"
	KindButton    Kind = "button"
	KindTextInput Kind = "text_input"
	KindIf        Kind = "if"
	KindFor       Kind = "for"
	KindForItem   Kind = "for_item"
	KindModal     Kind = "modal"
	KindFragment  Kind = "fragment"
	KindIFrame    Kind = "iframe"
	KindContainer Kind = "container"
	KindBadge     Kind = "badge"
	KindBar       Kind = "bar"
	KindIcon      Kind = "icon"
	KindDivider   Kind = "divider"
	KindPulse     Kind = "pulse"
	KindRouter    Kind = "router"
	KindRoute     Kind = "route"
	KindMatch     Kind = "match"     // discriminated-union match over a cell
	KindMatchArm  Kind = "match_arm" // one `| variant [as bind]` arm of a match
)

// Node is the rendered shape of a component subtree.
//
// ID is stable across renders of the same component instance — it is derived
// from the component's path in the tree. Patch diffing keys on ID, which lets
// the runtime preserve focus and selection across re-renders.
type Node struct {
	Kind     Kind                  `json:"kind"`
	ID       string                `json:"id"`
	Props    map[string]any        `json:"props,omitempty"`
	Handlers map[string]Action     `json:"on,omitempty"`       // event name → action
	Bindings map[string]BindingRef `json:"bindings,omitempty"` // prop name → cell binding
	Children []Node                `json:"children,omitempty"`
}

// Action is a declarative description of "what to do on this event". Actions
// are pure data so they can run anywhere — client runtime in M2, server
// session in M4, future SwiftUI target later.
type Action struct {
	Kind   string         `json:"kind"`           // "set", "add", "toggle", ...
	CellID string         `json:"cell,omitempty"` // target reactive cell, if any
	Args   map[string]any `json:"args,omitempty"`
}

// BindingRef ties a node prop to a reactive cell. The runtime watches the
// cell and re-applies the prop when it changes.
//
// Template is optional: when set, it's a format string with "${0}" as the
// placeholder for the cell's current value. The runtime renders
// `template.replace("${0}", String(cells[CellID]))` instead of using the
// raw cell value. This supports `text "count: ${count}"` and similar mixed
// literal-and-cell strings without needing multi-cell binding wire support.
type BindingRef struct {
	CellID   string `json:"cell"`
	Template string `json:"template,omitempty"`
}

// Document is a complete render output: the root node plus the initial values
// of every reactive cell referenced by the tree. Renderers consume Documents.
//
// Name is the source view's name when the document came from a .sigil file —
// used by renderers as the page title and by inspect/describe commands to
// label the tree. Empty when the document was built programmatically.
//
// Components records user-defined component signatures inlined by lowering.
// Renderers ignore it; tooling (describe, AI agents, future LSP) consults
// it for the per-file component vocabulary without re-parsing.
type Document struct {
	Name       string            `json:"name,omitempty"`
	Root       Node              `json:"root"`
	Cells      map[string]any    `json:"cells,omitempty"`
	CellNames  map[string]string `json:"cell_names,omitempty"` // id → source name
	Components []ComponentSig    `json:"components,omitempty"`
	Types      []TypeDecl        `json:"types,omitempty"`
	Queries    []Query           `json:"queries,omitempty"`
	Commands   []Command         `json:"commands,omitempty"`
	Streams    []Stream          `json:"streams,omitempty"`
	Apps       []App             `json:"apps,omitempty"`
	Tests      []Test            `json:"tests,omitempty"`
	Stories    []Story           `json:"stories,omitempty"`
	IconSets   []IconSet         `json:"icon_sets,omitempty"`
	Backends   []Backend         `json:"backends,omitempty"`
	Sessions   []Session         `json:"sessions,omitempty"`
	Fonts      []FontSource      `json:"fonts,omitempty"`
	// MountActions are executed once when the SPA finishes building the
	// DOM. Used for initial data fetching (e.g. loading agents on page
	// load). Declared in source as `on mount { action; action; ... }`
	// at the view body level.
	MountActions []Action `json:"mount_actions,omitempty"`

	// Themes carries source-declared themes for renderers that want to
	// emit them alongside (or instead of) the built-in defaults. Stored as
	// opaque `any` to avoid an ir → theme package import cycle; the HTML
	// renderer type-asserts back to *theme.Theme. Empty means "use
	// renderer defaults."
	Themes []any `json:"-"`
}

// FontSource is one web-font loading declaration: which provider
// serves the listed families. The weights/styles actually fetched are
// computed at render time from the theme text scales that reference
// each family — the source never repeats that information.
type FontSource struct {
	Provider string   `json:"provider"` // closed set; today "google"
	Families []string `json:"families"`
}

// Backend is one named call target — URL prefix + auth method —
// that operations route through at runtime. A project can declare
// multiple backends (e.g. one for the main API, one for analytics);
// each query / command binds to one backend at lower time.
//
// The compile-time picture stays pure data: how dedupe / cache /
// invalidation actually happen is in the codegen, not encoded here.
type Backend struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Auth Auth   `json:"auth"`
}

// Auth describes one backend's authentication contract. Method is a
// closed v1 enum: "none", "bearer", "cookie". For bearer, TokenCellID
// names the session-scoped cell whose value the runtime reads (live)
// to build the Authorization header.
type Auth struct {
	Method      string `json:"method"`
	TokenCellID string `json:"token_cell_id,omitempty"`
}

// Session is a named collection of long-lived reactive cells —
// auth tokens, current user, feature flags — that outlive any single
// view. Backends reference session cells by id to build their auth
// headers; views read session cells the same way they read their
// own state.
//
// The runtime owns persistence policy (in-memory v1; localStorage
// when a flag lands); the source decl only describes shape.
type Session struct {
	Name  string        `json:"name"`
	Cells []SessionCell `json:"cells"`
}

// SessionCell is one reactive cell scoped to a session. Mirrors the
// view-state shape but with the session as its container; the cell
// id namespace is shared (the codegen emits both into one global
// cell table) so handler-side reads / writes go through the same
// machinery.
type SessionCell struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Initial any     `json:"initial,omitempty"`
	Type    TypeRef `json:"type,omitempty"`
}

// IconSet is one named icon vocabulary the project declared via an
// `icons <Name> = ...` source decl. Icon refs in source qualify with
// the set name (`icon=Lucide.search`); the renderer resolves names
// against the matching set. v1 carries only the web realization
// (SVG); future targets add fields to IconAsset.
type IconSet struct {
	Name  string               `json:"name"`
	Icons map[string]IconAsset `json:"icons"`
}

// IconAsset is one icon's per-target payload. Web carries the
// validated <svg> inner content plus its viewBox; the renderer
// re-wraps in a <symbol> for the dedupe-via-`<use>` emission pattern.
type IconAsset struct {
	ViewBox string `json:"view_box"`
	Web     string `json:"web"`
}

// ComponentSig records the surface signature of a user-defined component
// (declaration order). Params list parameter names; variadic params are
// rendered with a leading `*`.
type ComponentSig struct {
	Name   string   `json:"name"`
	Params []string `json:"params"`
}

// TypeDecl is one source-declared type. Kind discriminates between
// record (Fields populated) and sum (Variants populated). Records
// and sums can't be mixed within one decl; the lowerer enforces.
// Optionals + generics will land as additional shape flags on
// TypeFieldSpec (likely Optional + GenericArgs).
//
// A "sum" with all-unit variants is the original closed string enum
// (Go `type X string` + consts; default-initializes to the first
// variant). A "sum" where any variant carries a payload is a
// discriminated union (Rust-flavored): VariantSpecs holds the per-
// variant payload, runtime values are tagged `{tag, value}`, and a
// `match` must handle every variant exhaustively. Variants and
// VariantSpecs stay parallel and in declaration order; Variants
// holds just the names for the many call sites that only need them.
type TypeDecl struct {
	Name         string          `json:"name"`
	Kind         string          `json:"kind"` // "record" | "sum"
	Fields       []TypeFieldSpec `json:"fields,omitempty"`
	Variants     []string        `json:"variants,omitempty"`
	VariantSpecs []VariantSpec   `json:"variantSpecs,omitempty"`
}

// UnionValue is a discriminated-union value: a variant Tag plus an
// optional single payload. It is the cell-init shape for union state
// and the runtime/wire representation (`{ tag, value }`). A unit
// variant has a nil Value.
type UnionValue struct {
	Tag   string `json:"tag"`
	Value any    `json:"value,omitempty"`
}

// VariantSpec is one variant of a sum/union: its name and an optional
// single payload type (nil = a unit variant). Multi-field payloads are
// expressed by pointing Payload at a declared record type.
type VariantSpec struct {
	Name    string   `json:"name"`
	Payload *TypeRef `json:"payload,omitempty"`
}

// HasPayloads reports whether any variant carries a payload — i.e.
// whether this sum is a discriminated union rather than a plain
// string enum. Drives the divergent codegen (tagged struct vs string
// const) and whether `match` binds payloads.
func (t TypeDecl) HasPayloads() bool {
	for _, v := range t.VariantSpecs {
		if v.Payload != nil {
			return true
		}
	}
	return false
}

// TypeFieldSpec is one field of a record type: name + a structured
// reference to the field's type (primitive, declared, optional,
// generic, or any combination). The recursive shape lives in
// TypeRef so service-method args, query return types, etc. can
// share it without re-introducing string-typed type info.
type TypeFieldSpec struct {
	Name string  `json:"name"`
	Type TypeRef `json:"type"`
}

// TypeRef is a reference to a type. Name is the head (a primitive,
// a declared TypeDecl, or a builtin generic head like "List").
// Optional=true marks the entire reference as nullable (`Int?`,
// `List<Pokemon>?`). GenericArgs carries the inner type parameters
// for generic refs.
type TypeRef struct {
	Name        string    `json:"name"`
	Optional    bool      `json:"optional,omitempty"`
	GenericArgs []TypeRef `json:"args,omitempty"`
}

// Query declares one read-side operation. Inputs are positional
// typed arguments (in declaration order); Return is the result type.
// The IR is intentionally transport-agnostic — codegen later picks
// the concrete wire (HTTP path, method, encoding) from the name +
// signature alone, the same way connect-web derives a URL from a
// service + method.
type Query struct {
	Name    string          `json:"name"`
	Inputs  []TypeFieldSpec `json:"inputs,omitempty"`
	Return  TypeRef         `json:"return"`
	Backend string          `json:"backend,omitempty"` // name of backend this op routes through; empty = legacy relative URL
}

// Command declares one write-side operation. Shape mirrors Query;
// the distinction is semantic — commands mutate, queries read —
// and is preserved through codegen so cache invalidation,
// optimistic prediction, and CSRF-style guards can branch on it.
//
// Invalidates names the queries whose cached results become stale
// after this command resolves successfully. The runtime walks the
// list, marks each entry's cache stale, and refetches any cell
// currently bound to one. Empty = no invalidation (the command
// doesn't affect read state).
type Command struct {
	Name        string          `json:"name"`
	Inputs      []TypeFieldSpec `json:"inputs,omitempty"`
	Return      TypeRef         `json:"return"`
	Backend     string          `json:"backend,omitempty"`
	Invalidates []string        `json:"invalidates,omitempty"`
}

// Stream declares one server-push operation: the response arrives as a
// sequence of deltas over a held connection, not a single value. Shape
// mirrors Query; the distinction is the transport — the client reads
// the response body incrementally (fetch + ReadableStream) and patches
// the bound cell once per chunk. Return is the type of each delta
// (String for token streaming). Streams are read-side, so there is no
// Invalidates list.
//
// Channels is populated when Return names a record type whose fields are
// all String: the field names become the stream's live channels, so one
// backend request drives N live regions with each delta tagged by its
// channel. The wire format for a multi-channel stream is NDJSON — one
// {"channel":"<name>","text":"<delta>"} object per line. Channels is
// empty for a scalar (String-return) stream, which keeps the raw-text
// wire format where each chunk is appended verbatim.
type Stream struct {
	Name     string          `json:"name"`
	Inputs   []TypeFieldSpec `json:"inputs,omitempty"`
	Return   TypeRef         `json:"return"`
	Backend  string          `json:"backend,omitempty"`
	Channels []string        `json:"channels,omitempty"`
}

// App is a target-agnostic declaration of a system under test. The app's
// identity (Name) is portable across targets; per-target config lives
// in Targets keyed by target name ("web", future "ios", "terminal").
// Apps make tests addressable by intent — `scenario in Dora` — rather
// than by transport-coupled URLs or view names.
//
// Navigation primitives (routes / screens / destinations) are
// deliberately absent at v0. The first scenarios that need cross-page
// navigation will force the right shape.
type App struct {
	Name    string               `json:"name"`
	Targets map[string]AppTarget `json:"targets,omitempty"`
}

// AppTarget is one target's adapter info. Config is a free-form bag
// of target-specific settings — `host` for web, future `bundle` /
// `scheme` for iOS, `binary` for terminal. The test runner reads
// keys it understands per target and ignores the rest.
type AppTarget struct {
	Name   string         `json:"name"`
	Config map[string]any `json:"config,omitempty"`
}

// Test is a runnable behavioral spec authored in Sigil. Exactly one of
// View or App is set: View targets a Sigil view rendered by the runner
// in an embedded server (the original mode); App targets a declared
// App at a runtime-selected target (the new mode, `scenario in <App>`).
// Steps are the sequence of interactions and assertions, in source
// order. Future per-target adapters (Swift, terminal, …) consume the
// same shape.
type Test struct {
	Name  string `json:"name"`
	View  string `json:"view,omitempty"`
	App   string `json:"app,omitempty"`
	Steps []Step `json:"steps"`
}

// Story is one named, compile-checked component example — a storybook
// entry. The body lowered as a standalone document (own cell namespace,
// own bundle) that shares the module's components, themes, types, and
// ops; `sigil stories` serves each Doc in isolation inside the catalog.
// Line/Col preserve the declaration's source position for tooling.
type Story struct {
	Name string   `json:"name"`
	Doc  Document `json:"doc"`
	Line int      `json:"line,omitempty"`
	Col  int      `json:"col,omitempty"`
}

// Step is one line of a test body. Kind enumerates the step verb;
// Args carries the verb-specific payload (selector role, name string,
// expected value, etc.). Line/Col preserve source position for
// failure messages. Arms is populated only for `match` steps — the
// branch-on-observed-state verb, whose arms each carry their own body of
// (nested) steps. This is the seed of the scenario-IR generalization:
// a step can contain steps.
type Step struct {
	Kind string         `json:"kind"`
	Args map[string]any `json:"args,omitempty"`
	Arms []StepArm      `json:"arms,omitempty"`
	Line int            `json:"line"`
	Col  int            `json:"col"`
}

// StepArm is one arm of a `match` step: a literal value to compare the
// observed state against, and the steps to run when it matches.
type StepArm struct {
	Match string `json:"match"`
	Steps []Step `json:"steps,omitempty"`
}
