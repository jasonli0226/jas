package dream

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jasonli0226/jas/core"
)

const (
	eventsFile     = "events.jsonl"
	defaultProfile = "default"
	maxEventsV     = 1 // newest event schema version this reader understands
)

// LogRef names one session event log a dream may read.
type LogRef struct {
	SessionID, Path, Root string
	Started, Modified     time.Time
}

// Seqs maps a session id to the event seqs its log holds.
type Seqs map[string]map[int64]bool

// logLine is the header of one event line plus the session_started fields
// window selection needs.
type logLine struct {
	V       int             `json:"v"`
	Seq     int64           `json:"seq"`
	TS      time.Time       `json:"ts"`
	Type    string          `json:"type"`
	Root    string          `json:"root"`
	Profile string          `json:"profile"`
	Dream   json.RawMessage `json:"dream"`
}

// parseLine decodes one raw line; reason is non-empty when it must be skipped.
func parseLine(raw []byte) (ev logLine, reason string) {
	if raw == nil {
		return ev, "oversized"
	}
	if json.Unmarshal(raw, &ev) != nil || ev.Type == "" {
		return ev, "unreadable"
	}
	if ev.V > maxEventsV {
		return ev, fmt.Sprintf("version %d", ev.V)
	}
	return ev, ""
}

// scanLog reads a log with Replay's tolerance. fn sees each usable line;
// skipped lines come back as notices naming the session and line number.
func scanLog(id, path string, fn func(logLine)) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var notices []string
	err = core.ScanEvents(f, func(n int, raw []byte) {
		ev, reason := parseLine(raw)
		if reason != "" {
			notices = append(notices, fmt.Sprintf("session %s line %d skipped (%s)", id, n, reason))
			return
		}
		fn(ev)
	})
	return notices, err
}

// SelectWindow returns the logs under sessionsDir that a dream for profile
// reads: same profile (a missing one counts as "default"), modified after
// since, and not themselves dream sessions. Newest Modified first, at most
// limit (limit <= 0 means no cap). Skipped lines and files become notices.
func SelectWindow(sessionsDir, profile string, since time.Time, limit int) ([]LogRef, []string, error) {
	entries, err := os.ReadDir(sessionsDir)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("list sessions: %w", err)
	}
	var cands []LogRef
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(sessionsDir, e.Name(), eventsFile)
		fi, err := os.Stat(p)
		if err != nil || !fi.ModTime().After(since) {
			continue
		}
		cands = append(cands, LogRef{SessionID: e.Name(), Path: p, Modified: fi.ModTime()})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Modified.After(cands[j].Modified) })

	var refs []LogRef
	var notices []string
	for _, c := range cands {
		if limit > 0 && len(refs) >= limit {
			break
		}
		ref, ns, ok := inspect(c, profile)
		notices = append(notices, ns...)
		if ok {
			refs = append(refs, ref)
		}
	}
	return refs, notices, nil
}

// inspect reads one candidate log's session_started and decides whether it
// belongs to the window. ok is false for logs left out; notices are returned
// for a log that is skipped as unreadable or kept, not for one filtered out.
func inspect(c LogRef, profile string) (ref LogRef, notices []string, ok bool) {
	var start *logLine
	notices, err := scanLog(c.SessionID, c.Path, func(ev logLine) {
		if start == nil && ev.Type == core.EvSessionStarted {
			s := ev
			start = &s
		}
	})
	if err != nil {
		return c, []string{fmt.Sprintf("session %s skipped (%v)", c.SessionID, err)}, false
	}
	if start == nil {
		return c, append(notices, fmt.Sprintf("session %s skipped (no readable session_started)", c.SessionID)), false
	}
	c.Started, c.Root = start.TS, start.Root
	p := start.Profile
	if p == "" {
		p = defaultProfile
	}
	if p != profile || len(start.Dream) > 0 && string(start.Dream) != "null" {
		return c, nil, false
	}
	return c, notices, true
}

// IndexSeqs records every seq each log holds, for checking citations.
func IndexSeqs(logs []LogRef) (Seqs, error) {
	out := make(Seqs, len(logs))
	for _, l := range logs {
		set := map[int64]bool{}
		if _, err := scanLog(l.SessionID, l.Path, func(ev logLine) { set[ev.Seq] = true }); err != nil {
			return nil, fmt.Errorf("index %s: %w", l.SessionID, err)
		}
		out[l.SessionID] = set
	}
	return out, nil
}
