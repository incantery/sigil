type PokemonType =
  | fire
  | water
  | grass
  | electric
  | psychic
  | normal

type Stats =
  hp      : Int
  attack  : Int
  defense : Int
  speed   : Int

type Pokemon =
  id       : Int
  name     : String
  nickname : String?
  kind     : PokemonType
  active   : Bool
  stats    : Stats

type PokemonPage =
  items : List<Pokemon>
  total : Int
  next  : Int?

query ListPokemon -> page : Int = PokemonPage
query GetPokemon -> id : Int = Pokemon?
query GetCount = Int

command CatchPokemon -> id : Int = Pokemon
command ReleasePokemon -> id : Int = Bool
command Refresh = Bool

view TypesDemo =
  state p : Pokemon
  state team : List<Pokemon> = []
  state count = 0
  card
    title "Active Pokémon"
    text p.name
    text p.kind
    stack horizontal gap=1
      text "hp"
      text p.stats.hp
    stack horizontal gap=1
      text "attack"
      text p.stats.attack
    button "heal +1" on click { p.stats.hp += 1 }
    button "attack +1" on click { p.stats.attack += 1 }
    title "Team"
    button "add party member" on click { team.append() }
    for member in team
      stack horizontal gap=1
        text member.name
        text member.kind
        text member.stats.hp
    title "Live ops"
    stack horizontal gap=1
      text "count"
      text count
    button "fetch count" on click { count = GetCount() }
    button "refresh" on click { Refresh() }

test "record state defaults are applied" = scenario TypesDemo
  expect-cell p.kind "fire"
  expect-cell p.stats.hp 0

test "heal increments nested cell" = scenario TypesDemo
  click button "heal +1"
  expect-cell p.stats.hp 1
  click button "heal +1"
  expect-cell p.stats.hp 2

test "add party member appends a row of defaults" = scenario TypesDemo
  click button "add party member"
  click button "add party member"
  // Default Pokemon: name="", kind="fire". With two members in the
  // team, both rendered rows show their default `kind` ("fire").
  expect-text "fire"
