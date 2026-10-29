package core

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event type names (design §3 "Event types (v1 contract)").
const (
	EvSessionStarted   = "session_started"
	EvAssistantMessage = "assistant_message"
	EvUsage            = "usage"
	EvToolStarted      = "tool_started"
	EvApproval         = "approval"
	EvToolResult       = "tool_result"
	EvElided           = "elided"
	EvSessionEnded     = "session_ended"
)

const (
	eventVersion = 1
	logFileMode  = 0o600
	logDirMode   = 0o700
)

// ErrLogClosed is returned by appends after Close.
var ErrLogClosed = errors.New("event log closed")

// ErrSessionLocked is returned when another process already holds the log.
var ErrSessionLocked = errors.New("session is open in another jas process")

// Event is one line of the JSONL log. Data's fields are flattened next to the
// header fields when marshaled.
type Event struct {
	V        int
	ID       string
	ParentID *string
	Seq      int64
	TS       time.Time
	Type     string
	Data     any
}

// MarshalJSON emits the header followed by the payload's fields. Header
// fields win over payload fields of the same name.
func (e Event) MarshalJSON() ([]byte, error) {
	payload := map[string]json.RawMessage{}
	if e.Data != nil {
		raw, err := json.Marshal(e.Data)
		if err != nil {
			return nil, err
		}
		if string(raw) != "null" {
			if err := json.Unmarshal(raw, &payload); err != nil {
				return nil, fmt.Errorf("event payload must be a JSON object: %w", err)
			}
		}
	}
	header := []struct {
		key string
		val any
	}{
		{"v", e.V}, {"id", e.ID}, {"parent_id", e.ParentID},
		{"seq", e.Seq}, {"ts", e.TS.Format(time.RFC3339Nano)}, {"type", e.Type},
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, h := range header {
		if i > 0 {
			buf.WriteByte(',')
		}
		v, err := json.Marshal(h.val)
		if err != nil {
			return nil, err
		}
		k, _ := json.Marshal(h.key)
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(v)
		delete(payload, h.key)
	}
	// Deterministic payload order via a sorted map encoding.
	if len(payload) > 0 {
		rest, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		buf.WriteByte(',')
		buf.Write(rest[1:]) // drop the leading '{'
		return buf.Bytes(), nil
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// EventLog is the single-writer session log. Safe for concurrent use.
type EventLog interface {
	// Append writes an event and returns once it is written (not fsynced).
	Append(typ string, data any) error
	// AppendSync writes an event and fsyncs before returning.
	AppendSync(typ string, data any) error
	// Sync waits for every earlier append, then fsyncs.
	Sync() error
	Close() error
}

type logReq struct {
	typ  string
	data any
	sync bool
	noop bool // Sync: write nothing, just fsync
	done chan error
}

type fileLog struct {
	f      *os.File
	tee    io.Writer
	syncFn func(*os.File) error

	mu     sync.RWMutex // guards closed and sends on ch
	closed bool
	ch     chan logReq
	wg     sync.WaitGroup
	seq    int64
	lastID string // id of the last event written; the next event's parent
}

// OpenEventLog opens (creating if needed) the JSONL log at path. tee, if
// non-nil, receives the same bytes as the file. redact, if non-nil, runs over
// every string value in each payload.
func OpenEventLog(path string, tee io.Writer, redact func(string) string) (EventLog, error) {
	return openEventLog(path, tee, redact, func(f *os.File) error { return f.Sync() })
}

func openEventLog(path string, tee io.Writer, redact func(string) string, syncFn func(*os.File) error) (EventLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), logDirMode); err != nil {
		return nil, fmt.Errorf("create event log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, logFileMode)
	if err != nil {
		return nil, fmt.Errorf("open event log: %w", err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		if errors.Is(err, ErrSessionLocked) {
			return nil, err
		}
		return nil, fmt.Errorf("lock event log: %w", err)
	}
	seq, lastID, err := resumeState(path, f)
	if err != nil {
		f.Close()
		return nil, err
	}
	l := &fileLog{seq: seq, lastID: lastID, f: f, tee: tee, syncFn: syncFn, ch: make(chan logReq)}
	l.wg.Add(1)
	go l.run()
	if redact == nil {
		return l, nil
	}
	return &redactingLog{fileLog: l, redact: redact}, nil
}

// resumeState scans an existing log for the last complete event line and
// returns its seq and id. A trailing partial line (crash mid-write) is ignored
// and a newline is appended so the next event starts on its own line.
func resumeState(path string, f *os.File) (int64, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, "", fmt.Errorf("read event log: %w", err)
	}
	if len(data) == 0 {
		return 0, "", nil
	}
	var seq int64
	var id string
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		var h struct {
			ID  string `json:"id"`
			Seq int64  `json:"seq"`
		}
		if json.Unmarshal(line, &h) == nil && h.ID != "" && h.Seq > 0 {
			seq, id = h.Seq, h.ID
		}
	}
	if data[len(data)-1] != '\n' {
		if _, err := f.Write([]byte{'\n'}); err != nil {
			return 0, "", fmt.Errorf("repair event log: %w", err)
		}
	}
	return seq, id, nil
}

func (l *fileLog) run() {
	defer l.wg.Done()
	for r := range l.ch {
		r.done <- l.handle(r)
	}
}

func (l *fileLog) handle(r logReq) error {
	if !r.noop {
		l.seq++
		ev := Event{V: eventVersion, ID: fmt.Sprintf("e%06d", l.seq), Seq: l.seq,
			TS: time.Now().UTC(), Type: r.typ, Data: r.data}
		if l.lastID != "" {
			parent := l.lastID
			ev.ParentID = &parent
		}
		line, err := json.Marshal(ev)
		if err != nil {
			l.seq--
			return fmt.Errorf("encode event: %w", err)
		}
		line = append(line, '\n')
		if _, err := l.f.Write(line); err != nil {
			return fmt.Errorf("write event log: %w", err)
		}
		l.lastID = ev.ID
		if l.tee != nil {
			if _, err := l.tee.Write(line); err != nil {
				return fmt.Errorf("write event tee: %w", err)
			}
		}
	}
	if r.sync {
		if err := l.syncFn(l.f); err != nil {
			return fmt.Errorf("fsync event log: %w", err)
		}
	}
	return nil
}

func (l *fileLog) submit(r logReq) error {
	r.done = make(chan error, 1)
	l.mu.RLock()
	if l.closed {
		l.mu.RUnlock()
		return ErrLogClosed
	}
	l.ch <- r
	l.mu.RUnlock()
	return <-r.done
}

func (l *fileLog) Append(typ string, data any) error {
	return l.submit(logReq{typ: typ, data: data})
}

func (l *fileLog) AppendSync(typ string, data any) error {
	return l.submit(logReq{typ: typ, data: data, sync: true})
}

func (l *fileLog) Sync() error { return l.submit(logReq{noop: true, sync: true}) }

func (l *fileLog) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	close(l.ch)
	l.mu.Unlock()
	l.wg.Wait()
	serr := l.syncFn(l.f)
	cerr := l.f.Close()
	return errors.Join(serr, cerr)
}

// redactingLog applies redact to every string in each payload before append.
type redactingLog struct {
	*fileLog
	redact func(string) string
}

func (r *redactingLog) Append(typ string, data any) error {
	d, err := redactPayload(data, r.redact)
	if err != nil {
		return err
	}
	return r.fileLog.Append(typ, d)
}

func (r *redactingLog) AppendSync(typ string, data any) error {
	d, err := redactPayload(data, r.redact)
	if err != nil {
		return err
	}
	return r.fileLog.AppendSync(typ, d)
}

func redactPayload(data any, redact func(string) string) (any, error) {
	if data == nil {
		return nil, nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode event payload: %w", err)
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // keep large integers exact
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("decode event payload: %w", err)
	}
	return walkRedact(v, redact), nil
}

func walkRedact(v any, redact func(string) string) any {
	switch x := v.(type) {
	case string:
		return redact(x)
	case []any:
		for i := range x {
			x[i] = walkRedact(x[i], redact)
		}
	case map[string]any:
		for k, e := range x {
			x[k] = walkRedact(e, redact)
		}
	}
	return v
}

// NewSessionID returns "<UTC timestamp>-<6 hex>", e.g. 20061029T150405-a1b2c3.
func NewSessionID(now time.Time) string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return now.UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}
