package farm

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/jasonli0226/jas/core"
)

// progressArgRunes caps the argument shown on a tool line.
const progressArgRunes = 80

// Progress prints one plain line per farm event and per worker tool call, so
// a farm run without --json shows what it is doing. Lines are redacted and
// sanitized; safe for concurrent use.
type Progress struct {
	mu     sync.Mutex
	w      io.Writer
	redact func(string) string
}

// NewProgress writes progress lines to w.
func NewProgress(w io.Writer, redact func(string) string) *Progress {
	return &Progress{w: w, redact: redact}
}

func (p *Progress) print(line string) {
	if p == nil || line == "" {
		return
	}
	if p.redact != nil {
		line = p.redact(line)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintln(p.w, core.Sanitize(line))
}

// farmLine renders a farm.jsonl event, or "" for events not shown.
func farmLine(typ string, m map[string]any) string {
	part := fmt.Sprint(m["part"])
	switch typ {
	case EvPartStarted:
		return fmt.Sprintf("[%s] started (%v)", part, m["role"])
	case EvPartCheck:
		if passed(m) {
			return fmt.Sprintf("[%s] part_check passed (%s)", part, dur(m))
		}
		return fmt.Sprintf("[%s] part_check failed (%s), %s", part, exitText(m), dur(m))
	case EvPartEnded:
		if m["status"] == StatusFailed {
			return fmt.Sprintf("[%s] failed: %v", part, m["reason"])
		}
		return strings.TrimSpace(fmt.Sprintf("[%s] %v %v", part, m["status"], orEmpty(m["diffstat"])))
	case EvBoardPosted:
		if m["topic"] == TopicDone {
			return "" // the part_ended line says it
		}
		return fmt.Sprintf("[%s] board: %v %s", part, m["topic"], orEmpty(m["name"]))
	case EvOwnerDecision:
		if p, ok := m["part"]; ok {
			return fmt.Sprintf("[%v] decision: %v", p, m["choice"])
		}
		return fmt.Sprintf("decision: %v", m["choice"])
	case EvIntegrateStarted:
		return "integrate: merging " + joinAny(m["merge"])
	case EvIntegrateCheck:
		if passed(m) {
			return "integrate: check passed on the merge"
		}
		return fmt.Sprintf("integrate: check failed on the merge (%s); starting the integrator", exitText(m))
	case EvIntegrateEnded:
		if m["status"] == StatusFailed {
			return fmt.Sprintf("integrate: failed: %v", m["reason"])
		}
		return strings.TrimSpace("integrate: done " + orEmpty(m["diffstat"]))
	case EvCheckRan:
		if passed(m) {
			return fmt.Sprintf("check: passed (%s)", dur(m))
		}
		return fmt.Sprintf("check: failed (%s), %s", exitText(m), dur(m))
	case EvBudgetStop:
		return fmt.Sprintf("budget stop: %v %s", m["reason"], orEmpty(m["detail"]))
	}
	return ""
}

// toolLine renders a worker's tool_started event: the tool and its main
// argument (path, command or pattern).
func toolLine(part string, m map[string]any) string {
	var args map[string]any
	if raw, ok := m["args"].(json.RawMessage); ok {
		_ = json.Unmarshal(raw, &args)
	}
	arg := ""
	for _, k := range []string{"path", "command", "pattern"} {
		if v, ok := args[k].(string); ok && v != "" {
			arg = firstLine(v)
			break
		}
	}
	return strings.TrimSpace(fmt.Sprintf("[%s] → %v %s", part, m["tool"], clipRunes(arg, progressArgRunes)))
}

func passed(m map[string]any) bool { return num(m["exit_code"]) == 0 && m["timed_out"] != true }

func exitText(m map[string]any) string {
	if m["timed_out"] == true {
		return "timed out"
	}
	return fmt.Sprintf("exit %d", num(m["exit_code"]))
}

func dur(m map[string]any) string {
	return (time.Duration(num(m["duration_ms"])) * time.Millisecond).Round(time.Second).String()
}

func num(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

func orEmpty(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func joinAny(v any) string {
	switch l := v.(type) {
	case []string:
		return strings.Join(l, ", ")
	case []any:
		parts := make([]string, len(l))
		for i, x := range l {
			parts[i] = fmt.Sprint(x)
		}
		return strings.Join(parts, ", ")
	}
	return orEmpty(v)
}

// progressLog is an event log that also prints progress lines.
type progressLog struct {
	core.EventLog
	line func(typ string, m map[string]any) string
	p    *Progress
}

func (l progressLog) note(typ string, data any) {
	if m, ok := data.(map[string]any); ok {
		l.p.print(l.line(typ, m))
	}
}

func (l progressLog) Append(typ string, data any) error {
	l.note(typ, data)
	return l.EventLog.Append(typ, data)
}

func (l progressLog) AppendSync(typ string, data any) error {
	l.note(typ, data)
	return l.EventLog.AppendSync(typ, data)
}

func (l progressLog) AppendID(typ string, data any) (string, error) {
	l.note(typ, data)
	return l.EventLog.AppendID(typ, data)
}

// farmLog prints farm events as progress lines; a nil Progress is a no-op.
func (p *Progress) farmLog(log core.EventLog) core.EventLog {
	if p == nil {
		return log
	}
	return progressLog{EventLog: log, line: farmLine, p: p}
}

// workerLog prints a worker's tool calls as progress lines.
func (p *Progress) workerLog(part string, log core.EventLog) core.EventLog {
	if p == nil {
		return log
	}
	return progressLog{EventLog: log, p: p, line: func(typ string, m map[string]any) string {
		if typ != core.EvToolStarted {
			return ""
		}
		return toolLine(part, m)
	}}
}
