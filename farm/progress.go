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
	// Width is the terminal's column count, or 0 when unknown; lines are
	// clipped to fit so they never wrap (a wrapped line can be overwritten
	// when the terminal reflows). nil: never clip.
	Width func() int

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
	line = core.Sanitize(line)
	if p.Width != nil {
		line = fitWidth(line, p.Width())
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintln(p.w, line)
}

// fitWidth clips line to width-1 runes, ending in "…", so it fits one row
// with the cursor's column to spare. width <= 1 means unknown: no clip.
func fitWidth(line string, width int) string {
	r := []rune(line)
	if width <= 1 || len(r) < width {
		return line
	}
	return string(r[:width-2]) + "…"
}

// ProgressLine is the progress line jas farm run prints for a farm.jsonl
// event, or "" for events it does not show.
func ProgressLine(typ string, m map[string]any) string { return farmLine(typ, m) }

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
	case EvGeneratedRegenerated:
		if num(m["exit_code"]) != 0 || m["timed_out"] == true {
			return fmt.Sprintf("[%s] regenerate %v failed (%s)", integratePart, m["path"], exitText(m))
		}
		if m["changed"] == true {
			return fmt.Sprintf("[%s] regenerated %v", integratePart, m["path"])
		}
		return ""
	case EvGeneratedRestored:
		return fmt.Sprintf("[%s] undid generated %s", part, joinAny(m["paths"]))
	case EvPartCheckDeferred:
		return fmt.Sprintf("[%s] part_check deferred to the merged check: %v (set part_check: \"\" if parts cannot pass check alone)", part, m["reason"])
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
		line := fmt.Sprintf("decision: %v", m["choice"])
		if p, ok := m["part"]; ok {
			line = fmt.Sprintf("[%v] %s", p, line)
		}
		if note, ok := m["note"].(string); ok && note != "" {
			line += " — " + note
		}
		return line
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

// Watcher receives farm.jsonl events and each worker's log events as they
// are written. Progress is the CLI's watcher; the chat supplies its own. Calls
// come from farm goroutines and must not block for long.
type Watcher interface {
	FarmEvent(typ string, m map[string]any)
	WorkerEvent(part, typ string, m map[string]any)
	// SteerUndelivered hands back owner text queued for a part that finished
	// before reading it.
	SteerUndelivered(part string, texts []string)
}

// FarmEvent prints a farm event as a progress line.
func (p *Progress) FarmEvent(typ string, m map[string]any) { p.print(farmLine(typ, m)) }

// WorkerEvent prints a worker's tool call as a progress line.
func (p *Progress) WorkerEvent(part, typ string, m map[string]any) {
	if typ == core.EvToolStarted {
		p.print(toolLine(part, m))
	}
}

// SteerUndelivered is a no-op: the CLI has no steering.
func (p *Progress) SteerUndelivered(string, []string) {}

// watchLog is an event log that also reports each event to a watcher.
type watchLog struct {
	core.EventLog
	note func(typ string, m map[string]any)
}

func (l watchLog) report(typ string, data any) {
	if m, ok := data.(map[string]any); ok {
		l.note(typ, m)
	}
}

func (l watchLog) Append(typ string, data any) error {
	l.report(typ, data)
	return l.EventLog.Append(typ, data)
}

func (l watchLog) AppendSync(typ string, data any) error {
	l.report(typ, data)
	return l.EventLog.AppendSync(typ, data)
}

func (l watchLog) AppendID(typ string, data any) (string, error) {
	l.report(typ, data)
	return l.EventLog.AppendID(typ, data)
}

// watchFarmLog reports farm events to w; a nil w is a no-op.
func watchFarmLog(w Watcher, log core.EventLog) core.EventLog {
	if w == nil {
		return log
	}
	return watchLog{EventLog: log, note: w.FarmEvent}
}

// watchWorkerLog reports a worker's events to w; a nil w is a no-op.
func watchWorkerLog(w Watcher, part string, log core.EventLog) core.EventLog {
	if w == nil {
		return log
	}
	return watchLog{EventLog: log, note: func(typ string, m map[string]any) { w.WorkerEvent(part, typ, m) }}
}
