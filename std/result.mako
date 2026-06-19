// std/result — a total success-or-failure value. This is the type guarded
// boundaries (fetch, storage, …) hand back, forcing callers to handle failure
// via exhaustive match instead of leaking untyped, possibly-absent data.

pub type Result a e = Ok of a | Err of e

// withDefault returns the Ok value, or a fallback when the result is an Err.
pub let withDefault d r =
  match r with
  | Ok x -> x
  | Err e -> d

// isOk reports whether a result succeeded.
pub let isOk r =
  match r with
  | Ok x -> true
  | Err e -> false
