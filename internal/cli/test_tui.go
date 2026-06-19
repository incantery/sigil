package cli

import (
	"fmt"
	"image/color"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/incantery/mako/pkg/ir"
)

// Color palette mirrors tui.go's choices so the test runner looks
// like part of the same product as `sigil help` / `sigil explore`.
var (
	colPass   = lipgloss.Color("#22c55e") // green
	colFail   = lipgloss.Color("#ef4444") // red
	colRun    = lipgloss.Color("#fcd34d") // gold/yellow (re-uses colAccent)
	colPend   = lipgloss.Color("#52525b") // dim gray
	colHeader = lipgloss.Color("#a78bfa") // violet (re-uses colPrimary)
	colFaint  = lipgloss.Color("#71717a") // muted (re-uses colMuted)
	colText   = lipgloss.Color("#e4e4e7") // off-white body text
)

var (
	tuiHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colHeader)

	tuiTestNameStyle = lipgloss.NewStyle().
				Foreground(colText).
				Bold(true)

	tuiAppStyle = lipgloss.NewStyle().
			Foreground(colFaint)

	tuiPassStyle  = lipgloss.NewStyle().Foreground(colPass).Bold(true)
	tuiFailStyle  = lipgloss.NewStyle().Foreground(colFail).Bold(true)
	tuiRunStyle   = lipgloss.NewStyle().Foreground(colRun).Bold(true)
	tuiPendStyle  = lipgloss.NewStyle().Foreground(colPend)
	tuiFaintStyle = lipgloss.NewStyle().Foreground(colFaint)

	tuiVerbStyle    = lipgloss.NewStyle().Foreground(colText)
	tuiPayloadStyle = lipgloss.NewStyle().Foreground(colFaint).Italic(true)
	tuiTimingStyle  = lipgloss.NewStyle().Foreground(colFaint)
)

// Braille-dot spinner, 100ms per frame. Hand-rolled because
// charm.land/bubbles/v2 isn't released as a v2 module yet (per
// project deps memory). 10 frames is the bubbles default set.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerInterval = 100 * time.Millisecond

// stepState mirrors the e2eEvent step lifecycle: pending → running →
// passed | failed. Verb + payload come from step_start; At + failure
// come from step_end. Line/Col are preserved so the failure card
// can show source-location hints like "/file.mako:6:3".
type stepState struct {
	Verb    string
	Payload map[string]any
	Status  string // "pending" | "running" | "passed" | "failed"
	At      int64
	Failure string
	Line    int
	Col     int
}

// testState is one row of the TUI's running list. Pre-populated as
// pending at startup; transitions through running → passed | failed
// as events arrive.
type testState struct {
	Name    string
	AppName string
	Status  string // "pending" | "running" | "passed" | "failed"
	Steps   []stepState
	DurMS   int64
	Failure string
}

// runModel is the bubbletea program for app-target test runs. State
// is pure — every Update returns a new model — but we hold pointers
// into the slice for in-place step updates, which is fine because
// Update is sequential.
type runModel struct {
	width        int
	target       string
	sourcePath   string // file path for the run header and source-location hints
	tests        []testState
	spinnerFrame int
	startedAt    time.Time
	done         bool
}

func newRunModel(tests []ir.Test, target, sourcePath string) runModel {
	rows := make([]testState, len(tests))
	for i, t := range tests {
		rows[i] = testState{
			Name:    t.Name,
			AppName: t.App,
			Status:  "pending",
		}
	}
	return runModel{
		tests:      rows,
		target:     target,
		sourcePath: sourcePath,
		startedAt:  time.Now(),
		width:      80, // sane default until WindowSizeMsg lands
	}
}

func (m runModel) Init() tea.Cmd { return spinnerTick() }

