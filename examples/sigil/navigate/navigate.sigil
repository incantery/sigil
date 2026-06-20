// Full-page navigation between server-served pages (REQUEST 14).
//
// Two forms:
//   navigate "<path>"                    — unconditional full-page load
//                                          (a Cancel link, a "back" button)
//   Op(args) then navigate "<path>"      — navigate ONLY after the op
//                                          resolves without error
//
// The `then` hook runs in the command's success path: if Login throws /
// returns non-2xx, Login.failed trips (and an error can render) and the
// navigation is skipped — the user stays put instead of being sent to a
// gated page that would just bounce them back. This is the two-page auth
// pattern: a public /login that navigates to / on success, and a Logout
// that navigates back to /login.
backend Api =
  url same-origin
  auth none

command Login -> email : String -> password : String = Bool
command Logout = Bool

view Login =
  state email = ""
  state password = ""
  card
    title "Sign in"
    stack gap=2
      input email type=email placeholder="you@example.com"
      input password type=password placeholder="password"
      stack horizontal gap=1
        // Navigate home only if the credentials check out.
        button "Sign in" tone=primary disabled=Login.pending on click { Login(email, password) then navigate "/" }
        // Unconditional: bail back to the marketing page.
        button "Cancel" tone=muted on click { navigate "/" }
      if Login.failed
        text Login.error tone=danger size=caption
      // Logout lives on the gated page in a real app; shown here for the
      // symmetric `then navigate` idiom.
      button "Sign out" tone=muted on click { Logout() then navigate "/login" }
