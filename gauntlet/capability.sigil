// Gauntlet challenge #5 — capability contrast.
//
// The point: the available vocabulary is a typed function of what is
// known about the target — something a generic browser driver cannot do.
//
// Against this FOREIGN page (plain HTML, no Sigil, no cell map), the only
// way to assert "there are three agents" is to observe the rendered DOM:
//
//     expect-count ".agent" 3        <- Observe floor, works anywhere
//
// The Sigil-native analog would read the state directly:
//
//     expect-cell agents 3           <- Introspect, native targets ONLY
//
// and writing that against this `external` app is a COMPILE ERROR, not a
// runtime miss — the gate lives in `lower`. See the negative lock in
// pkg/lang/lower (TestExpectCellRejectedOnExternalTarget) and
// docs/scenario-ir.md §4.

app Capability =
  target web
    external
    host "http://localhost:7373/c/capability/vanilla"

test "cardinality of a foreign list is observed from the DOM" = scenario in Capability
  expect-text "Agents"
  expect-count ".agent" 3
