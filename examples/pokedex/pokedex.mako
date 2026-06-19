// A small end-to-end demo of the Sigil ops loop:
//
//   - types are declared here in Sigil
//   - queries / commands are the typed contract
//   - the client-side bundle emits `window.__sigil_ops.<Name>(args)`
//   - the Go server in main.go implements the matching `api` interface
//     and Mount()s the routes
//
// Click any button in the browser to drive a real HTTP round-trip
// against the running server.

// Brand theme — the page identity isn't generic-admin-UI blue, it's
// the Pokédex device. Red as the primary accent + a warm off-white
// page bg replace the stock light theme. Renamed `pokedex extends
// light` so the rendered page swaps in this palette whenever the
// viewer prefers light mode.
theme pokedex extends light =
  surface = "#ffffff" on "#1a1a1a"
  page    = "#f5f2ea" on "#1a1a1a"
  primary = "#1a1a1a" on "#ffffff"
  accent  = "#c91515" on "#ffffff"
  danger  = "#a31010" on "#ffffff"
  success = "#247046" on "#ffffff"
  warning = "#8a6500" on "#ffffff"

// Types are owned by a sibling package; we import the set so the
// queries and the view can reference Slot / Mood without duplicating
// the declarations here. First demo of L52's import mechanism.
import github.com/incantery/mako/examples/pokedex/types

// Icon set: the project brings its own SVGs (the compiler ships
// zero curated icons). Folder walk discovers `plus.svg`,
// `pokeball.svg`, etc.; each file is validated for the headless-color
// contract (no hardcoded fill/stroke; viewBox required) before it
// becomes part of the set. Reference downstream as `PkdxIcons.name`.
icons PkdxIcons =
  web "./icons/web"

// L54 backend declaration: every op routes through this. The
// shared sigilFetch helper in the emitted JS reads `url` per call
// and consults `auth` to build headers. `auth none` keeps the demo
// trivially callable; a real app's auth swaps to `bearer` and
// names a session cell for the token.
backend Api =
  url "http://localhost:8080"
  auth none

// Ops bind to the sole declared backend by default; the explicit
// `backend Api` clause is unnecessary here but called out below
// once to document the surface.
//
// CatchPokemon mutates team size + slot data, so it declares the
// queries it invalidates. After the command resolves, sigilFetch
// evicts those cache entries; subsequent reads refetch.
query Ping = Bool
query GetTeamSize = Int
query GetActiveName = String
query GetSlot -> id : Int = types.Slot

command Train = Bool
command CatchPokemon -> id : Int = Bool backend Api invalidates GetTeamSize GetSlot
command SetMood -> mood : types.Mood = Bool

view Pokedex =
  state online = false
  state size = 0
  state active = ""
  state featured : types.Slot
  state roster = []
    id   : Int
    name : String
  container width=medium
    stack gap=3
      card tone=accent elevation=md
        stack gap=1
          title "Pokédex" size=lg
          text "A field guide for trainers" size=caption

      card elevation=sm
        stack gap=3
          stack horizontal gap=2
            icon PkdxIcons.target tone=accent
            title "Status" size=md
          stack gap=2
            stack horizontal gap=3
              text "online" tone=muted size=caption
              text online size=body-strong
            stack horizontal gap=3
              text "team size" tone=muted size=caption
              text size size=body-strong
            stack horizontal gap=3
              text "active" tone=muted size=caption
              text active size=body-strong
          divider tone=muted
          stack horizontal gap=1
            button "ping" tone=accent icon=PkdxIcons.sparkle on click { online = Ping() }
            button "fetch size" tone=accent icon=PkdxIcons.download on click { size = GetTeamSize() }
            button "fetch active" tone=accent icon=PkdxIcons.download on click { active = GetActiveName() }
            button "train" tone=warning icon=PkdxIcons.bolt on click { Train() }

      card elevation=sm
        stack gap=3
          stack horizontal gap=2
            icon PkdxIcons.sparkle tone=accent
            title "Featured slot" size=md
          stack gap=2
            stack horizontal gap=3
              text "id" tone=muted size=caption
              text featured.id size=body-strong
            stack horizontal gap=3
              text "name" tone=muted size=caption
              text featured.name size=body-strong
            stack gap=1
              stack horizontal gap=3
                text "hp" tone=muted size=caption
                text featured.hp size=body-strong
              bar value=featured.hp max=100 tone=success
          divider tone=muted
          stack horizontal gap=1
            button "load slot 1" tone=accent icon=PkdxIcons.target on click { featured = GetSlot(1) }

      card elevation=sm
        stack gap=3
          stack horizontal gap=2
            icon PkdxIcons.pokeball tone=accent
            title "Roster" size=md
          stack horizontal gap=1
            button "add slot" tone=accent icon=PkdxIcons.plus on click { roster.append(0, "vacant") }
          divider tone=muted
          for slot in roster
            stack horizontal gap=2
              badge slot.name tone=muted size=caption
              button "catch" tone=success icon=PkdxIcons.pokeball on click { CatchPokemon(slot.id) }

// Test target — a hand-rolled Go server runs at this host. Start it
// with `go run ./examples/pokedex` from the repo root, then `sigil
// test examples/pokedex/pokedex.sigil` from another terminal.
app Pokedex =
  target web
    host "http://localhost:8080"

test "ping server flips online" = scenario in Pokedex
  expect-cell online false
  click button "ping"
  expect-cell online true

test "fetch team size lands in the cell" = scenario in Pokedex
  expect-cell size 0
  click button "fetch size"
  expect-cell size 3

test "fetch active lands the active name in the cell" = scenario in Pokedex
  expect-cell active ""
  click button "fetch active"
  expect-cell active "Pikachu"

test "load slot spreads the record into leaf cells" = scenario in Pokedex
  expect-cell featured.id 0
  expect-cell featured.name ""
  expect-cell featured.hp 0
  click button "load slot 1"
  expect-cell featured.id 1
  expect-cell featured.name "Pikachu"
  expect-cell featured.hp 100
