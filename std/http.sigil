// std/http — a guarded HTTP boundary over the kernel's __fetch primitive. The
// raw (ok, body) the kernel hands back is decoded into a Result the caller must
// match: Ok carries the response body, Err carries an error message.

import "std/result" (Ok, Err)

// get fetches url and invokes onResult with a `Result String String`. onResult
// returns a deferred effect (the same shape as an event handler), so it runs
// when the response arrives.
pub let get url onResult =
  (effect {
    __fetch url (fun ok body ->
      if ok then onResult (Ok body) else onResult (Err body))
  }) ()
