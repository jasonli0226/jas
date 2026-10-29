package core

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jasonli0226/jas/providers"
)

const (
	eventsFileName = "events.jsonl"
	// maxLogLine bounds one event line; tool outputs can be large, and a line
	// over the limit must not be mistaken for a broken one.
	maxLogLine = 16 << 20
	// interruptedOutput stands in for a tool call that never logged a result.
	interruptedOutput = "interrupted: jas stopped while this call was running; it may have partly run. Inspect the current state before retrying."
	firstMsgRunes     = 80
)

// ReplayState is a session file turned back into a conversation.
type ReplayState struct {
	Messages []providers.Message
	Model    string   // last session_started/session_resumed/model_changed model
	Root     string   // session_started.root
	Notices  []string // e.g. "ignored unreadable line 41"
	// Interrupted lists tool calls with no logged result, which replay
	// answered with interruptedOutput. ResumeSession logs them as results.
	Interrupted []InterruptedCall
}

// InterruptedCall is a tool call that started but never logged a result.
type InterruptedCall struct{ ID, Tool string }

// replayLine is one event line: the header plus every payload field replay uses.
type replayLine struct {
	Type    string      `json:"type"`
	TS      time.Time   `json:"ts"`
	Model   string      `json:"model"`
	Root    string      `json:"root"`
	Content []wireBlock `json:"content"`
	CallID  string      `json:"call_id"`
	Status  string      `json:"status"`
	Output  string      `json:"output"`
}

func newLogScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLogLine)
	return sc
}

// Replay rebuilds the conversation from a session file. It fails only when the
// file can't be opened or holds no session_started; damaged lines are skipped
// with a notice, and a missing session_ended is normal (a crash).
func Replay(path string) (ReplayState, error) {
	f, err := os.Open(path)
	if err != nil {
		return ReplayState{}, fmt.Errorf("open session: %w", err)
	}
	defer f.Close()

	rp := &replayer{}
	sc := newLogScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var ev replayLine
		if json.Unmarshal(line, &ev) != nil || ev.Type == "" {
			rp.notef("ignored unreadable line %d", n)
			continue
		}
		rp.apply(n, ev)
	}
	if err := sc.Err(); err != nil {
		rp.notef("stopped reading after line %d: %v", n, err)
	}
	rp.flushResults()
	if !rp.started {
		return ReplayState{}, fmt.Errorf("session %s has no session_started event", path)
	}
	return rp.st, nil
}

// replayer folds events into messages. Tool results are held back until the
// next message event so they can be emitted as one user message in the
// assistant's tool_use order.
type replayer struct {
	st      ReplayState
	started bool
	order   []string                          // tool_use ids awaiting results, in call order
	names   map[string]string                 // tool_use id → tool name
	results map[string]providers.ContentBlock // results seen so far
}

func (r *replayer) notef(format string, a ...any) {
	r.st.Notices = append(r.st.Notices, fmt.Sprintf(format, a...))
}

func (r *replayer) apply(line int, ev replayLine) {
	switch ev.Type {
	case EvSessionStarted:
		if !r.started {
			r.st.Root = ev.Root
		}
		r.started = true
		r.setModel(ev.Model)
	case EvSessionResumed, EvModelChanged:
		r.setModel(ev.Model)
	case EvUserMessage:
		r.flushResults()
		r.add(providers.RoleUser, ev.Content)
	case EvAssistantMessage:
		r.flushResults()
		if ev.Content == nil {
			r.notef("skipped assistant message at line %d: no stored content", line)
			return
		}
		r.add(providers.RoleAssistant, ev.Content)
		r.expectResults(ev.Content)
	case EvToolResult:
		r.recordResult(ev)
	}
}

func (r *replayer) setModel(m string) {
	if m != "" {
		r.st.Model = m
	}
}

func (r *replayer) add(role providers.Role, content []wireBlock) {
	r.st.Messages = append(r.st.Messages, providers.Message{Role: role, Content: fromWire(content)})
}

