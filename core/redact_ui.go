package core

// NewRedactingUI wraps ui so streamed text and event summaries are redacted
// before they reach the terminal. A secret split across two streamed deltas
// can slip through the per-delta pass; the event log and tee are unaffected.
func NewRedactingUI(ui UI, redact func(string) string) UI {
	return redactingUI{ui: ui, redact: redact}
}

type redactingUI struct {
	ui     UI
	redact func(string) string
}

func (u redactingUI) Text(delta string) { u.ui.Text(u.redact(delta)) }

func (u redactingUI) Event(typ string, data any) {
	d, err := redactPayload(data, u.redact)
	if err != nil {
		return // cannot redact: show nothing rather than raw data
	}
	u.ui.Event(typ, d)
}
