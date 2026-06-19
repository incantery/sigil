// Sigil Studio — flagship application
//
// A desktop-style AI workstation for managing agents, prompts, tools,
// workflows, and model providers. Local-first, no account required.
//
// Types live in a sibling package (examples/studio/types) to exercise
// the L52 import system at real scale.

import github.com/incantery/mako/examples/studio/types

theme studio extends dark =
  surface = "#111111" on "#e5e5e5"
  page    = "#0a0a0a" on "#e5e5e5"
  primary = "#7c3aed" on "#ffffff"
  accent  = "#7c3aed" on "#ffffff"
  danger  = "#dc2626" on "#ffffff"
  success = "#15803d" on "#ffffff"
  warning = "#a16207" on "#ffffff"

icons StudioIcons =
  web "./icons/web"

backend Api =
  url "http://localhost:9090"
  auth none

query ListAgents = List<types.Agent>
query ListRuns = List<types.Run>
query GetTimeline = List<types.TimelineEvent>
query ListPrompts = List<types.Prompt>
query ListProviders = List<types.Provider>
query ListRoutes = List<types.RoutingRule>

// Commands mutate server state and declare the queries they
// invalidate. After a command resolves, sigilFetch evicts those
// cache entries so the immediate refetch (`agents = ListAgents()`)
// reads server truth instead of a stale cached list — the full
// client→server round-trip the flagship is meant to demonstrate.
command CreateAgent -> name : String -> model : String = Bool invalidates ListAgents
command ArchiveAgent -> name : String = Bool invalidates ListAgents

// The workstation's built-in assistant. `stream` is a server-push op:
// the reply arrives as String deltas over a held connection (fetch +
// ReadableStream), filling the conversation transcript token by token.
stream Assist -> prompt : String = String

