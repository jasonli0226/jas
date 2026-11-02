package farm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jasonli0226/jas/core"
)

// Part statuses.
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
	StatusPaused  = "paused"
	StatusDropped = "dropped"
)

// PartState is one part's record in the manifest.
type PartState struct {
	Role          string `json:"role"`
	Branch        string `json:"branch"`
	Worktree      string `json:"worktree"`
	SessionID     string `json:"session_id"`
	Log           string `json:"log"`
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason"`
	ExitCode      int    `json:"exit_code"`
	Tokens        int    `json:"tokens"`
	Diffstat      string `json:"diffstat"`
	Undelivered   []int  `json:"undelivered"`
}

// Manifest is the farm's on-disk summary (manifest.json).
type Manifest struct {
	FarmID      string                `json:"farm_id"`
	PlanSHA256  string                `json:"plan_sha256"`
	Base        string                `json:"base"`
	Parts       map[string]*PartState `json:"parts"`
	Integrator  *PartState            `json:"integrator"`
	Check       *core.CheckResult     `json:"-"`
	FinalChoice string                `json:"final_choice"`
	Reason      string                `json:"reason"`
	ExitCode    int                   `json:"exit_code"`
	TotalTokens int                   `json:"total_tokens"`
}

// manifestMu serializes Update and Save. It is package-level so Manifest stays
// copyable (LoadManifest returns it by value); contention is negligible.
var manifestMu sync.Mutex

type checkJSON struct {
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Output     string `json:"output"`
	TimedOut   bool   `json:"timed_out"`
}

type manifestJSON struct {
	*manifestAlias
	Check *checkJSON `json:"check"`
}

type manifestAlias Manifest

// NewFarmID returns an id shaped like core.NewSessionID.
func NewFarmID(now time.Time) string { return core.NewSessionID(now) }

// Update runs fn while holding the manifest lock.
func (m *Manifest) Update(fn func(*Manifest)) {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	fn(m)
}

// Save writes the manifest atomically (temp file, fsync, rename).
func (m *Manifest) Save(path string) error {
	manifestMu.Lock()
	defer manifestMu.Unlock()
	view := manifestJSON{manifestAlias: (*manifestAlias)(m)}
	if m.Check != nil {
		view.Check = &checkJSON{m.Check.ExitCode, m.Check.Duration.Milliseconds(), m.Check.Output, m.Check.TimedOut}
	}
	data, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create manifest dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp manifest: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close manifest: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename manifest: %w", err)
	}
	return nil
}

// LoadManifest reads a manifest written by Save.
func LoadManifest(path string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(path)
	if err != nil {
		return m, fmt.Errorf("read manifest: %w", err)
	}
	view := manifestJSON{manifestAlias: (*manifestAlias)(&m)}
	if err := json.Unmarshal(data, &view); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	if view.Check != nil {
		m.Check = &core.CheckResult{ExitCode: view.Check.ExitCode,
			Duration: time.Duration(view.Check.DurationMS) * time.Millisecond,
			Output:   view.Check.Output, TimedOut: view.Check.TimedOut}
	}
	return m, nil
}
