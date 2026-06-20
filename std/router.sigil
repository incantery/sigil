// std/router — client-side path routing as a library, over the kernel's
// location boundary (__path / __pushPath / __onPopState) and reactive __when.
// There is no `router`/`route` compiler primitive: a route is just a reactive
// match on the current path.

import "std/reactive" (cell)
import "std/string" (split)
import "std/list" (length, get)

// router returns (path, navigate): a reactive reader of the current path and a
// function that navigates to a new one. Browser back/forward stays in sync via
// the popstate listener.
pub let router () =
  let (path, setPath) = cell (__path ())
  let sync = (effect { __onPopState (fun () -> setPath (__path ())) }) ()
  let navigate = fun p -> (effect { __pushPath p ; setPath p }) ()
  (path, navigate)

// Access is a route's access policy. Every route must declare one: there is no
// way to call `route` without passing Public or a Guard, so "default-deny" is
// enforced by the TYPE SYSTEM, not a lint or a compiler pass. A Guard carries the
// predicate that gates the route (typically reading a session cell).
//
// Note: a client-side guard *declares* policy; it cannot *enforce* it (client
// code is attacker-controlled). Real enforcement stays at the server op-auth
// boundary. The guard is the single declaration the client gate reads.
pub type Access = Public | Guard of (Unit -> Bool)

let permitted access =
  match access with
  | Public -> true
  | Guard ok -> ok ()

// route renders view while the path matches AND the access policy permits it. A
// guarded route whose guard is currently false renders nothing — its content is
// gated. The guard predicate is re-checked reactively (it auto-tracks any cell it
// reads), so the route appears/disappears as auth state changes.
pub let route current path access view =
  __when (fun () -> current () == path && permitted access) view

// --- typed path parameters (`/user/:id`) ---

// isParam reports whether a pattern segment is a `:name` parameter.
let isParam seg =
  match get (split seg "") 0 with
  | Some c -> c == ":"
  | None -> false

// segMatch: a pattern segment matches a path segment if it is a parameter, or
// the two are equal.
let segMatch pat path = isParam pat || pat == path

// matchFrom walks pattern and path segments together. Both running out at the
// same index means a match; a length mismatch (one None while the other is Some)
// means no match.
let rec matchFrom patSegs pathSegs i =
  match get patSegs i with
  | None ->
    (match get pathSegs i with
     | None -> true
     | Some y -> false)
  | Some ps ->
    (match get pathSegs i with
     | Some xs -> segMatch ps xs && matchFrom patSegs pathSegs (i + 1)
     | None -> false)

// pathMatches reports whether a path pattern (with :params) matches a concrete
// path: same number of segments, each literal segment equal.
let pathMatches pattern path =
  matchFrom (split pattern "/") (split path "/") 0

let rec paramAt patSegs pathSegs name i =
  match get patSegs i with
  | None -> None
  | Some ps -> if ps == (":" ++ name) then get pathSegs i else paramAt patSegs pathSegs name (i + 1)

// routeParam is route for a path PATTERN with :params: it renders view while the
// current path matches the pattern and access permits.
pub let routeParam current pattern access view =
  __when (fun () -> pathMatches pattern (current ()) && permitted access) view

// param is a reactive reader of the value bound to :name in pattern for the
// current path (None when the pattern does not match / the name is absent).
pub let param current pattern name =
  fun () -> paramAt (split pattern "/") (split (current ()) "/") name 0
