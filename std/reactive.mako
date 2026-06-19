// std/reactive — fine-grained reactivity as a mako library over the kernel's
// __cell / __get / __set / __effect intrinsics.
//
// A cell is a Solid-style (read, write) pair: read the current value with `r ()`
// (pure; subscribes the enclosing effect), write a new value with `w v`. The
// effect-performing intrinsics are wrapped in `effect { } ()` here so the rest of
// the library — and its callers — use ordinary functions.

// get reads a cell's current value. Pure: callable anywhere, and tracked when
// evaluated inside an effect.
pub let get c = __get c

// set writes a cell. The write effect is built and run immediately, so `set` is
// an ordinary effectful function usable directly from event handlers.
pub let set c v = (effect { __set c v }) ()

// watch registers a reactive effect: f runs once now, then again whenever a cell
// it read has changed.
pub let watch f = (effect { __effect f }) ()

// cell creates a signal and returns its (read, write) pair.
pub let cell init =
  let c = __cell init
  (fun () -> __get c, fun v -> set c v)

// computed derives a read-only signal from other signals; it recomputes whenever
// any dependency it reads changes.
pub let computed f =
  let c = __cell (f ())
  let reg = watch (fun () -> set c (f ()))
  fun () -> __get c
