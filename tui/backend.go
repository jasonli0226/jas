package tui

import (
	"context"
	"io"

	"github.com/jasonli0226/jas/core"
)

// SettingLine is one row of /settings (effective value + source).
// Options is the closed set for the value picker; empty means free-text.
type SettingLine struct {
	Key, Value, Source string
	Options            []string
}

// ContextFile is one context entry offered to the session; Loaded is the
// trust decision on it.
type ContextFile struct {
	Path   string
	Loaded bool
}

// Backend is everything the chat needs from jas's wiring; cmd/jas implements it.
type Backend interface {
	NewSession(model string, ui core.UI, gate core.Gate) (*Live, error)
	Resume(path string, ui core.UI, gate core.Gate) (*Live, []string, error) // notices
	Sessions(exclude string) ([]core.SessionInfo, error)                     // this project, newest first
	Models() []string
	Window(model string) (window int, known bool) // known=false: a default guess
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
	// MemoryReport is the /memory text: mode, dirs, index size and note counts.
	MemoryReport() string
	// ContextFiles lists the context file and project skills offered to the
	// chat (the start banner's [Context] rows); empty hides the section.
	ContextFiles() []ContextFile
	SkillFiles() map[string]string                // SKILL.md path -> skill name
	SkillPrompt(name, text string) (string, bool) // redacted user message; false if unknown
	// Farmer is the farm port; nil when this chat cannot run farms.
	Farmer() Farmer
	// DefaultSettingsScope is the write scope /settings uses when none is given
	// (local).
	DefaultSettingsScope() string
	// ListSettings returns effective settings with sources for /settings.
	ListSettings() []SettingLine
	// GetSetting returns one effective setting.
	GetSetting(key string) (SettingLine, error)
	// SetSetting writes key=value to scope (global|project|local); empty scope
	// uses DefaultSettingsScope.
	SetSetting(scope, key, value string) error
	// UnsetSetting removes key from scope.
	UnsetSetting(scope, key string) error
	// SettingNote is what the confirmation of a write (or, with unset, an
	// unset) of key to scope adds, e.g. " (overrides profile base)" when a
	// --in-profile write overrides a parent profile's value; "" for none.
	SettingNote(scope, key string, unset bool) string
	// Reload rediscovers skills and rebuilds AGENTS.md / project context into
	// s under the trust rules. ask is called when trust must be re-confirmed
	// (nil refuses like a non-interactive skip). Notices describe what loaded.
	Reload(s *core.Session, ask func(string) string) (notices []string, err error)
	// Profile is the chat's active profile name.
	Profile() string
	// Profiles lists the profiles there are, sorted, default included.
	Profiles() []string
	// SetDefaultProfile stores name as profile in the global config, so a new
	// jas starts on it. The chat calls it from the /profile picker's Ctrl+S.
	SetDefaultProfile(name string) error
	// SwitchProfile makes name the chat's active profile and reloads s like
	// Reload does (profile skills and AGENTS.md, project context, read
	// roots), keeping the conversation. ask is Reload's trust prompt.
	SwitchProfile(s *core.Session, name string, ask func(string) string) (ProfileSwitch, error)
	// Shell runs the owner's ! command in the project root with the owner's
	// environment, writing stdout and stderr to out. It returns the exit code
	// and an error with an ExitCode method for a non-zero exit, or why the
	// command could not run.
	Shell(ctx context.Context, command string, out io.Writer) (code int, err error)
}

// ProfileSwitch is what a profile switch changed. Notices describe what
// loaded. Live holds the new effective values of the live settings
// (approve, model, hints, farm_bell) that differ from before the switch;
// Deferred names the other settings whose effective value changed, which
// wait for /new.
type ProfileSwitch struct {
	Notices  []string
	Live     map[string]string
	Deferred []string
}

// Live is one open session.
type Live struct {
	Session         *core.Session
	ID, Path, Model string
	Provider        string // provider name the session's requests go to; "" in tests
	Budget          *core.Budget
	Close           func(status string, code int) error // session_ended, then closes the log
	Notices         []string                            // warnings from opening a new session, shown once
}
