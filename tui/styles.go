package tui

import "github.com/charmbracelet/lipgloss"

// Color palette — high contrast, readable
var (
	colorBg       = lipgloss.Color("#0d1117") // GitHub dark bg
	colorFg       = lipgloss.Color("#e6edf3") // Bright white
	colorMainText = lipgloss.Color("#e6edf3") // Bright white for readability
	colorMuted    = lipgloss.Color("#8b949e") // GitHub muted gray
	colorAccent   = lipgloss.Color("#f0883e") // Vibrant orange
	colorAccent2  = lipgloss.Color("#a5d6ff") // Bright blue
	colorSuccess  = lipgloss.Color("#7ee787") // Vibrant green
	colorError    = lipgloss.Color("#ff7b72") // Bright red
	colorWarning  = lipgloss.Color("#d29922") // Amber

	// Code block colors — syntax highlighting palette
	codeKeyword  = lipgloss.Color("#ff7b72") // Red/pink
	codeString   = lipgloss.Color("#a5d6ff") // Light blue
	codeNumber   = lipgloss.Color("#79c0ff") // Blue
	codeComment  = lipgloss.Color("#8b949e") // Gray
	codeFunction = lipgloss.Color("#d2a8ff") // Purple
	codeType     = lipgloss.Color("#79c0ff") // Blue
	codeOperator = lipgloss.Color("#ff7b72") // Red
	codeProperty = lipgloss.Color("#79c0ff") // Blue
	codeBlockBg  = lipgloss.Color("#161b22") // Slightly lighter than bg
	codeBlockBrd = lipgloss.Color("#30363d") // Border color

	headerStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true).
			Padding(0, 1)

	modeStyle = lipgloss.NewStyle().
			Foreground(colorAccent2).
			Italic(true).
			Padding(0, 1)

	infoStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 1)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	statusKeyStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Padding(0, 1)

	statusInfoStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 1)

	sepStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#30363d")) // GitHub-style separator
)

// Spinner frames — blinking circle (Claude Code style).
// ◉ = filled (active), ○ = empty (blink off).
var spinnerFrames = []string{"◉", "○"}

// Phase colors — used to color the spinner per phase.
var (
	spinnerColorGrounding = lipgloss.Color("#f0883e") // orange
	spinnerColorPlanning  = lipgloss.Color("#a5d6ff") // blue
	spinnerColorExecuting = lipgloss.Color("#7ee787") // green
	spinnerColorDefault   = lipgloss.Color("#8b949e") // muted gray
	spinnerColorDone      = lipgloss.Color("#7ee787") // green
	spinnerColorError     = lipgloss.Color("#ff7b72") // red
)
