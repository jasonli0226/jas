package tui

import "github.com/jasonli0226/jas/core"

// Backend is everything the chat needs from jas's wiring; cmd/jas implements it.
type Backend interface {
	NewSession(model string, ui core.UI, gate core.Gate) (*Live, error)
	Resume(path string, ui core.UI, gate core.Gate) (*Live, []string, error) // notices
	Sessions(exclude string) ([]core.SessionInfo, error)                     // this project, newest first
	Models() []string
	Window(model string) int
	SkillNames() []string
	SkillFiles() map[string]string                // SKILL.md path -> skill name
	SkillPrompt(name, text string) (string, bool) // redacted user message; false if unknown
}

// Live is one open session.
type Live struct {
	Session         *core.Session
	ID, Path, Model string
	Budget          *core.Budget
	Close           func(status string, code int) error // session_ended, then closes the log
}
