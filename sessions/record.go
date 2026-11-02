// Package sessions keeps one small status record per running jas session in
// the repo's .jas/sessions/ directory, so sessions in the same repo (chats,
// jas run, farm workers) can see each other. Records are advisory.
package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Record timing and size limits (session-awareness spec).
const (
	HeartbeatEvery = 15 * time.Second
	staleAfter     = 60 * time.Second // no heartbeat and the pid is gone
	staleAlways    = 10 * time.Minute // no heartbeat, whatever the pid says
	endedKeep      = 60 * time.Second // ended records stay for late readers
	maxTouched     = 200
	recordVersion  = 1
)

// Record states.
const (
	StateActive = "active"
	StateIdle   = "idle"
	StateEnded  = "ended"
)

// Farm marks a farm worker's record.
type Farm struct {
	ID           string   `json:"id"`
	Part         string   `json:"part"`
	Owns         []string `json:"owns"`
	TargetBranch string   `json:"target_branch"`
}

// Record is one session's status file.
type Record struct {
	V                int       `json:"v"`
	SessionID        string    `json:"session_id"`
	PID              int       `json:"pid"`
	Host             string    `json:"host"`
	Mode             string    `json:"mode"` // chat | run | farm-worker
	Title            string    `json:"title"`
	Branch           string    `json:"branch"`
	Worktree         string    `json:"worktree"`
	Farm             *Farm     `json:"farm,omitempty"`
	Touched          []string  `json:"touched"`
	TouchedTruncated bool      `json:"touched_truncated,omitempty"`
	ShellUsed        bool      `json:"shell_used,omitempty"`
	Started          time.Time `json:"started"`
	Heartbeat        time.Time `json:"heartbeat"`
	State            string    `json:"state"`
	Log              string    `json:"log"`
}

// Handle owns one session's record file.
type Handle struct {
	path string

	mu  sync.Mutex
	rec Record
	err error // last write error
}

// Register writes r as <dir>/<session id>.json, filling in version, pid,
// host, start and heartbeat times and the active state.
func Register(dir string, r Record) (*Handle, error) {
	if r.SessionID == "" || strings.ContainsAny(r.SessionID, `/\`) {
		return nil, fmt.Errorf("sessions: bad session id %q", r.SessionID)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	now := time.Now()
	r.V, r.PID, r.State = recordVersion, os.Getpid(), StateActive
	r.Host, _ = os.Hostname()
	r.Started, r.Heartbeat = now, now
	if r.Touched == nil {
		r.Touched = []string{}
	}
	h := &Handle{path: filepath.Join(dir, r.SessionID+".json"), rec: r}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.write(); err != nil {
		return nil, err
	}
	return h, nil
}

// Path is the record file.
func (h *Handle) Path() string { return h.path }

// write saves the record atomically; the caller holds mu.
func (h *Handle) write() error {
	b, err := json.MarshalIndent(h.rec, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(h.path), ".record-*")
	if err != nil {
		h.err = err
		return err
	}
	_, werr := tmp.Write(append(b, '\n'))
	cerr := tmp.Close()
	if err = errors.Join(werr, cerr); err == nil {
		err = os.Rename(tmp.Name(), h.path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	h.err = err
	return err
}

// update applies f to the record and saves it.
func (h *Handle) update(f func(r *Record)) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	f(&h.rec)
	_ = h.write()
}

// Touch records a repo-relative path this session wrote.
func (h *Handle) Touch(rel string) {
	h.update(func(r *Record) {
		switch {
		case slices.Contains(r.Touched, rel):
		case len(r.Touched) >= maxTouched:
			r.TouchedTruncated = true
		default:
			r.Touched = append(r.Touched, rel)
		}
	})
}

// ShellUsed notes that the session ran shell, whose writes are not tracked.
func (h *Handle) ShellUsed() {
	h.update(func(r *Record) { r.ShellUsed = true })
}

// SetState sets active or idle.
func (h *Handle) SetState(state string) {
	h.update(func(r *Record) { r.State = state })
}

// Flush returns the last write error, if any.
func (h *Handle) Flush() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

// Start refreshes the heartbeat every HeartbeatEvery until ctx ends.
func (h *Handle) Start(ctx context.Context) { h.StartEvery(ctx, HeartbeatEvery) }

// StartEvery is Start with a custom interval.
func (h *Handle) StartEvery(ctx context.Context, every time.Duration) {
	if h == nil {
		return
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				h.update(func(r *Record) { r.Heartbeat = now })
			}
		}
	}()
}

// Close marks the record ended; Prune deletes it after a minute.
func (h *Handle) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rec.State, h.rec.Heartbeat = StateEnded, time.Now()
	return h.write()
}

// Stale reports whether r should no longer be treated as a peer. alive
// reports whether a pid on this host is running.
func Stale(r Record, now time.Time, host string, alive func(pid int) bool) bool {
	age := now.Sub(r.Heartbeat)
	if r.State == StateEnded {
		return age > endedKeep
	}
	if age <= staleAfter {
		return false
	}
	return age > staleAlways || (r.Host == host && !alive(r.PID))
}

// read loads every record in dir, skipping unreadable files.
func read(dir string) ([]Record, []string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var recs []Record
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var r Record
		if json.Unmarshal(b, &r) != nil {
			continue
		}
		recs, paths = append(recs, r), append(paths, p)
	}
	return recs, paths, nil
}

// List returns the records in dir that are not stale.
func List(dir string, now time.Time) ([]Record, error) {
	recs, _, err := read(dir)
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	var out []Record
	for _, r := range recs {
		if !Stale(r, now, host, Alive) {
			out = append(out, r)
		}
	}
	return out, nil
}

// Prune deletes stale records and returns how many it removed.
func Prune(dir string, now time.Time) (int, error) {
	recs, paths, err := read(dir)
	if err != nil {
		return 0, err
	}
	host, _ := os.Hostname()
	n := 0
	for i, r := range recs {
		if Stale(r, now, host, Alive) {
			if err := os.Remove(paths[i]); err == nil {
				n++
			}
		}
	}
	return n, nil
}
