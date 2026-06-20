// Studio domain types — shared across the main view and future sub-views.

type Agent =
  name    : String
  model   : String
  tools   : Int
  runs    : Int
  latency : String
  success : String
  status  : String

type Run =
  id      : String
  agent   : String
  status  : String
  model   : String
  tokens  : Int
  cost    : String
  latency : String
  summary : String
  time    : String

type TimelineEvent =
  kind    : String
  label   : String
  detail  : String
  atMs    : Int

type Prompt =
  name     : String
  kind     : String
  tokens   : Int
  usedBy   : String
  versions : Int
  updated  : String

type Provider =
  name    : String
  models  : Int
  status  : String
  spend   : String
  lastUse : String

type RoutingRule =
  pattern : String
  target  : String
