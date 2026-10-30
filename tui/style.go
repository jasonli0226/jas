package tui

import "charm.land/lipgloss/v2"

// Styles are plain lipgloss values; the terminal profile downgrades colours
// when the blocks are finally printed.
var (
	boldStyle    = lipgloss.NewStyle().Bold(true)
	dimStyle     = lipgloss.NewStyle().Faint(true)
	errStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	userStyle    = lipgloss.NewStyle().Bold(true)
	codeBox      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8")).Padding(0, 1)
	stringStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	commentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	numberStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)
