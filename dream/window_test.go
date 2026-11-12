package dream

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonli0226/jas/internal/assert"
)

func started(profile, extra string) string {
	p := ""
	if profile != "" {
		p = `,"profile":"` + profile + `"`
	}
	return `{"v":1,"id":"a","seq":1,"ts":"2026-11-01T10:00:00Z","type":"session_started","root":"/w"` + p + extra + `}`
}

// writeLog writes sessions/<id>/events.jsonl with the given mtime.
func writeLog(t *testing.T, dir, id string, mtime time.Time, lines ...string) {
	t.Helper()
	d := filepath.Join(dir, id)
	assert.NoErr(t, os.MkdirAll(d, 0o755))
	p := filepath.Join(d, "events.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	assert.NoErr(t, os.WriteFile(p, []byte(body), 0o644))
	assert.NoErr(t, os.Chtimes(p, mtime, mtime))
}

func ids(refs []LogRef) []string {
	var out []string
	for _, r := range refs {
		out = append(out, r.SessionID)
	}
	return out
}

func TestSelectWindow_FiltersProfileAndSince(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeLog(t, dir, "s-default", now.Add(-1*time.Hour), started("default", ""))
	writeLog(t, dir, "s-none", now.Add(-2*time.Hour), started("", ""))
	writeLog(t, dir, "s-work", now.Add(-1*time.Hour), started("work", ""))
	writeLog(t, dir, "s-old", now.Add(-48*time.Hour), started("default", ""))
	refs, _, err := SelectWindow(dir, "default", now.Add(-24*time.Hour), 50)
	assert.NoErr(t, err)
	got := ids(refs)
	assert.Eq(t, len(got), 2)
	assert.Eq(t, got[0], "s-default")
	assert.Eq(t, got[1], "s-none")
	assert.Eq(t, refs[0].Root, "/w")
	assert.Eq(t, refs[0].Started.Format(time.RFC3339), "2026-11-01T10:00:00Z")
	assert.Eq(t, refs[0].Path, filepath.Join(dir, "s-default", "events.jsonl"))
}

func TestSelectWindow_SkipsDreamSessions(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeLog(t, dir, "normal", now, started("default", ""))
	writeLog(t, dir, "dreamy", now, started("default", `,"dream":{"id":"d1","window_sessions":3}`))
	refs, _, err := SelectWindow(dir, "default", now.Add(-time.Hour), 50)
	assert.NoErr(t, err)
	assert.Eq(t, len(refs), 1)
	assert.Eq(t, refs[0].SessionID, "normal")
}

func TestSelectWindow_Limit(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		writeLog(t, dir, id, now.Add(-time.Duration(i)*time.Minute), started("default", ""))
	}
	refs, _, err := SelectWindow(dir, "default", now.Add(-time.Hour), 3)
	assert.NoErr(t, err)
	got := ids(refs)
	assert.Eq(t, len(got), 3)
	assert.Eq(t, got[0], "a")
	assert.Eq(t, got[2], "c")
}

func TestSelectWindow_SkipsTornLines(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeLog(t, dir, "torn", now, started("default", ""),
		`{"v":1,"id":"b","seq":2,"type":"user_mess`,
		`{"v":2,"id":"c","seq":3,"ts":"2026-11-01T10:00:00Z","type":"future"}`,
		`{"v":1,"id":"d","seq":4,"ts":"2026-11-01T10:00:00Z","type":"user_message"}`)
	refs, notices, err := SelectWindow(dir, "default", now.Add(-time.Hour), 50)
	assert.NoErr(t, err)
	assert.Eq(t, len(refs), 1)
	assert.Eq(t, len(notices), 2)
	assert.Contains(t, notices[0], "torn")
	assert.Contains(t, notices[0], "line 2")
	assert.Contains(t, notices[1], "line 3")
}

func TestSelectWindow_SkipsLogWithoutStart(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeLog(t, dir, "nostart", now, `{"v":1,"id":"b","seq":1,"ts":"2026-11-01T10:00:00Z","type":"user_message"}`)
	refs, notices, err := SelectWindow(dir, "default", now.Add(-time.Hour), 50)
	assert.NoErr(t, err)
	assert.Eq(t, len(refs), 0)
	assert.Eq(t, len(notices), 1)
	assert.Contains(t, notices[0], "nostart")
}

func TestSelectWindow_MissingDir(t *testing.T) {
	refs, _, err := SelectWindow(filepath.Join(t.TempDir(), "none"), "default", time.Time{}, 50)
	assert.NoErr(t, err)
	assert.Eq(t, len(refs), 0)
}

func TestIndexSeqs(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeLog(t, dir, "s1", now, started("default", ""),
		`{"v":1,"id":"b","seq":2,"ts":"2026-11-01T10:00:00Z","type":"user_message"}`,
		`{"v":1,"id":"c`,
		`{"v":1,"id":"d","seq":4,"ts":"2026-11-01T10:00:00Z","type":"user_message"}`)
	refs, _, err := SelectWindow(dir, "default", now.Add(-time.Hour), 50)
	assert.NoErr(t, err)
	seqs, err := IndexSeqs(refs)
	assert.NoErr(t, err)
	assert.Eq(t, seqs["s1"][1], true)
	assert.Eq(t, seqs["s1"][2], true)
	assert.Eq(t, seqs["s1"][3], false)
	assert.Eq(t, seqs["s1"][4], true)
}