func spinnerTick() tea.Cmd {
	return tea.Tick(spinnerInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Messages from the test-driver goroutine. eventMsg carries one
// raw e2eEvent paired with the test index it belongs to; the model
// translates it into a step-state update. testStartMsg / testDoneMsg
// bracket each test so the model can mark status transitions even
// for the legacy view-target path (which doesn't surface events).
type tickMsg time.Time
type testStartMsg int
type eventMsg struct {
	idx int
	e   e2eEvent
}
type testDoneMsg struct {
	idx    int
	result testResult
}
type allDoneMsg struct{}

func (m runModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tickMsg:
		m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
		if m.done {
			return m, nil
		}
		return m, spinnerTick()

	case testStartMsg:
		idx := int(msg)
		if idx >= 0 && idx < len(m.tests) {
			m.tests[idx].Status = "running"
		}
		return m, nil

	case eventMsg:
		if msg.idx >= 0 && msg.idx < len(m.tests) {
			applyEventToTest(&m.tests[msg.idx], msg.e)
		}
		return m, nil

	case testDoneMsg:
		if msg.idx >= 0 && msg.idx < len(m.tests) {
			t := &m.tests[msg.idx]
			if msg.result.Passed {
				t.Status = "passed"
			} else {
				t.Status = "failed"
				t.Failure = msg.result.Failure
			}
			t.DurMS = msg.result.DurMS
		}
		return m, nil

	case allDoneMsg:
		m.done = true
		return m, tea.Quit

	case tea.KeyPressMsg:
		if s := msg.String(); s == "ctrl+c" || s == "q" {
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// applyEventToTest mutates a testState row in place based on the
// incoming bundle event. Out-of-range step indices are ignored
// defensively (they'd indicate a codegen bug).
func applyEventToTest(t *testState, e e2eEvent) {
	switch e.Type {
	case "scenario_start":
		// Status already "running" via testStartMsg; nothing more.
	case "step_start":
		idx := e.Step - 1
		// Grow steps slice to fit (codegen always emits step_start
		// before step_end so we shouldn't need to grow elsewhere).
		for len(t.Steps) <= idx {
			t.Steps = append(t.Steps, stepState{Status: "pending"})
		}
		t.Steps[idx] = stepState{
			Verb:    e.Verb,
			Payload: e.Payload,
			Status:  "running",
			Line:    e.Line,
			Col:     e.Col,
		}
	case "step_end":
		idx := e.Step - 1
		if idx < 0 || idx >= len(t.Steps) {
			return
		}
		ok := e.OK != nil && *e.OK
		if ok {
			t.Steps[idx].Status = "passed"
		} else {
			t.Steps[idx].Status = "failed"
			t.Steps[idx].Failure = e.Failure
		}
		t.Steps[idx].At = e.At
	case "scenario_end":
		// Status transition is driven by testDoneMsg (which carries
		// the authoritative testResult); ignore the bundle's own
		// scenario_end to avoid race-ordering surprises.
	case "runner_error":
		// Surface as a failure-shaped pseudo-step so the user sees it.
		t.Failure = e.Failure
	}
}

// cardWidth picks the outer width for the main + failure cards.
// Adapts to terminal width but caps so cards stay visually coherent
// on wide windows. The minimum guards against pathologically narrow
// terminals where the title wouldn't fit.
func cardWidth(termWidth int) int {
	const (
		minWidth = 50
		maxWidth = 72
	)
	w := termWidth - 4 // 2-col left margin + breathing room on the right
	switch {
	case w < minWidth:
		return minWidth
	case w > maxWidth:
		return maxWidth
	default:
		return w
	}
}

// formatDuration renders ms as a compact human string. Sub-second
// stays in ms; from one second up uses one decimal of s. Keeps the
// timing column narrow.
func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

// titledCard renders the brand-first card shape: a rounded box with
// the title embedded in the top border. Standard lipgloss borders
// don't support inline titles, so we draw the top row manually,
// then let lipgloss style the body with the sides + bottom of a
// borderless box (we render sides + bottom separately too so all
// borders share the same color).
//
// outerWidth is the full width including the two border columns.
// borderColor colors all border characters (top, sides, bottom);
// body keeps its own internal styling.
func titledCard(title, body string, outerWidth int, borderColor color.Color) string {
	inner := outerWidth - 2 // subtract two side-border chars
	if inner < 4 {
		inner = 4
	}

	borderStyle := lipgloss.NewStyle().Foreground(borderColor)

	// Top: ╭─ <title> ─...─╮
	titleRendered := lipgloss.NewStyle().
		Foreground(borderColor).
		Bold(true).
		Render(title)
	titleWidth := lipgloss.Width(titleRendered)
	// Account for "╭─ " (3) + " " (1) + "╮" (1) = 5 fixed chars, plus title
	dashTail := inner - 3 - titleWidth - 1 - 1
	if dashTail < 1 {
		dashTail = 1
	}
	top := borderStyle.Render("╭─ ") + titleRendered + borderStyle.Render(" "+strings.Repeat("─", dashTail)+"╮")

	// Body: each line gets `│ <padded> │`. Pad to inner-2 (one space
	// of left padding, one of right) using visual width.
	side := borderStyle.Render("│")
	contentWidth := inner - 2
	var bodyBuf strings.Builder
	for _, line := range strings.Split(body, "\n") {
		pad := contentWidth - lipgloss.Width(line)
		if pad < 0 {
			pad = 0
		}
		bodyBuf.WriteString(side + " " + line + strings.Repeat(" ", pad) + " " + side + "\n")
	}

	// Bottom: ╰───...───╯
	bottom := borderStyle.Render("╰" + strings.Repeat("─", inner) + "╯")

	return top + "\n" + bodyBuf.String() + bottom
}

// flexRow places `left` and `right` at the edges of a fixed-width
// row, padding the middle with spaces. Used for test rows (name on
// the left, duration on the right) so the duration column lines up
// inside the card regardless of name length.
func flexRow(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m runModel) View() tea.View {
	width := cardWidth(m.width)
	contentWidth := width - 4 // 2 border + 2 padding chars

	// --- Main card body ---
	var body strings.Builder

	if m.sourcePath != "" {
		body.WriteString(tuiFaintStyle.Render(m.sourcePath))
		body.WriteString("\n")
	}

	// "<appName>  ·  target <name>" — picks the first non-empty app
	// from the tests list, which v0 collapses to one app per file.
	appLabel := firstAppName(m.tests)
	if appLabel != "" {
		body.WriteString(
			tuiTestNameStyle.Render(appLabel) +
				tuiFaintStyle.Render("  ·  ") +
				tuiFaintStyle.Render("target "+m.target),
		)
	} else {
		body.WriteString(tuiFaintStyle.Render("target " + m.target))
	}
	body.WriteString("\n\n")

	for _, t := range m.tests {
		body.WriteString(renderCardTestRow(t, contentWidth, m.spinnerFrame))
		body.WriteString("\n")
	}

	mainCard := titledCard("◆ sigil  test", strings.TrimRight(body.String(), "\n"), width, colPrimary)

	// --- Failure cards (one per failed test) ---
	var failCards strings.Builder
	for _, t := range m.tests {
		if t.Status != "failed" {
			continue
		}
		failCards.WriteString("\n")
		failCards.WriteString(renderFailureCard(t, m.sourcePath, width))
		failCards.WriteString("\n")
	}

	// --- Footer summary (outside cards) ---
	passed, failed, running, pending := tallyStatuses(m.tests)
	elapsed := time.Since(m.startedAt).Truncate(time.Millisecond)

	var summary string
	if running == 0 && pending == 0 {
		summary = fmt.Sprintf("%s   %s   %s   %s",
			tuiPassStyle.Render(fmt.Sprintf("%d passed", passed)),
			tuiFaintStyle.Render("·"),
			tuiFailStyle.Render(fmt.Sprintf("%d failed", failed)),
			tuiFaintStyle.Render(elapsed.String()),
		)
	} else {
		summary = fmt.Sprintf("%s   %s   %s   %s   %s   %s   %s",
			tuiPassStyle.Render(fmt.Sprintf("%d passed", passed)),
			tuiFaintStyle.Render("·"),
			tuiFailStyle.Render(fmt.Sprintf("%d failed", failed)),
			tuiFaintStyle.Render("·"),
			tuiRunStyle.Render(fmt.Sprintf("%d running", running)),
			tuiPendStyle.Render(fmt.Sprintf("%d pending", pending)),
			tuiFaintStyle.Render(elapsed.String()),
		)
	}

	var out strings.Builder
	out.WriteString("\n")
	out.WriteString(indent(mainCard, "  "))
	out.WriteString("\n")
	if failCards.Len() > 0 {
		out.WriteString(indent(strings.TrimRight(failCards.String(), "\n"), "  "))
		out.WriteString("\n")
	}
	out.WriteString("\n  ")
	out.WriteString(summary)
	out.WriteString("\n")

	return tea.NewView(out.String())
}

// renderCardTestRow emits one row inside the main card. Pending →
// dim dot + dim name; running → spinner + name + (optionally) the
// currently-running step on a sub-row; passed/failed → check/cross
// + name + right-aligned duration.
func renderCardTestRow(t testState, width, spinFrame int) string {
	switch t.Status {
	case "pending":
		left := tuiPendStyle.Render("·  ") + tuiPendStyle.Render(t.Name)
		return left

	case "running":
		left := tuiRunStyle.Render(spinnerFrames[spinFrame]) + "  " + tuiTestNameStyle.Render(t.Name)
		// If a step is in flight, surface its intent as a dim sub-line.
		var b strings.Builder
		b.WriteString(left)
		for _, s := range t.Steps {
			if s.Status == "running" {
				b.WriteString("\n     " + tuiRunStyle.Render(spinnerFrames[spinFrame]) + "  " + tuiFaintStyle.Render(intentLabel(s)))
			}
		}
		return b.String()

	case "passed":
		left := tuiPassStyle.Render("✓") + "  " + tuiTestNameStyle.Render(t.Name)
		right := tuiTimingStyle.Render(formatDuration(t.DurMS))
		return flexRow(left, right, width)

	case "failed":
		left := tuiFailStyle.Render("✗") + "  " + tuiTestNameStyle.Render(t.Name)
		right := tuiTimingStyle.Render(formatDuration(t.DurMS))
		return flexRow(left, right, width)
	}
	return ""
}

// renderFailureCard builds the per-failure diagnostic card: title
// names the failing test, body shows the failed step's intent + the
// failure message + the source location. Foreshadows the Pillar-3
// diagnosis block — for now it's a clean container, the IR-aware
// hints come later.
func renderFailureCard(t testState, sourcePath string, width int) string {
	contentWidth := width - 4

	var body strings.Builder

	// Find the failed step (codegen short-circuits on first failure,
	// so there's at most one).
	var failed stepState
	for _, s := range t.Steps {
		if s.Status == "failed" {
			failed = s
			break
		}
	}

	if failed.Verb != "" {
		left := tuiVerbStyle.Render(intentLabel(failed))
		right := tuiTimingStyle.Render("at " + formatDuration(failed.At))
		body.WriteString(flexRow(left, right, contentWidth))
		body.WriteString("\n\n")
	}

	failureMsg := failed.Failure
	if failureMsg == "" {
		failureMsg = t.Failure
	}
	if failureMsg != "" {
		body.WriteString(tuiFailStyle.Render(failureMsg))
		body.WriteString("\n")
	}

	if failed.Line > 0 && sourcePath != "" {
		body.WriteString("\n")
		body.WriteString(tuiFaintStyle.Render(fmt.Sprintf("%s:%d:%d", sourcePath, failed.Line, failed.Col)))
	}

	title := "FAILED ─ " + t.Name
	return titledCard(title, body.String(), width, colFail)
}

// intentLabel renders a step's verb + payload as a single-line
// human-readable label. For expect-text, quotes the text and styles
// it dim+italic so the verb reads as the action and the literal
// reads as the data.
func intentLabel(s stepState) string {
	if text, ok := s.Payload["text"].(string); ok && s.Verb == "expect-text" {
		return s.Verb + " " + tuiPayloadStyle.Render(`"`+text+`"`)
	}
	return s.Verb
}

// tallyStatuses counts tests by status for the footer summary.
func tallyStatuses(tests []testState) (passed, failed, running, pending int) {
	for _, t := range tests {
		switch t.Status {
		case "passed":
			passed++
		case "failed":
			failed++
		case "running":
			running++
		case "pending":
			pending++
		}
	}
	return
}

// firstAppName returns the first non-empty AppName across tests.
// v0 collapses to one app per file, so this is the de-facto label.
func firstAppName(tests []testState) string {
	for _, t := range tests {
		if t.AppName != "" {
			return t.AppName
		}
	}
	return ""
}

// indent prefixes every line of s with `prefix`. Used to push the
// rendered cards in from the left margin without baking the indent
// into the card-building helpers (which would complicate width math).
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = prefix + ln
	}
	return strings.Join(lines, "\n")
}

// renderTuiTest emits one test's block. Pending tests are dim
// one-liners; running tests expand into the per-step view with a
// spinner on the in-flight step; passed/failed collapse to a
// one-line summary with duration (and a wrapped failure note for
// failures).
func renderTuiTest(b *strings.Builder, t testState, spinFrame int) {
	switch t.Status {
	case "pending":
		fmt.Fprintf(b, "  %s  %s\n",
			tuiPendStyle.Render("·"),
			tuiPendStyle.Render(fmt.Sprintf("%s · %s", t.AppName, t.Name)))

	case "running":
		header := fmt.Sprintf("%s  %s · %s",
			tuiRunStyle.Render(spinnerFrames[spinFrame]),
			tuiAppStyle.Render(t.AppName),
			tuiTestNameStyle.Render(t.Name),
		)
		fmt.Fprintf(b, "  %s\n", header)
		for _, s := range t.Steps {
			renderTuiStep(b, s, spinFrame)
		}

	case "passed":
		fmt.Fprintf(b, "  %s  %s · %s   %s\n",
			tuiPassStyle.Render("✓"),
			tuiAppStyle.Render(t.AppName),
			tuiTestNameStyle.Render(t.Name),
			tuiTimingStyle.Render(formatDuration(t.DurMS)),
		)

	case "failed":
		fmt.Fprintf(b, "  %s  %s · %s   %s\n",
			tuiFailStyle.Render("✗"),
			tuiAppStyle.Render(t.AppName),
			tuiTestNameStyle.Render(t.Name),
			tuiTimingStyle.Render(formatDuration(t.DurMS)),
		)
		// Show the step that failed, plus the failure message.
		for _, s := range t.Steps {
			if s.Status == "failed" {
				renderTuiStep(b, s, spinFrame)
			}
		}
		if t.Failure != "" {
			fmt.Fprintf(b, "     %s\n", tuiFailStyle.Render("└─ "+t.Failure))
		}
	}
}

// renderTuiStep emits one step line. Pending → dim dot; running →
// spinner + yellow; passed → green check; failed → red cross + the
// failure detail line.
//
// Per-step timing uses "at Nms" because it's elapsed-from-scenario-start,
// not per-step duration — bare "Nms" reads like the step took that
// long, which it didn't (especially with --slowmo where most of the
// elapsed time is the pause).
func renderTuiStep(b *strings.Builder, s stepState, spinFrame int) {
	intent := s.Verb
	if text, ok := s.Payload["text"].(string); ok && s.Verb == "expect-text" {
		intent = fmt.Sprintf(`%s %s`, s.Verb, tuiPayloadStyle.Render(`"`+text+`"`))
	}

	switch s.Status {
	case "pending":
		fmt.Fprintf(b, "      %s  %s\n", tuiPendStyle.Render("·"), tuiPendStyle.Render(intent))
	case "running":
		fmt.Fprintf(b, "      %s  %s\n", tuiRunStyle.Render(spinnerFrames[spinFrame]), tuiVerbStyle.Render(intent))
	case "passed":
		fmt.Fprintf(b, "      %s  %s   %s\n",
			tuiPassStyle.Render("✓"),
			tuiVerbStyle.Render(intent),
			tuiTimingStyle.Render("at "+formatDuration(s.At)),
		)
	case "failed":
		fmt.Fprintf(b, "      %s  %s   %s\n",
			tuiFailStyle.Render("✗"),
			tuiVerbStyle.Render(intent),
			tuiTimingStyle.Render("at "+formatDuration(s.At)),
		)
		if s.Failure != "" {
			fmt.Fprintf(b, "         %s\n", tuiFailStyle.Render(s.Failure))
		}
	}
}

// runTestsTUI is the entry point used by testCmd's RunE when stdout
// is a TTY. It creates the bubbletea program, drives tests in a
// background goroutine that forwards events via p.Send, and blocks
// on p.Run() until the program quits. Returns the collected results
// for the caller to compute the exit code.
//
// runOne is the per-test driver injected by the caller — it knows
// how to dispatch to runAppTest (compiled-bundle, surfaces events)
// or runOneTest (legacy view-target, doesn't). For view-target the
// TUI shows the test transitioning through running → passed/failed
// without per-step detail, which is acceptable v0 behavior.
func runTestsTUI(tests []ir.Test, target, sourcePath string, runOne func(ir.Test, func(e2eEvent)) testResult) []testResult {
	model := newRunModel(tests, target, sourcePath)
	p := tea.NewProgram(model)

	results := make([]testResult, len(tests))
	var resultsLock sync.Mutex

	go func() {
		for i, t := range tests {
			p.Send(testStartMsg(i))
			res := runOne(t, func(e e2eEvent) {
				p.Send(eventMsg{idx: i, e: e})
			})
			resultsLock.Lock()
			results[i] = res
			resultsLock.Unlock()
			p.Send(testDoneMsg{idx: i, result: res})
		}
		p.Send(allDoneMsg{})
	}()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "sigil test: TUI error: %v\n", err)
	}

	resultsLock.Lock()
	defer resultsLock.Unlock()
	return results
}
