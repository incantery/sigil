// Package contract carves the IDL out of a compiled sigil program:
// the backends, operations (query / command / stream), and type
// declarations that define the client↔server wire contract — and
// nothing else. Backend code generators consume a Contract, never an
// ir.Document, so they are insulated from the UI IR's churn: layout,
// reactivity, themes, and rendering can evolve every level without
// touching anything a server implementor depends on.
//
// The Contract is also the unit of version-skew detection. Hash()
// returns a stable digest of the wire shape; `sigil gen` stamps it
// into generated server code, the page-serving runtime recomputes it
// from the freshly compiled doc at boot, and a mismatch fails loudly
// at construction time — a server built against one contract never
// silently serves a client bundle built against another.
package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/incantery/sigil/pkg/ir"
)

// Contract is the IDL subset of one compiled sigil program. The
// element types are shared with pkg/ir — they are already pure,
// JSON-tagged data — but only these five fields exist here; the
// document's node tree, cells, icons, fonts, and themes are
// deliberately out of reach.
type Contract struct {
	Backends []ir.Backend  `json:"backends,omitempty"`
	Types    []ir.TypeDecl `json:"types,omitempty"`
	Queries  []ir.Query    `json:"queries,omitempty"`
	Commands []ir.Command  `json:"commands,omitempty"`
	Streams  []ir.Stream   `json:"streams,omitempty"`
}

// FromDoc extracts the contract from a compiled document.
func FromDoc(doc ir.Document) Contract {
	return Contract{
		Backends: doc.Backends,
		Types:    doc.Types,
		Queries:  doc.Queries,
		Commands: doc.Commands,
		Streams:  doc.Streams,
	}
}

// Empty reports whether the contract declares no operations at all.
// A program with no ops has no server half to generate.
func (c Contract) Empty() bool {
	return len(c.Queries) == 0 && len(c.Commands) == 0 && len(c.Streams) == 0
}

// hashShape is what Hash digests: the wire shape only. Backends are
// excluded on purpose — a URL moving from localhost to a tunnel
// hostname, or auth switching tiers, changes deployment config, not
// the request/response shapes a generated server must satisfy.
// Slices are sorted by name so reordering declarations in source
// (without changing any shape) keeps the hash stable.
type hashShape struct {
	Version  int           `json:"v"`
	Types    []ir.TypeDecl `json:"types,omitempty"`
	Queries  []ir.Query    `json:"queries,omitempty"`
	Commands []ir.Command  `json:"commands,omitempty"`
	Streams  []ir.Stream   `json:"streams,omitempty"`
}

// hashVersion is the contract-digest format version. Bump it when
// the digested shape itself changes meaning (new fields that affect
// the wire, a different canonicalization) so old and new hashes can
// never collide silently.
const hashVersion = 1

// Hash returns the canonical digest of the contract's wire shape:
// "c1:" + hex(sha256(canonical JSON)). Two programs get the same
// hash iff their op set, parameter shapes, return types, stream
// channels, and type declarations are identical.
func (c Contract) Hash() string {
	shape := hashShape{
		Version:  hashVersion,
		Types:    slices.Clone(c.Types),
		Queries:  slices.Clone(c.Queries),
		Commands: slices.Clone(c.Commands),
		Streams:  slices.Clone(c.Streams),
	}
	sort.Slice(shape.Types, func(i, j int) bool { return shape.Types[i].Name < shape.Types[j].Name })
	sort.Slice(shape.Queries, func(i, j int) bool { return shape.Queries[i].Name < shape.Queries[j].Name })
	sort.Slice(shape.Commands, func(i, j int) bool { return shape.Commands[i].Name < shape.Commands[j].Name })
	sort.Slice(shape.Streams, func(i, j int) bool { return shape.Streams[i].Name < shape.Streams[j].Name })

	b, err := json.Marshal(shape)
	if err != nil {
		// Marshal over pure data structs cannot fail; if it ever
		// does, the digest must not silently degrade.
		panic(fmt.Sprintf("contract: canonical marshal failed: %v", err))
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("c%d:%s", hashVersion, hex.EncodeToString(sum[:]))
}
