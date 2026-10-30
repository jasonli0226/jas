package tui

import tea "charm.land/bubbletea/v2"

// Messages posted by a session's bridge. gen identifies the session the
// message came from, so the chat can drop output from a replaced session.
type (
	textMsg struct {
		gen int
		s   string
	}
	reasoningMsg struct {
		gen int
		s   string
	}
	eventMsg struct {
		gen  int
		typ  string
		data map[string]any
	}
)

// bridge is the core.UI of one session; it turns UI calls into tea messages.
type bridge struct {
	gen  int
	send func(tea.Msg)
}

func (b *bridge) Text(delta string)      { b.send(textMsg{b.gen, delta}) }
func (b *bridge) Reasoning(delta string) { b.send(reasoningMsg{b.gen, delta}) }

// Event forwards map payloads (what the redacting UI delivers); anything else
// is dropped.
func (b *bridge) Event(typ string, data any) {
	m, ok := data.(map[string]any)
	if !ok {
		return
	}
	b.send(eventMsg{b.gen, typ, m})
}
