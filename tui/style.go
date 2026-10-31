package tui

import "charm.land/lipgloss/v2"

// Palette: hex approximations of pi's dark theme. Styles are plain lipgloss
// values; the terminal profile downgrades colours when the blocks are
// finally printed, and NO_COLOR drops them, leaving the role markers.
var (
	textFg      = lipgloss.Color("#dcdee3")
	mutedFg     = lipgloss.Color("#9fa3ae")
	blueFg      = lipgloss.Color("#8ea2ee")
	yellowFg    = lipgloss.Color("#c8d86a")
	greenFg     = lipgloss.Color("#6fdcb2")
	orangeFg    = lipgloss.Color("#e5b56b")
	violetFg    = lipgloss.Color("#c98fe0")
	userBg      = lipgloss.Color("#26304f")
	toolOkBg    = lipgloss.Color("#1d3f33")
	toolErrBg   = lipgloss.Color("#4d2b22")
	toolOtherBg = lipgloss.Color("#34363c")
)

var (
	boldStyle      = lipgloss.NewStyle().Bold(true)
	dimStyle       = lipgloss.NewStyle().Faint(true)
	errStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	userStyle      = lipgloss.NewStyle().Background(userBg).Foreground(textFg).Padding(1, 0)
	assistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	toolStyle      = lipgloss.NewStyle().Foreground(textFg)
	fenceStyle     = lipgloss.NewStyle().Foreground(mutedFg)
	keywordStyle   = lipgloss.NewStyle().Foreground(blueFg)
	funcStyle      = lipgloss.NewStyle().Foreground(yellowFg)
	stringStyle    = lipgloss.NewStyle().Foreground(orangeFg)
	commentStyle   = lipgloss.NewStyle().Foreground(mutedFg)
	numberStyle    = lipgloss.NewStyle().Foreground(greenFg)
	codeSpanStyle  = lipgloss.NewStyle().Foreground(violetFg)
)