view Studio =
  state page = "agents"
  state agents = []
    name    : String
    model   : String
    tools   : Int
    runs    : Int
    latency : String
    success : String
    status  : String
  state recentRuns = []
    id      : String
    agent   : String
    status  : String
    model   : String
    tokens  : Int
    cost    : String
    latency : String
    summary : String
    time    : String
  state timeline = []
    kind    : String
    label   : String
    detail  : String
    atMs    : Int
  state prompts = []
    name     : String
    kind     : String
    tokens   : Int
    usedBy   : String
    versions : Int
    updated  : String
  state providers = []
    name    : String
    models  : Int
    status  : String
    spend   : String
    lastUse : String
  state routes = []
    pattern : String
    target  : String
  state totalRuns = 0
  state totalCost = "$0.00"
  state avgLatency = "0s"
  state successRate = "0%"
  state agentCount = 0
  state searchText = ""
  state showPalette = false
  state showNewAgent = false
  state newAgentName = ""
  state newAgentModel = ""
  state assistantInput = ""
  state conversation = []
    role : String
    text : String
  state selectedAgent = ""
  state selectedModel = ""
  state selectedTools = 0
  state selectedRuns = 0
  state selectedLatency = ""
  state selectedSuccess = ""

  on mount { totalRuns = 847; totalCost = "$24.18"; avgLatency = "3.4s"; successRate = "94.7%"; agentCount = 10; agents = ListAgents(); recentRuns = ListRuns(); timeline = GetTimeline(); prompts = ListPrompts(); providers = ListProviders(); routes = ListRoutes() }

  // height=screen bounds the shell to exactly the viewport, so the
  // sidebar, each route page, and the assistant transcript scroll
  // internally (scroll=y) instead of growing the page.
  stack horizontal height=screen
    // Sidebar
    stack width=240 padding=md gap=3 tone=surface border=outline scroll=y
      stack gap=1
        title "Sigil Studio" size=sm
        text "local" tone=muted size=caption

      stack horizontal gap=2 padding=sm on click { showPalette = true }
        icon StudioIcons.search tone=muted
        text "Search..." tone=muted size=caption

      stack gap=1
        stack horizontal gap=2 padding=sm match=page when="agents" on click { page = "agents" }
          icon StudioIcons.agents tone=accent
          text "Agents" size=body-strong
        stack horizontal gap=2 padding=sm match=page when="runs" on click { page = "runs" }
          icon StudioIcons.runs
          text "Runs" tone=muted
        stack horizontal gap=2 padding=sm match=page when="prompts" on click { page = "prompts" }
          icon StudioIcons.prompts
          text "Prompts" tone=muted
        stack horizontal gap=2 padding=sm match=page when="tools" on click { page = "tools" }
          icon StudioIcons.tools
          text "Tools" tone=muted
        stack horizontal gap=2 padding=sm match=page when="workflows" on click { page = "workflows" }
          icon StudioIcons.workflows
          text "Workflows" tone=muted
        stack horizontal gap=2 padding=sm match=page when="evals" on click { page = "evals" }
          icon StudioIcons.evals
          text "Evaluations" tone=muted
        divider tone=muted
        stack horizontal gap=2 padding=sm match=page when="models" on click { page = "models" }
          icon StudioIcons.models
          text "Models" tone=muted
        stack horizontal gap=2 padding=sm match=page when="mcp" on click { page = "mcp" }
          icon StudioIcons.mcp
          text "MCP Servers" tone=muted
        divider tone=muted
        stack horizontal gap=2 padding=sm match=page when="settings" on click { page = "settings" }
          icon StudioIcons.settings
          text "Settings" tone=muted

    // Main content — routes swap based on page cell
    stack flex=1
      router page
        route "agents"
          stack padding=lg gap=4 scroll=y
            stack gap=2
              stack horizontal gap=3
                title "Agents" size=lg
                button "+ New Agent" tone=accent on click { showNewAgent = true }
              input searchText placeholder="Filter agents..."
              stack horizontal gap=3
                badge totalRuns size=caption
                badge totalCost size=caption
                badge avgLatency size=caption
                badge successRate size=caption

            stack gap=1
              stack columns="2fr 1.5fr 60px 70px 80px 80px" padding=sm
                text "Agent" size=caption tone=muted
                text "Model" size=caption tone=muted
                text "Tools" size=caption tone=muted
                text "Runs" size=caption tone=muted
                text "Latency" size=caption tone=muted
                text "Success" size=caption tone=muted

              divider tone=muted

              for agent in agents filter=searchText
                stack columns="2fr 1.5fr 60px 70px 80px 80px" padding=sm on click { page = "agent-detail"; selectedAgent = agent.name; selectedModel = agent.model; selectedTools = agent.tools; selectedRuns = agent.runs; selectedLatency = agent.latency; selectedSuccess = agent.success }
                  text agent.name size=body-strong
                  text agent.model size=caption tone=muted
                  text agent.tools size=caption
                  text agent.runs size=caption
                  text agent.latency size=caption tone=muted
                  text agent.success size=caption tone=success

        route "agent-detail"
          stack horizontal flex=1
            // Agent info (main)
            stack flex=1 padding=lg gap=4 scroll=y
              stack gap=2
                stack horizontal gap=3
                  button "Back" tone=muted on click { page = "agents" }
                  title selectedAgent size=lg
                  button "Archive" tone=danger on click { ArchiveAgent(selectedAgent); agents = ListAgents(); page = "agents" }

                stack horizontal gap=3
                  badge selectedModel size=caption tone=accent
                  badge selectedSuccess size=caption tone=success

              divider tone=muted

              // Instructions card
              stack gap=2
                title "Instructions" size=md
                card elevation=sm
                  stack gap=2
                    text "You are a senior code reviewer. Review the provided diff for:" size=body-strong
                    text "1. Correctness — logic bugs, edge cases, error handling" size=caption
                    text "2. Security — auth/authz, injection, secrets, data exposure" size=caption
                    text "3. Style — naming, structure, consistency with codebase" size=caption
                    text "4. Performance — N+1 queries, hot-path allocations, complexity" size=caption

              // Tools section
              stack gap=2
                title "Tools" size=md
                stack horizontal gap=2
                  card elevation=sm
                    stack gap=1
                      text "github.get_diff" size=body-strong
                      text "Fetch PR diff and metadata" size=caption tone=muted
                  card elevation=sm
                    stack gap=1
                      text "github.post_review" size=body-strong
                      text "Submit review comments" size=caption tone=muted
                  card elevation=sm
                    stack gap=1
                      text "fs.read" size=body-strong
                      text "Read project files" size=caption tone=muted
                  card elevation=sm
                    stack gap=1
                      text "grep" size=body-strong
                      text "Search codebase" size=caption tone=muted

            // Right sidebar - metrics
            stack width=280 padding=md gap=3 border=outline scroll=y
              title "Metrics" size=sm

              divider tone=muted

              stack gap=2
                stack gap=1
                  text "MODEL" size=caption tone=muted
                  text selectedModel size=body-strong
                stack gap=1
                  text "TOOLS" size=caption tone=muted
                  text selectedTools size=body-strong
                stack gap=1
                  text "TOTAL RUNS" size=caption tone=muted
                  text selectedRuns size=body-strong
                stack gap=1
                  text "AVG LATENCY" size=caption tone=muted
                  text selectedLatency size=body-strong
                stack gap=1
                  text "SUCCESS RATE" size=caption tone=muted
                  text selectedSuccess size=body-strong

        route "runs"
          stack horizontal flex=1
            // Run list (left panel)
            stack width=360 gap=1 border=outline scroll=y padding=md
              stack gap=2
                title "Runs" size=md
                stack horizontal gap=2
                  badge totalRuns size=caption

              divider tone=muted

              for run in recentRuns
                stack gap=1 padding=sm on click { page = "runs" }
                  stack horizontal gap=2
                    text run.agent size=body-strong
                    badge run.status size=caption tone=success
                  text run.summary size=caption tone=muted
                  stack horizontal gap=2
                    text run.time size=caption tone=muted
                    text run.cost size=caption tone=muted
                    text run.latency size=caption tone=muted

            // Run detail (center)
            stack flex=1 padding=lg gap=3 scroll=y
              stack gap=1
                title "run_a4f9c2" size=md
                stack horizontal gap=2
                  badge "code-reviewer" size=caption tone=accent
                  badge "success" size=caption tone=success
                  text "claude-sonnet-4.5" size=caption tone=muted

              divider tone=muted

              stack gap=3
                card elevation=sm
                  stack gap=2
                    text "ASSISTANT" size=caption tone=accent
                    text "Review PR #2847 — refactor auth middleware to use session-based verification." size=body-strong
                    text "Focus on security implications and behavioral changes vs the existing token-based flow." tone=muted

                card elevation=sm
                  stack gap=2
                    text "TOOL" size=caption tone=warning
                    text "github.get_diff" size=body-strong
                    text "auth/middleware.ts  +47 -12" tone=muted size=caption

                card elevation=sm
                  stack gap=2
                    text "TOOL" size=caption tone=warning
                    text "fs.read" size=body-strong
                    text "auth/middleware.test.ts" tone=muted size=caption

                card elevation=sm
                  stack gap=2
                    text "ASSISTANT" size=caption tone=accent
                    text "Review complete. The refactor is sound, but the new session-based verification has a few behavioral regressions worth addressing before merge." size=body-strong

            // Timeline (right panel)
            stack width=280 padding=md gap=2 border=outline scroll=y
              title "Timeline" size=sm

              divider tone=muted

              for event in timeline
                stack gap=1 padding=sm
                  stack horizontal gap=2
                    badge event.kind size=caption tone=muted
                    text event.label size=caption
                  text event.detail size=caption tone=muted

        route "prompts"
          stack horizontal flex=1
            // Prompt list (left)
            stack width=300 gap=1 border=outline scroll=y padding=md
              title "Prompts" size=md

              divider tone=muted

              for prompt in prompts
                stack gap=1 padding=sm on click { page = "prompts" }
                  text prompt.name size=body-strong
                  stack horizontal gap=2
                    badge prompt.kind size=caption tone=muted
                    text prompt.tokens size=caption tone=muted
                    text prompt.updated size=caption tone=muted

            // Prompt content (center)
            stack flex=1 padding=lg gap=3 scroll=y
              stack gap=1
                title "code-review/system" size=md
                stack horizontal gap=2
                  badge "system" size=caption tone=accent
                  text "2,184 tokens" size=caption tone=muted
                  text "8 versions" size=caption tone=muted

              divider tone=muted

              card elevation=sm
                stack gap=2
                  text "You are a senior code reviewer at {{org.name}}." size=body-strong
                  text "Your job is to review the diff for {{pr_number}} on {{repo.path}}." tone=muted
                  text "and produce actionable, high-signal review comments." tone=muted

              card tone=surface
                stack gap=1
                  text "Review Categories" size=caption tone=accent
                  text "1. Correctness — logic bugs, edge cases, error handling" size=caption
                  text "2. Security — auth/authz, injection, secrets, data exposure" size=caption
                  text "3. Behavioral — API contract changes, error code changes" size=caption
                  text "4. Style — naming, structure, codebase consistency" size=caption
                  text "5. Performance — N+1 queries, complexity, allocations" size=caption

            // Prompt metadata (right)
            stack width=260 padding=md gap=3 border=outline scroll=y
              title "Metadata" size=sm

              divider tone=muted

              stack gap=2
                stack gap=1
                  text "TYPE" size=caption tone=muted
                  text "system" size=body-strong
                stack gap=1
                  text "USED BY" size=caption tone=muted
                  badge "code-reviewer" size=caption tone=accent
                stack gap=1
                  text "VERSIONS" size=caption tone=muted
                  text "8" size=body-strong
                stack gap=1
                  text "UPDATED" size=caption tone=muted
                  text "2 days ago" size=body-strong

        route "tools"
          stack padding=lg gap=3
            title "Tools" size=lg
            text "Tool registry will appear here." tone=muted

        route "workflows"
          stack padding=lg gap=3
            title "Workflows" size=lg
            text "Workflow builder will appear here." tone=muted

        route "evals"
          stack padding=lg gap=3
            title "Evaluations" size=lg
            text "Evaluation suites will appear here." tone=muted

        route "models"
          stack padding=lg gap=3
            title "Models" size=lg
            text "Model configuration will appear here." tone=muted

        route "mcp"
          stack padding=lg gap=3
            title "MCP Servers" size=lg
            text "MCP server management will appear here." tone=muted

        route "settings"
          stack horizontal flex=1
            // Settings nav (left)
            stack width=200 padding=md gap=2 border=outline scroll=y
              text "GENERAL" size=caption tone=muted
              stack gap=1
                text "Workspace" size=caption
                text "Appearance" size=caption tone=muted

              text "MODELS" size=caption tone=muted
              stack gap=1
                text "Providers" size=body-strong
                text "Routing" size=caption tone=muted
                text "Cost settings" size=caption tone=muted

              text "INTEGRATIONS" size=caption tone=muted
              stack gap=1
                text "MCP servers" size=caption tone=muted
                text "Workflows" size=caption tone=muted
                text "Tools allowlist" size=caption tone=muted

            // Settings content (right)
            stack flex=1 padding=lg gap=4 scroll=y
              stack gap=1
                title "Providers" size=lg
                text "Configure model providers. Keys are stored locally and never leave this machine." tone=muted

              // Provider cards
              stack gap=2
                for provider in providers
                  card elevation=sm
                    stack horizontal gap=3
                      stack flex=1 gap=1
                        text provider.name size=body-strong
                        stack horizontal gap=2
                          text provider.models size=caption tone=muted
                          text provider.lastUse size=caption tone=muted
                      stack horizontal gap=2
                        badge provider.spend size=caption
                        badge provider.status size=caption tone=success

              // Routing rules
              stack gap=2
                stack gap=1
                  title "Routing rules" size=md
                  text "Applied in order. First match wins." tone=muted size=caption

                stack gap=1
                  stack columns="2fr 1.5fr" padding=sm
                    text "Pattern" size=caption tone=muted
                    text "Target" size=caption tone=muted

                  divider tone=muted

                  for rule in routes
                    stack columns="2fr 1.5fr" padding=sm
                      text rule.pattern size=caption
                      text rule.target size=caption tone=accent

    // Assistant rail — the workstation's own AI, present on every
    // screen (a peer of the sidebar, outside the router). Send appends
    // your turn plus an empty assistant turn, then streams the reply
    // into that row via `conversation.last.text <- Assist(...)`, so the
    // transcript keeps full multi-turn history.
    stack width=340 padding=md gap=2 tone=surface border=outline
      stack gap=1
        title "Assistant" size=sm
        text "Ask about your agents, runs, and prompts." tone=muted size=caption

      divider tone=muted

      // anchor=end keeps the transcript pinned to the newest turn as
      // the reply streams in, releasing while you read scrollback.
      stack flex=1 gap=2 scroll=y anchor=end
        for m in conversation
          stack gap=1 padding=sm
            text m.role size=caption tone=accent
            text m.text size=body

      // Stream lifecycle, as product UX: `Assist.pending` (implicit,
      // runtime-maintained) fills the dead air between Send and the
      // first token, and disables Send so a second click can't fork
      // the transcript; `Assist.failed` / `Assist.error` surface a
      // dead backend instead of a silently empty turn.
      if Assist.pending
        text "Assistant is thinking…" size=caption tone=muted
      if Assist.failed
        text Assist.error size=caption tone=danger

      stack horizontal gap=1
        input assistantInput placeholder="Ask the assistant…"
        button "Send" tone=accent disabled=Assist.pending on click { conversation.append("You", assistantInput); conversation.append("Assistant", ""); conversation.last.text <- Assist(assistantInput); assistantInput = "" }

  // Command palette — overlay triggered by clicking "Search..."
  modal showPalette
    stack padding=lg gap=3
      input searchText placeholder="Search agents, runs, prompts..."
      stack gap=1
        text "AGENTS" size=caption tone=muted
        stack gap=1 padding=sm on click { showPalette = false; page = "agent-detail"; selectedAgent = "code-reviewer"; selectedModel = "claude-sonnet-4.5"; selectedTools = 6; selectedRuns = 3421; selectedLatency = "8.7s"; selectedSuccess = "94%" }
          text "code-reviewer" size=body-strong
          text "Reviews PRs for style, bugs, security issues" size=caption tone=muted
        stack gap=1 padding=sm on click { showPalette = false; page = "agent-detail"; selectedAgent = "qa-tester-e2e"; selectedModel = "claude-sonnet-4.5"; selectedTools = 5; selectedRuns = 234; selectedLatency = "9.8s"; selectedSuccess = "88%" }
          text "qa-tester-e2e" size=body-strong
          text "Generates and executes E2E test cases" size=caption tone=muted
      stack gap=1
        text "ACTIONS" size=caption tone=muted
        stack gap=1 padding=sm on click { showPalette = false; page = "runs" }
          text "Run code-reviewer on..." size=body-strong
          text "Choose a PR, repo, or paste a diff" size=caption tone=muted
        stack gap=1 padding=sm on click { showPalette = false; page = "prompts" }
          text "Edit prompt: code-review/system" size=body-strong
          text "v8 · 2,184 tokens" size=caption tone=muted
      stack gap=1
        text "RECENT RUNS" size=caption tone=muted
        stack gap=1 padding=sm on click { showPalette = false; page = "runs" }
          text "run_a4f9c2 · Review PR #2847" size=body-strong
          text "code-reviewer · success · 8.7s" size=caption tone=muted
        stack gap=1 padding=sm on click { showPalette = false; page = "runs" }
          text "run_f7d2e6 · Review PR #2845" size=body-strong
          text "code-reviewer · failed · 2.1s" size=caption tone=muted

  // New-agent modal — the headline mutation. Create issues a command
  // (POST /command/CreateAgent), which invalidates ListAgents; the
  // immediate refetch then replaces the table with the server's list,
  // so the new row appears without a page reload.
  modal showNewAgent
    stack padding=lg gap=3
      title "New Agent" size=md
      input newAgentName placeholder="Agent name..."
      input newAgentModel placeholder="Model (e.g. claude-sonnet-4.5)..."
      stack horizontal gap=2
        button "Cancel" tone=muted on click { showNewAgent = false }
        button "Create" tone=accent on click { CreateAgent(newAgentName, newAgentModel); agents = ListAgents(); showNewAgent = false; newAgentName = ""; newAgentModel = "" }

