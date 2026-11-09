package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

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
	// The removed band is a touch more saturated than the swatch's #3f1d1d,
	// which lands on black on 16 colours; this one stays red there.
	diffDelBg     = lipgloss.Color("#4a1414")
	diffAddBg     = lipgloss.Color("#1c3a24")
	diffDelWordBg = lipgloss.Color("#7a2a2a")
	diffAddWordBg = lipgloss.Color("#2a6b3c")

	// agentHues tell farm agents apart, in plan order; red stays for errors.
	agentHues = []color.Color{blueFg, orangeFg, violetFg, greenFg, yellowFg}
)

// agentStyle colors a farm agent's name with its hue.
func agentStyle(hue int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(agentHues[hue%len(agentHues)])
}

var (
	doneStyle      = lipgloss.NewStyle().Foreground(greenFg)
	boldStyle      = lipgloss.NewStyle().Bold(true)
	dimStyle       = lipgloss.NewStyle().Faint(true)
	errStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	userStyle      = lipgloss.NewStyle().Background(userBg).Foreground(textFg).Padding(0, 0)
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

// perLine renders s with st one line at a time. lipgloss pads a multi-line
// string to its widest line, which leaves trailing spaces in copied text;
// a foreground-only style looks the same either way.
func perLine(st lipgloss.Style, s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = st.Render(l)
	}
	return strings.Join(lines, "\n")
}
