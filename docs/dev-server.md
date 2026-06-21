# Dev server: `sigil dev` vs `sigil serve`

- **`sigil serve ENTRY.sigil`** — production. Builds the bundle **once** at
  startup (fails fast on a type/parse error) and serves it as static bytes. No
  per-request rebuild.
- **`sigil dev ENTRY.sigil`** — development. Watches every `.sigil` file under
  `--root` (mtime poll, ~150ms) and performs **state-preserving, in-place hot
  module replacement**: on a change it rebuilds and pushes the new bundle over
  Server-Sent Events; the in-page client agent snapshots reactive cell values,
  disposes global listeners, empties `#app`, evals the new bundle, and rehydrates
  each cell. No page reload — scroll, focus, and console survive.

Both default to port `8099` and take `--root` / `--port`.

## How state is preserved (and when it resets)

Every cell funnels through `std/reactive`'s `cell`/`computed`, so the emitted
program has a single `__cell` site. Cells are therefore matched across a reload
by **creation order**: the Nth cell created adopts the Nth saved value.

- **Survives:** editing markup, styles, event handlers, and any code that does
  not change how many cells are created or their order — the common case.
- **Resets:** adding, removing, or reordering `cell` declarations (this shifts
  the indices of cells created afterward), and per-row local state created inside
  an `each` render thunk (v1 limitation; the seam toward preserving it is keyed
  `each`).

## Build errors

A failed rebuild shows a dismissable overlay over the still-running app (which
keeps its state). The next successful build clears the overlay and hot-swaps.
