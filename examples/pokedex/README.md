# Pokédex demo

A working end-to-end Sigil app — types, queries, and commands declared
in `pokedex.mako`; a typed Go contract generated into `api/api.go`
via `sigil gen go`; and a hand-rolled Go HTTP server in `main.go`
that implements the contract and serves the Sigil-compiled HTML.

## Run

From the repo root:

```
go run ./examples/pokedex
```

Then open <http://localhost:8080>. Each button drives a real HTTP
round-trip:

| Button                | Verb     | Effect                                     |
| --------------------- | -------- | ------------------------------------------ |
| ping server           | query    | sets `online = true` if the server is up   |
| fetch team size       | query    | reads `teamSize` from the in-memory server |
| fetch active          | query    | reads `active` from the in-memory server   |
| train                 | command  | increments `teamSize`; discards the result |

After clicking *train* once or twice, click *fetch team size* again
to see the new value.

## Regenerate the API contract

When you edit `pokedex.mako`, regenerate `api/api.go`:

```
sigil gen go examples/pokedex/pokedex.mako --package api \
  --out examples/pokedex/api/api.go
```

The generated file declares struct types, an `API` interface, and a
`Mount(api, mux)` helper. The server in `main.go` implements `API`
and calls `Mount(s, mux)` to wire HTTP routes.

## Tests

The Sigil source declares an `app Pokedex` with a `target web host
"http://localhost:8080"` and a few `scenario in Pokedex` tests.
With the server running:

```
sigil test examples/pokedex/pokedex.mako
```

The runner compiles each scenario to a self-contained JS bundle,
injects it into a Chromium tab pointed at the configured host, and
asserts via `click` / `expect-cell` / `expect-text` verbs. Each
`expect-cell` polls Playwright-style — async op calls have time to
land without explicit waits.

## Wire shape

- Queries POST to `/query/<Name>`
- Commands POST to `/command/<Name>`
- Both request bodies are JSON of the op's `Args` struct
- Both response bodies are JSON of the op's return type

Same-origin from the served HTML, so no CORS plumbing.