func (r *replayer) expectResults(content []wireBlock) {
	r.names = map[string]string{}
	for _, b := range content {
		if b.Type == providers.BlockToolUse {
			r.order = append(r.order, b.ToolUseID)
			r.names[b.ToolUseID] = b.Name
		}
	}
	r.results = map[string]providers.ContentBlock{}
}

func (r *replayer) recordResult(ev replayLine) {
	if !slices.Contains(r.order, ev.CallID) {
		return // no matching tool_use; nothing to answer
	}
	r.results[ev.CallID] = providers.ContentBlock{
		Type: providers.BlockToolResult, ToolUseID: ev.CallID, Text: ev.Output, IsError: ev.Status != statusOK,
	}
}

// flushResults emits the tool results for the last assistant message, filling
// in "interrupted" for calls that never logged one.
func (r *replayer) flushResults() {
	if len(r.order) == 0 {
		return
	}
	blocks := make([]providers.ContentBlock, len(r.order))
	for i, id := range r.order {
		b, ok := r.results[id]
		if !ok {
			b = providers.ContentBlock{Type: providers.BlockToolResult, ToolUseID: id, Text: interruptedOutput, IsError: true}
			r.st.Interrupted = append(r.st.Interrupted, InterruptedCall{ID: id, Tool: r.names[id]})
		}
		blocks[i] = b
	}
	r.st.Messages = append(r.st.Messages, providers.Message{Role: providers.RoleUser, Content: blocks})
	r.order, r.results, r.names = nil, nil, nil
}

// SessionInfo describes one saved session for a picker.
type SessionInfo struct {
	Path         string
	ID           string    // directory name
	Started      time.Time // session_started ts
	FirstMessage string    // first user_message text, one line, ≤80 runes
}

// ListSessions returns up to limit sessions under sessionsDir whose root is
// root, newest first by file mtime (limit <= 0 means all). Sessions with no
// user message yet are left out. A missing sessionsDir is an empty list.
func ListSessions(sessionsDir, root string, limit int) ([]SessionInfo, error) {
	entries, err := os.ReadDir(sessionsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	type cand struct {
		path, id string
		mtime    time.Time
	}
	var cands []cand
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(sessionsDir, e.Name(), eventsFileName)
		if fi, err := os.Stat(p); err == nil {
			cands = append(cands, cand{p, e.Name(), fi.ModTime()})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].mtime.After(cands[j].mtime) })

	want := canonPath(root)
	var out []SessionInfo
	for _, c := range cands {
		if limit > 0 && len(out) >= limit {
			break
		}
		info, sessRoot, ok := readHead(c.path)
		if !ok || canonPath(sessRoot) != want {
			continue
		}
		info.Path, info.ID = c.path, c.id
		out = append(out, info)
	}
	return out, nil
}

// readHead reads a session file up to its first user_message. ok is false when
// the file has no session_started or no user message.
func readHead(path string) (info SessionInfo, root string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return info, "", false
	}
	defer f.Close()
	started := false
	sc := newLogScanner(f)
	for sc.Scan() {
		var ev replayLine
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case EvSessionStarted:
			started = true
			root, info.Started = ev.Root, ev.TS
		case EvUserMessage:
			if !started {
				continue
			}
			info.FirstMessage = blocksLine(ev.Content, firstMsgRunes)
			return info, root, true
		}
	}
	return info, root, false
}

// blocksLine joins the text blocks, collapses whitespace and cuts to max runes.
func blocksLine(content []wireBlock, max int) string {
	var parts []string
	for _, b := range content {
		if b.Type == providers.BlockText {
			parts = append(parts, b.Text)
		}
	}
	rs := []rune(strings.Join(strings.Fields(strings.Join(parts, " ")), " "))
	if len(rs) > max {
		rs = append(rs[:max-1], '…')
	}
	return string(rs)
}

// canonPath resolves symlinks so two spellings of one directory compare equal.
func canonPath(p string) string {
	if p == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.Clean(p)
}