app Studio =
  target web
    host "http://localhost:9090"

test "agents load on mount" = scenario in Studio
  expect-text "recruiter-screener"
  expect-text "code-reviewer"
  expect-text "summarizer-v2"

test "stats populate on mount" = scenario in Studio
  expect-text "847"
  expect-text "$24.18"
  expect-text "94.7%"

test "navigate to runs page" = scenario in Studio
  click text "Runs"
  expect-text "run_a4f9c2"
  expect-text "code-reviewer"

test "runs page shows timeline" = scenario in Studio
  click text "Runs"
  expect-text "Timeline"
  expect-text "github.get_diff"

test "navigate to prompts page" = scenario in Studio
  click text "Prompts"
  expect-text "code-review/system"
  expect-text "screening-criteria-v3"

test "click agent row shows detail" = scenario in Studio
  click text "code-reviewer"
  expect-text "Instructions"
  expect-text "github.get_diff"

test "command palette opens and navigates" = scenario in Studio
  click text "Search..."
  expect-text "AGENTS"
  expect-text "code-reviewer"
  expect-text "RECENT RUNS"

test "navigate to settings page" = scenario in Studio
  click text "Settings"
  expect-text "Providers"
  expect-text "Anthropic"
  expect-text "Routing rules"

// Full mutation lifecycle in one scenario. Create issues a command
// (POST), invalidates ListAgents, and the refetch replaces the table
// so the new row appears; archive then removes it. The test is
// net-zero against the shared mock server (it cleans up the agent it
// creates), so it stays idempotent across repeated `sigil test` runs.
test "agent create + archive round-trip" = scenario in Studio
  expect-text "code-reviewer"
  click button "+ New Agent"
  expect-text "New Agent"
  fill input "Agent name..." "deploy-bot"
  fill input "Model (e.g. claude-sonnet-4.5)..." "claude-opus-4.5"
  click button "Create"
  expect-text "deploy-bot"
  expect-text "claude-opus-4.5"
  click text "deploy-bot"
  expect-text "Instructions"
  click button "Archive"
  expect-text "Agents"
  expect-text "code-reviewer"
  expect-no-text "deploy-bot"

// The built-in assistant: a message streams a reply into the transcript
// via `conversation.last.text <- Assist(...)`. Asserts both the user
// turn and the streamed-in assistant reply land in the conversation.
test "assistant streams a reply into the conversation" = scenario in Studio
  fill input "Ask the assistant…" "tell me about my agents"
  click button "Send"
  expect-text "tell me about my agents"
  expect-text "Assistant is thinking…"
  expect-cell assistantInput ""
  expect-text "You have 10 agents configured. code-reviewer is the busiest with 3421 runs, and summarizer-v2 has the highest success rate at 97%."
  expect-no-text "Assistant is thinking…"
