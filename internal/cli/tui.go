package cli

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Palette. Picked from a desaturated violet-and-gold set — primary is the
// "sigil"/magic association; accent is reserved for hotkeys.
var (
	colPrimary = lipgloss.Color("#a78bfa")
	colAccent  = lipgloss.Color("#fcd34d")
	colMuted   = lipgloss.Color("#71717a")
	colFg      = lipgloss.Color("#fafafa")
)

var (
	logoStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colPrimary).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colPrimary).
			Padding(0, 4).
			Align(lipgloss.Center)

	taglineStyle = lipgloss.NewStyle().
			Foreground(colMuted).
			Italic(true).
			MarginTop(1)

	sectionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colPrimary).
			MarginTop(1).
			MarginBottom(1)

	cmdNameStyle = lipgloss.NewStyle().
			Foreground(colFg).
			Bold(true)

	argStyle = lipgloss.NewStyle().
			Foreground(colMuted)

	keyStyle = lipgloss.NewStyle().
			Foreground(colAccent).
			Bold(true)

	descStyle = lipgloss.NewStyle().
			Foreground(colMuted)

	footerStyle = lipgloss.NewStyle().
			Foreground(colMuted).
			Faint(true).
			MarginTop(2)
)

type helpModel struct {
	width  int
	height int
}

func newHelpModel() helpModel { return helpModel{} }

func (m helpModel) Init() tea.Cmd { return nil }

func (m helpModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
	}
	return m, nil
}

type cmdEntry struct {
	name, args, desc string
}

type keyEntry struct {
	keys, desc string
}

var commands = []cmdEntry{
	{"run", "<file.mako>", "Compile a Sigil file and serve it over HTTP"},
	{"check", "<file.mako>", "Validate a Sigil file (no server). --json for structured output"},
	{"describe", "<file.mako>", "Print a tree description of what the file renders"},
	{"fmt", "<file.mako>", "Print the canonical formatting of a Sigil file (--write)"},
	{"version", "", "Print version info"},
}

var keys = []keyEntry{
	{"q · ctrl+c · esc", "Quit"},
}

func (m helpModel) View() tea.View {
	logo := logoStyle.Render("S I G I L")
	tagline := taglineStyle.Render("A semantic UI compiler with renderer-agnostic IR.")

	cmdLines := []string{sectionStyle.Render("COMMANDS")}
	for _, c := range commands {
		cmdLines = append(cmdLines, renderCommand(c))
	}
	cmdBlock := lipgloss.JoinVertical(lipgloss.Left, cmdLines...)

	keyLines := []string{sectionStyle.Render("KEYS")}
	for _, k := range keys {
		keyLines = append(keyLines, renderKey(k))
	}
	keyBlock := lipgloss.JoinVertical(lipgloss.Left, keyLines...)

	footer := footerStyle.Render("press q to quit")

	body := lipgloss.JoinVertical(lipgloss.Left,
		cmdBlock,
		keyBlock,
		footer,
	)

	doc := lipgloss.JoinVertical(lipgloss.Center,
		logo,
		tagline,
		body,
	)

	// Center the whole document in the terminal — handles narrow and wide
	// windows alike. Top padding gives the logo a little air.
	framed := lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		AlignHorizontal(lipgloss.Center).
		AlignVertical(lipgloss.Center).
		Render(doc)

	v := tea.NewView(framed)
	v.AltScreen = true
	return v
}

const (
	cmdColumn = 22
	keyColumn = 22
)

func renderCommand(c cmdEntry) string {
	name := cmdNameStyle.Render(c.name)
	left := name
	if c.args != "" {
		left = name + " " + argStyle.Render(c.args)
	}
	return "  " + left + pad(cmdColumn-lipgloss.Width(left)) + descStyle.Render(c.desc)
}

func renderKey(k keyEntry) string {
	left := keyStyle.Render(k.keys)
	return "  " + left + pad(keyColumn-lipgloss.Width(left)) + descStyle.Render(k.desc)
}

func pad(n int) string {
	if n < 1 {
		return " "
	}
	return strings.Repeat(" ", n)
}
