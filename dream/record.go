// Package dream holds the records and store snapshots behind `jas dream`.
package dream

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// State is where a dream stands in its life cycle.
type State string

const (
	Pending   State = "pending"
	Applied   State = "applied"
	Discarded State = "discarded"
	Stale     State = "stale"
	Undone    State = "undone"
)

const (
	dreamFile         = "dream.json"
	stateFile         = "state.json"
	idTimeLayout      = "20060102T150405" // same as core.NewSessionID
	watermarkFallback = 30 * 24 * time.Hour
)

// Counts summarizes a proposal.
type Counts struct {
	Changed   int `json:"changed"`
	Added     int `json:"added"`
	Removed   int `json:"removed"`
	LintFixed int `json:"lint_fixed"`
	LintOpen  int `json:"lint_open"`
	Promote   int `json:"promote"`
}

// Window is the span of sessions a dream looked at.
type Window struct {
	Since    time.Time `json:"since"`
	Until    time.Time `json:"until"`
	Sessions []string  `json:"sessions"`
}

// Meta is the contents of <id>/dream.json.
type Meta struct {
	ID        string            `json:"id"`
	Profile   string            `json:"profile"`
	State     State             `json:"state"`
	StartedAt time.Time         `json:"started_at"`
	EndedAt   time.Time         `json:"ended_at"`
	SessionID string            `json:"session_id"`
	Window    Window            `json:"window"`
	Base      map[string]string `json:"base"`
	Proposed  map[string]string `json:"proposed"`
	Counts    Counts            `json:"counts"`
	Partial   bool              `json:"partial"`
}

type watermarkFile struct {
	Watermark time.Time `json:"watermark"`
}

// NewID returns "<UTC timestamp>-<4 hex>", e.g. 20061029T150405-a1b2.
func NewID(now time.Time) string {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return now.UTC().Format(idTimeLayout) + "-" + hex.EncodeToString(b[:])
}

// Dir returns the dreams directory of a profile.
func Dir(profileDir string) string { return filepath.Join(profileDir, "dreams") }

// Save writes <dreamsDir>/<id>/dream.json atomically.
func Save(dreamsDir string, m Meta) error {
	if m.ID == "" {
		return errors.New("dream: save: empty id")
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("dream: encode %s: %w", m.ID, err)
	}
	if err := atomicWrite(filepath.Join(dreamsDir, m.ID, dreamFile), append(data, '\n')); err != nil {
		return fmt.Errorf("dream: save %s: %w", m.ID, err)
	}
	return nil
}

// Load reads <dreamsDir>/<id>/dream.json.
func Load(dreamsDir, id string) (Meta, error) {
	var m Meta
	data, err := os.ReadFile(filepath.Join(dreamsDir, id, dreamFile))
	if err != nil {
		return m, fmt.Errorf("dream: load %s: %w", id, err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return Meta{}, fmt.Errorf("dream: parse %s: %w", id, err)
	}
	return m, nil
}

// List returns every readable dream, newest first. Directories without a
// readable dream.json are skipped; a missing dreamsDir yields none.
func List(dreamsDir string) ([]Meta, error) {
	entries, err := os.ReadDir(dreamsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dream: list: %w", err)
	}
	var ms []Meta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := Load(dreamsDir, e.Name())
		if err != nil {
			continue
		}
		ms = append(ms, m)
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].ID > ms[j].ID })
	return ms, nil
}

// Newest returns the newest dream in state s.
func Newest(ms []Meta, s State) (Meta, bool) {
	var best Meta
	found := false
	for _, m := range ms {
		if m.State == s && (!found || m.ID > best.ID) {
			best, found = m, true
		}
	}
	return best, found
}

// Watermark returns the end of the last applied or discarded dream, or
// now-30d when none is recorded.
func Watermark(dreamsDir string, now time.Time) time.Time {
	data, err := os.ReadFile(filepath.Join(dreamsDir, stateFile))
	if err == nil {
		var w watermarkFile
		if json.Unmarshal(data, &w) == nil && !w.Watermark.IsZero() {
			return w.Watermark
		}
	}
	return now.Add(-watermarkFallback)
}

// SetWatermark records t as the watermark.
func SetWatermark(dreamsDir string, t time.Time) error {
	data, err := json.MarshalIndent(watermarkFile{Watermark: t.UTC()}, "", "  ")
	if err != nil {
		return fmt.Errorf("dream: encode watermark: %w", err)
	}
	if err := atomicWrite(filepath.Join(dreamsDir, stateFile), append(data, '\n')); err != nil {
		return fmt.Errorf("dream: set watermark: %w", err)
	}
	return nil
}

// Prune deletes the oldest non-pending dreams beyond keep. Pending dreams
// are never deleted and do not count against keep.
func Prune(dreamsDir string, keep int) error {
	ms, err := List(dreamsDir)
	if err != nil {
		return err
	}
	kept := 0
	for _, m := range ms {
		if m.State == Pending {
			continue
		}
		if kept++; kept <= keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dreamsDir, m.ID)); err != nil {
			return fmt.Errorf("dream: prune %s: %w", m.ID, err)
		}
	}
	return nil
}

// atomicWrite writes data to path via a temp file and rename, creating
// parent directories.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".jas-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}
