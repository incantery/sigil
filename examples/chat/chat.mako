// Streaming chat — Sigil's first server-*push* primitive.
//
// `query` and `command` are request/response: POST, await one JSON
// value. `stream` is different — the server holds the connection open
// and emits deltas over time. The `<-` arrow-into operator fills a
// String cell with those deltas as they arrive (fetch + ReadableStream
// under the hood), re-rendering the bound text on every chunk.
//
// This demo uses a *multi-channel* stream: one inference (one backend
// request) produces two interleaved channels — `thinking` (the model's
// chain-of-thought) and `answer` (the reply). A record-typed return
// makes the channels live; the tuple `(thinking, reply) <- Chat(prompt)`
// fans the single request into two live regions, demuxed by channel.
//
// Run the mock backend with `go run ./examples/chat`, open
// http://localhost:8090, type a message, and watch the thinking pane
// fill first, then the answer.

// `url same-origin`: this process serves both the page and the ops, so
// the client calls /stream/<op> on whatever host the page was loaded
// from — localhost, a LAN IP, or a tunnel hostname, with no baked-in URL.
backend Api =
  url same-origin
  auth none

// The delta type. A record of String fields = a multi-channel stream:
// each field name is a live channel. Same typed-contract shape as
// query/command; only the transport (NDJSON deltas) differs.
type ChatDelta =
  thinking : String
  answer : String

stream Chat -> prompt : String = ChatDelta

// Always fails (the mock 500s it) — exercises the lifecycle error path:
// `Broken.failed` flips true and `Broken.error` carries the message.
stream Broken -> prompt : String = String

// A discriminated union returned by a query: the Go server builds one
// variant, marshals it `{tag, value}`, and the client `match` renders
// the matching arm — record payload (ok), primitive payload (down), or
// unit variant (unknown). Same typed contract as any op.
type Stats =
  uptime : Int
  region : String

type Health =
  | unknown
  | ok : Stats
  | down : String

query CheckHealth = Health

view ChatDemo =
  state prompt = ""
  state thinking = ""
  state reply = ""
  state broken = ""
  state health : Health

  container width=medium
    stack gap=3
      card elevation=sm
        stack gap=1
          title "Sigil Chat" size=lg
          text "Type a message — the thinking pane fills first, then the answer streams in. One inference, two live channels." tone=muted size=caption

      card elevation=sm
        stack gap=2
          input prompt placeholder="Ask something…"
          stack horizontal gap=1
            // One stream op, two targets: thinking <- `thinking` channel,
            // reply <- `answer` channel (positional, by record field order).
            // `disabled=Chat.pending` rides the implicit lifecycle cell:
            // the button greys out the moment the request opens and
            // re-enables when the stream settles.
            button "Send" tone=accent disabled=Chat.pending on click { (thinking, reply) <- Chat(prompt) }
            button "Send (broken)" tone=muted on click { broken <- Broken(prompt) }
          // Lifecycle surface: pending fills the dead air before the
          // first delta; failed/error report a stream that threw.
          if Chat.pending
            text "Streaming…" size=caption tone=muted
          if Broken.failed
            text Broken.error size=caption tone=danger

      card elevation=sm
        stack gap=1
          text "THINKING" size=caption tone=muted
          text thinking size=body tone=muted

      card elevation=sm
        stack gap=1
          text "ASSISTANT" size=caption tone=accent
          text reply size=body

      // Discriminated union over the wire: CheckHealth returns Health,
      // and `match` renders exactly one arm for the variant the server
      // built — exhaustively (drop an arm and the compiler refuses).
      card elevation=sm
        stack gap=1
          text "SERVER HEALTH" size=caption tone=muted
          button "Check health" tone=muted on click { health = CheckHealth() }
          match health
            | unknown
              text "Not checked yet" size=caption tone=muted
            | ok as s
              text "Online from ${s.region}" size=body
            | down as why
              text why size=body tone=danger

app ChatDemo =
  target web
    host "http://localhost:8090"

test "thinking then answer stream into separate regions" = scenario in ChatDemo
  fill input "Ask something…" "hello"
  click button "Send"
  expect-text "Let me think about \"hello\"… weighing the question carefully."
  expect-text "You asked: hello. Here is the streamed answer from a mock Sigil backend."

// Lifecycle: pending shows while the connection is open, then clears
// when the stream settles (the answer text is the settle signal).
test "pending indicator tracks the open stream" = scenario in ChatDemo
  fill input "Ask something…" "ping"
  click button "Send"
  expect-text "Streaming…"
  expect-text "You asked: ping. Here is the streamed answer from a mock Sigil backend."
  expect-no-text "Streaming…"

// Error path: the Broken impl fails before its first delta, so the
// generated handler answers 502 (the wire contract's
// error-before-first-delta rule); the thrown "Broken: 502" lands in
// Broken.error instead of a silent promise rejection.
test "failed stream surfaces its error" = scenario in ChatDemo
  fill input "Ask something…" "boom"
  click button "Send (broken)"
  expect-text "Broken: 502"

// A discriminated union round-trips the wire: the Go server returns the
// `ok` variant with a Stats payload; the client decodes `{tag,value}`
// and `match` renders the `ok` arm, reading the record payload's field.
test "union return renders the matching arm" = scenario in ChatDemo
  expect-text "Not checked yet"
  click button "Check health"
  expect-text "Online from iad"
  expect-no-text "Not checked yet"
