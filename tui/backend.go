package tui

import "github.com/jasonli0226/jas/core"

// Backend is everything the chat needs from jas's wiring; cmd/jas implements it.
type Backend interface {
	NewSession(model string, ui core.UI, gate core.Gate) (*Live, error)
	Resume(path string, ui core.UI, gate core.Gate) (*Live, []string, error) // notices
	Sessions(exclude string) ([]core.SessionInfo, error)                     // this project, newest first
	Models() []string
	Window(model string) int
	// SetDefaultModel stores model as the config default, so a new jas
	// starts on it. The chat calls it from the /model picker's Ctrl+S.
	SetDefaultModel(model string) error
	// ProviderOf names the provider a model reference resolves to; ""
	// when unknown. The chat uses it to refuse switching a live session
	// to another provider.
	ProviderOf(model string) string
	// BareModel is the model id a reference resolves to on the provider it
	// names; the reference itself when it names none. Switching models
	// stores this, not the reference, so requests never carry a provider
	// prefix.
	BareModel(ref string) string
	SkillNames() []string
	SkillFiles() map[string]string                // SKILL.md path -> skill name
	SkillPrompt(name, text string) (string, bool) // redacted user message; false if unknown
}

// Live is one open session.
type Live struct {
	Session         *core.Session
	ID, Path, Model string
	Provider        string // provider name the session's requests go to; "" in tests
	Budget          *core.Budget
	Close           func(status string, code int) error // session_ended, then closes the log
}
