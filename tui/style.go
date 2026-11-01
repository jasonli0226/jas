package tui

import "charm.land/lipgloss/v2"

// Palette: hex approximations of pi's dark theme. Styles are plain lipgloss
// values; the terminal profile downgrades colours when the blocks are
// finally printed, and NO_COLOR drops them, leaving the role markers. The
// band colours are picked to stay distinct on 16-colour terminals (blue,
// green, red, black).
var (
	textFg      = lipgloss.Color("#dcdee3")
	mutedFg     = lipgloss.Color("#9fa3ae")
	blueFg      = lipgloss.Color("#8ea2ee")
	yellowFg    = lipgloss.Color("#c8d86a")
	greenFg     = lipgloss.Color("#6fdcb2")
	orangeFg    = lipgloss.Color("#e5b56b")
	violetFg    = lipgloss.Color("#c98fe0")
	userBg      = lipgloss.Color("#1f2d5c")
	toolOkBg    = lipgloss.Color("#1e4a2a")
	toolErrBg   = lipgloss.Color("#4d2b22")
	toolOtherBg = lipgloss.Color("#34363c")

	// Edit diffs: a removed or added line, and its changed words on top.
	diffDelBg     = lipgloss.Color("#3f1d1d")
	diffAddBg     = lipgloss.Color("#1c3a24")
	diffDelWordBg = lipgloss.Color("#7a2a2a")
	diffAddWordBg = lipgloss.Color("#2a6b3c")
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
	ctxWarnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	ctxHotStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	codeSpanStyle  = lipgloss.NewStyle().Foreground(violetFg)
)
