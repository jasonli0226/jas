package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

const argSummaryMax = 80

// UI receives streamed assistant text and every logged event.
type UI interface {
	Text(delta string)
	Reasoning(delta string)
	Event(typ string, data any)
}

// NewQuietUI prints nothing; with --json the event log tee carries the stream.
func NewQuietUI() UI { return quietUI{} }

type quietUI struct{}

func (quietUI) Text(string)       {}
func (quietUI) Reasoning(string)  {}
func (quietUI) Event(string, any) {}

// NewPrinter streams text to w, one "→ tool(args)" line per tool_started and
// "✗ tool: output" for failed results.
func NewPrinter(w io.Writer) UI { return &printer{w: w, atLineStart: true} }

type printer struct {
	mu          sync.Mutex
	w           io.Writer
	atLineStart bool
}

// Reasoning is not shown in one-shot output.
func (*printer) Reasoning(string) {}

func (p *printer) Text(delta string) {
	if delta == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, _ = io.WriteString(p.w, delta)
	p.atLineStart = strings.HasSuffix(delta, "\n")
}

func (p *printer) Event(typ string, data any) {
	m, ok := data.(map[string]any)
	if !ok {
		return
	}
	var line string
	switch typ {
	case EvToolStarted:
		line = fmt.Sprintf("→ %v(%s)", m["tool"], summarizeArgs(m["args"]))
	case EvToolResult:
		if st, _ := m["status"].(string); st == statusError || st == statusDenied {
			line = fmt.Sprintf("✗ %v: %s", m["tool"], firstLine(fmt.Sprint(m["output"])))
		}
	}
	if line == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.atLineStart {
		_, _ = io.WriteString(p.w, "\n")
	}
	_, _ = io.WriteString(p.w, line+"\n")
	p.atLineStart = true
}

func summarizeArgs(v any) string {
	var s string
	switch a := v.(type) {
	case json.RawMessage:
		s = string(a)
	case []byte:
		s = string(a)
	case string:
		s = a
	default:
		b, _ := json.Marshal(v)
		s = string(b)
	}
	if json.Valid([]byte(s)) {
		var compact bytes.Buffer
		if json.Compact(&compact, []byte(s)) == nil {
			s = compact.String()
		}
	}
	return truncateRunes(s, argSummaryMax)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return truncateRunes(line, argSummaryMax*2)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// uiLog forwards every appended event to a UI after writing it to the log.
type uiLog struct {
	EventLog
	ui UI
}

// NewUILog returns a log that also shows each event to ui. Use it for the one
// log shared by Run and the Executor so the UI sees tool events.
func NewUILog(l EventLog, ui UI) EventLog { return &uiLog{EventLog: l, ui: ui} }

func (l *uiLog) Append(typ string, data any) error {
	err := l.EventLog.Append(typ, data)
	l.ui.Event(typ, data)
	return err
}

func (l *uiLog) AppendID(typ string, data any) (string, error) {
	id, err := l.EventLog.AppendID(typ, data)
	l.ui.Event(typ, data)
	return id, err
}

func (l *uiLog) AppendSync(typ string, data any) error {
	err := l.EventLog.AppendSync(typ, data)
	l.ui.Event(typ, data)
	return err
}

// screenAppender is a log that can show the UI a different payload from the
// one it logs. Implemented by uiLog only.
type screenAppender interface {
	// AppendScreen logs logData and shows screenData to the UI.
	AppendScreen(typ string, logData, screenData any) error
}

var _ screenAppender = (*uiLog)(nil)

func (l *uiLog) AppendScreen(typ string, logData, screenData any) error {
	err := l.EventLog.Append(typ, logData)
	l.ui.Event(typ, screenData)
	return err
}
