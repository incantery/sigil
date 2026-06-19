// std/list — list utilities over the kernel's __listLen / __listAt primitives.
// Recursion is index-based (the kernel has no cons pattern); access is total
// (get returns an Option), so these functions never fault.

// length returns the number of elements.
pub let length xs = __listLen xs

// get returns the element at i, or None when i is out of range.
pub let get xs i = __listAt xs i

// findFrom returns the first element from index i that satisfies pred. Recursion
// terminates when the index runs past the end (__listAt returns None).
pub let rec findFrom pred xs i =
  match __listAt xs i with
  | None -> None
  | Some x -> if pred x then Some x else findFrom pred xs (i + 1)

// find returns the first element satisfying pred, or None.
pub let find pred xs = findFrom pred xs 0

// any reports whether any element satisfies pred.
pub let any pred xs =
  match find pred xs with
  | Some x -> true
  | None -> false

// concat joins two lists.
pub let concat xs ys = __listConcat xs ys

// append adds one element to the end of a list.
pub let append xs x = __listConcat xs [x]

let rec mapFrom f xs i acc =
  match __listAt xs i with
  | None -> acc
  | Some x -> mapFrom f xs (i + 1) (__listConcat acc [f x])

// map applies f to every element, building a new list.
pub let map f xs = mapFrom f xs 0 []

let rec filterFrom pred xs i acc =
  match __listAt xs i with
  | None -> acc
  | Some x -> filterFrom pred xs (i + 1) (if pred x then __listConcat acc [x] else acc)

// filter keeps the elements satisfying pred.
pub let filter pred xs = filterFrom pred xs 0 []
