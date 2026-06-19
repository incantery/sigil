type ChatDelta =
  thinking : String
  answer   : String

type Mode =
  | fast
  | deep

type User =
  name  : String
  email : String

type LoginOutcome =
  | idle
  | success : User
  | invalid : String
  | locked

backend Api =
  url same-origin
  auth none

query ListModes = List<String>
command SetMode -> mode : Mode = Bool
command Login -> email : String -> password : String = LoginOutcome
stream Chat -> prompt : String = ChatDelta
stream Tail -> path : String = String

view App =
  text "conformance fixture"
