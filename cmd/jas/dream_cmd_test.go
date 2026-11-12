package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/dream"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
	"github.com/jasonli0226/jas/settings"
)

const (
	dreamPastSession = "20261101T100000-abcd"
	dreamIndex       = "- [Tabs](tabs.md) — use tabs\n"
	dreamTestKey     = "dream-test-key-0123456789"
	dreamNote        = "---\nname: tabs\ndescription: indent with tabs\ntype: feedback\n---\nUse tabs.\n"
)

// dreamFixture is a jas home with a default-profile memory store and one
// past session in the window.
type dreamFixture struct {
	home, mem, dreams, tmp string
}

// newDreamFixture sets up the home, the model server and the environment
// for an in-process dream run.
func newDreamFixture(t *testing.T, turns []fake.Turn) (dreamFixture, func() [][]byte) {
	t.Helper()
	url, bodies := testutil.FakeOpenAI(t, turns)
	return newDreamFixtureURL(t, url), bodies
}

func newDreamFixtureURL(t *testing.T, url string) dreamFixture {
	t.Helper()
	old := isTerminal
	isTerminal = func(int) bool { return false }
	t.Cleanup(func() { isTerminal = old })
	home := testutil.TempHome(t)
	tmp := t.TempDir()
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, tmp)
	}
	t.Setenv("JAS_MODEL", "m")
	t.Setenv("JAS_BASE_URL", url)
	t.Setenv("JAS_API_KEY", dreamTestKey)
	t.Setenv("JAS_MEMORY", "")
	f := dreamFixture{
		home:   home,
		mem:    filepath.Join(settings.ProfileDir(home, "default"), "memory"),
		dreams: dream.Dir(settings.ProfileDir(home, "default")),
		tmp:    tmp,
	}
	writeFile(t, filepath.Join(f.mem, "MEMORY.md"), dreamIndex)
	writeFile(t, filepath.Join(f.mem, "tabs.md"), dreamNote)
	f.pastSession(t, dreamPastSession, time.Now().Add(-time.Hour))
	return f
}

// pastSession writes an ordinary default-profile session log with mtime at.
func (f dreamFixture) pastSession(t *testing.T, id string, at time.Time) {
	t.Helper()
	p := filepath.Join(f.home, sessionsDir, id, eventsFile)
	ts := at.UTC().Format(time.RFC3339)
	writeFile(t, p, `{"v":1,"id":"e1","seq":1,"ts":"`+ts+`","type":"session_started","root":"/w","profile":"default"}`+"\n"+
		`{"v":1,"id":"e2","seq":2,"ts":"`+ts+`","type":"user_message","content":[{"type":"text","text":"use spaces, not tabs"}]}`+"\n")
	assert.NoErr(t, os.Chtimes(p, at, at))
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	assert.NoErr(t, os.MkdirAll(filepath.Dir(p), 0o755))
	assert.NoErr(t, os.WriteFile(p, []byte(content), 0o644))
}

func readText(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	assert.NoErr(t, err)
	return string(b)
}

func runDreamCmd(t *testing.T, ctx context.Context, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := dreamRun(ctx, args, &out)
	return out.String(), err
}

// onlyDream returns the one dream recorded, failing otherwise.
func (f dreamFixture) onlyDream(t *testing.T) dream.Meta {
	t.Helper()
	ms, err := dream.List(f.dreams)
	assert.NoErr(t, err)
	if len(ms) != 1 {
		t.Fatalf("want 1 dream, got %d", len(ms))
	}
	return ms[0]
}

func (f dreamFixture) sessionEvents(t *testing.T, m dream.Meta) []map[string]any {
	t.Helper()
	return testutil.ReadEvents(t, filepath.Join(f.home, sessionsDir, m.SessionID, eventsFile))
}

func eventOf(evs []map[string]any, typ string) map[string]any {
	for _, ev := range evs {
		if ev["type"] == typ {
			return ev
		}
	}
	return nil
}

func exitCode(err error) int {
	var ee *core.ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	if err != nil {
		return core.ExitFailure
	}
	return core.ExitOK
}

func call(name, args string) fake.Turn {
	return fake.Turn{Calls: []fake.Call{{Name: name, Args: args}}}
}

func TestDreamCmd_WritesProposalLeavesLive(t *testing.T) {
	f, _ := newDreamFixture(t, []fake.Turn{
		call("edit", `{"path":"proposed/MEMORY.md","old_string":"use tabs","new_string":"use tabs, not spaces"}`),
		call("report_change", `{"kind":"fix","paths":["MEMORY.md"],"reason":"clearer","evidence":["`+dreamPastSession+`#2"]}`),
		{Text: "done"},
	})
	liveBefore, err := dream.HashTree(f.mem)
	assert.NoErr(t, err)
	out, err := runDreamCmd(t, context.Background())
	assert.NoErr(t, err)

	m := f.onlyDream(t)
	idDir := filepath.Join(f.dreams, m.ID)
	for _, name := range []string{"dream.json", "report.md", "evidence.md", "proposed"} {
		if _, err := os.Stat(filepath.Join(idDir, name)); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}
	assert.Eq(t, m.State, dream.Pending)
	assert.Contains(t, readText(t, filepath.Join(idDir, "proposed", "MEMORY.md")), "not spaces")
	assert.Eq(t, readText(t, filepath.Join(f.mem, "MEMORY.md")), dreamIndex)
	liveAfter, err := dream.HashTree(f.mem)
	assert.NoErr(t, err)
	assert.Eq(t, len(dream.ChangedPaths(liveBefore, liveAfter)), 0)
	assert.Eq(t, len(dream.ChangedPaths(m.Base, liveBefore)), 0)
	persisted, err := dream.HashTree(filepath.Join(idDir, "proposed"))
	assert.NoErr(t, err)
	assert.Eq(t, len(dream.ChangedPaths(m.Proposed, persisted)), 0)
	assert.Eq(t, m.Counts.Changed, 1)
	if !slices.Contains(m.Window.Sessions, dreamPastSession) {
		t.Errorf("window %v lacks %s", m.Window.Sessions, dreamPastSession)
	}
	assert.Contains(t, out, m.ID)
	assert.Contains(t, out, filepath.Join(idDir, "report.md"))

	evs := f.sessionEvents(t, m)
	start := eventOf(evs, core.EvSessionStarted)
	d, _ := start["dream"].(map[string]any)
	if d == nil || d["id"] != m.ID || d["window_sessions"] != 1.0 {
		t.Fatalf("session_started.dream = %v", start["dream"])
	}
	prop := eventOf(evs, core.EvDreamProposed)
	if prop == nil || prop["dream_id"] != m.ID || prop["partial"] != false || prop["report"] != filepath.Join(idDir, "report.md") {
		t.Fatalf("dream_proposed = %v", prop)
	}
	if _, ok := prop["counts"].(map[string]any); !ok {
		t.Fatalf("dream_proposed has no counts: %v", prop)
	}
	if entries, _ := filepath.Glob(filepath.Join(f.tmp, "jas-dream-*")); len(entries) != 0 {
		t.Fatalf("workspace left behind: %v", entries)
	}
}

func TestDreamCmd_WriteOutsideProposedRefused(t *testing.T) {
	repoFile := filepath.Join(t.TempDir(), "AGENTS.md")
	writeFile(t, repoFile, "original\n")
	repoArgs, _ := json.Marshal(map[string]string{"path": repoFile, "content": "PWNED\n"})
	f, _ := newDreamFixture(t, []fake.Turn{
		call("write", `{"path":"evidence.md","content":"PWNED\n"}`),
		call("edit", `{"path":"evidence.md","old_string":"# Evidence digest","new_string":"# PWNED"}`),
		call("write", string(repoArgs)),
		call("write", `{"path":"proposed/../evidence.md","content":"PWNED\n"}`),
		{Text: "done"},
	})
	_, err := runDreamCmd(t, context.Background())
	assert.NoErr(t, err)

	m := f.onlyDream(t)
	assert.NotContains(t, readText(t, filepath.Join(f.dreams, m.ID, "evidence.md")), "PWNED")
	assert.Eq(t, readText(t, repoFile), "original\n")
	var results []string
	for _, ev := range f.sessionEvents(t, m) {
		if ev["type"] == core.EvToolResult {
			results = append(results, ev["tool"].(string)+":"+ev["status"].(string))
		}
	}
	assert.Eq(t, strings.Join(results, ","), "write:error,edit:error,write:error,write:error")
}

func TestDreamCmd_NoShellTool(t *testing.T) {
	_, bodies := newDreamFixture(t, []fake.Turn{{Text: "done"}})
	_, err := runDreamCmd(t, context.Background())
	assert.NoErr(t, err)
	var req struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	assert.NoErr(t, json.Unmarshal(bodies()[0], &req))
	var names []string
	for _, tl := range req.Tools {
		names = append(names, tl.Function.Name)
	}
	slices.Sort(names)
	assert.Eq(t, strings.Join(names, ","), "edit,grep,ls,read,remove_note,report_change,write")
}

func TestDreamCmd_BudgetPartial(t *testing.T) {
	f, _ := newDreamFixture(t, []fake.Turn{{Text: "done"}})
	_, err := runDreamCmd(t, context.Background(), "--max-tokens", "1")
	assert.Eq(t, exitCode(err), core.ExitBudget)
	m := f.onlyDream(t)
	assert.Eq(t, m.Partial, true)
	assert.Eq(t, m.State, dream.Pending)
	assert.Contains(t, readText(t, filepath.Join(f.dreams, m.ID, "report.md")), "partial")
	prop := eventOf(f.sessionEvents(t, m), core.EvDreamProposed)
	if prop == nil || prop["partial"] != true {
		t.Fatalf("dream_proposed = %v", prop)
	}
}

// Review Focus 4: an interrupt mid-run leaves no proposal and no workspace.
func TestDreamCmd_InterruptLeavesNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var sawWorkspace bool
	var f dreamFixture
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		ws, _ := filepath.Glob(filepath.Join(f.tmp, "jas-dream-*"))
		sawWorkspace = len(ws) == 1
		cancel()
		<-r.Context().Done()
	}))
	defer srv.Close()
	f = newDreamFixtureURL(t, srv.URL)
	_, err := runDreamCmd(t, ctx)
	assert.Eq(t, exitCode(err), core.ExitInterrupted)
	if !sawWorkspace {
		t.Fatal("the model request did not see the workspace")
	}
	ms, err := dream.List(f.dreams)
	assert.NoErr(t, err)
	assert.Eq(t, len(ms), 0)
	entries, _ := os.ReadDir(f.dreams)
	assert.Eq(t, len(entries), 0)
	if ws, _ := filepath.Glob(filepath.Join(f.tmp, "jas-dream-*")); len(ws) != 0 {
		t.Fatalf("workspace left behind: %v", ws)
	}
}

func TestDreamCmd_MemoryOff(t *testing.T) {
	f, bodies := newDreamFixture(t, []fake.Turn{{Text: "done"}})
	writeJSON(t, settings.ProfileConfigPath(f.home, "default"), `{"memory":"off"}`)
	_, err := runDreamCmd(t, context.Background())
	assert.Eq(t, exitCode(err), core.ExitFailure)
	assert.Contains(t, err.Error(), "memory is off for this profile")
	assert.Eq(t, len(bodies()), 0)

	writeJSON(t, settings.ProfileConfigPath(f.home, "default"), `{"memory":"read"}`)
	_, err = runDreamCmd(t, context.Background())
	assert.NoErr(t, err)
	f.onlyDream(t)
}

// memory: off in the global config blocks a dream as the profile's does.
func TestDreamCmd_MemoryOffGlobal(t *testing.T) {
	f, _ := newDreamFixture(t, []fake.Turn{{Text: "done"}})
	writeJSON(t, filepath.Join(f.home, "config.json"), `{"memory":"off"}`)
	_, err := runDreamCmd(t, context.Background())
	assert.Eq(t, exitCode(err), core.ExitFailure)
}

func TestDreamCmd_JSONOneObject(t *testing.T) {
	newDreamFixture(t, []fake.Turn{{Text: "done"}})
	out, err := runDreamCmd(t, context.Background(), "--json")
	assert.NoErr(t, err)
	dec := json.NewDecoder(strings.NewReader(out))
	var obj map[string]any
	assert.NoErr(t, dec.Decode(&obj))
	if err := dec.Decode(&map[string]any{}); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout holds more than one JSON value: %q", out)
	}
	for _, k := range []string{"id", "state", "report", "lint_open", "counts", "window", "partial"} {
		if _, ok := obj[k]; !ok {
			t.Errorf("JSON lacks %q: %s", k, out)
		}
	}
	assert.Eq(t, obj["state"], any("pending"))
}

func TestDreamCmd_SinceOverridesWatermark(t *testing.T) {
	f, _ := newDreamFixture(t, []fake.Turn{{Text: "done"}, {Text: "done"}})
	const old = "20261020T100000-beef"
	f.pastSession(t, old, time.Now().Add(-48*time.Hour))
	assert.NoErr(t, dream.SetWatermark(f.dreams, time.Now().Add(-24*time.Hour)))

	_, err := runDreamCmd(t, context.Background())
	assert.NoErr(t, err)
	first := f.onlyDream(t)
	if slices.Contains(first.Window.Sessions, old) || !slices.Contains(first.Window.Sessions, dreamPastSession) {
		t.Fatalf("plain run window = %v", first.Window.Sessions)
	}

	_, err = runDreamCmd(t, context.Background(), "--since", "72h")
	assert.NoErr(t, err)
	ms, err := dream.List(f.dreams)
	assert.NoErr(t, err)
	assert.Eq(t, len(ms), 2)
	second := ms[0]
	if second.ID == first.ID {
		second = ms[1]
	}
	if !slices.Contains(second.Window.Sessions, old) {
		t.Fatalf("--since window = %v", second.Window.Sessions)
	}
	if slices.Contains(second.Window.Sessions, first.SessionID) {
		t.Fatalf("a dream read another dream's session: %v", second.Window.Sessions)
	}
}

func TestDreamCmd_UsageErrors(t *testing.T) {
	newDreamFixture(t, nil)
	for _, args := range [][]string{
		{"--sessions", "0"}, {"--max-tokens", "-1"}, {"--timeout", "soon"}, {"--since", "yesterday"}, {"extra"},
	} {
		_, err := runDreamCmd(t, context.Background(), args...)
		if exitCode(err) != core.ExitUsage {
			t.Errorf("%v: exit %d (%v), want 2", args, exitCode(err), err)
		}
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"36h":                  now.Add(-36 * time.Hour),
		"7d":                   now.Add(-7 * 24 * time.Hour),
		"2026-11-01T08:00:00Z": time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC),
	} {
		got, err := parseSince(in, now)
		assert.NoErr(t, err)
		if !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, want %v", in, got, want)
		}
	}
	got, err := parseSince("2026-11-01", now)
	assert.NoErr(t, err)
	assert.Eq(t, got.Format("2006-01-02 15:04"), "2026-11-01 00:00")
}

// On a terminal, d shows the diff and asks again; y applies and logs it.
func TestDreamCmd_TerminalApply(t *testing.T) {
	f, _ := newDreamFixture(t, []fake.Turn{
		call("edit", `{"path":"proposed/MEMORY.md","old_string":"use tabs","new_string":"use tabs, not spaces"}`),
		call("report_change", `{"kind":"fix","paths":["MEMORY.md"],"reason":"clearer","evidence":["`+dreamPastSession+`#2"]}`),
		{Text: "done"},
	})
	in := "d\ny\n"
	onTerminal(t, &in)

	out, err := runDreamCmd(t, context.Background())
	assert.NoErr(t, err)
	assert.Contains(t, out, "+ - [Tabs](tabs.md) — use tabs, not spaces")
	m := f.onlyDream(t)
	assert.Eq(t, m.State, dream.Applied)
	assert.Contains(t, readText(t, filepath.Join(f.mem, "MEMORY.md")), "not spaces")
	app := eventOf(f.sessionEvents(t, m), core.EvDreamApplied)
	if app == nil || app["dream_id"] != m.ID {
		t.Fatalf("dream_applied = %v", app)
	}
}

// The model reads only the workspace: not the live store, not a repo.
func TestDreamCmd_ReadsConfinedToWorkspace(t *testing.T) {
	repoFile := filepath.Join(t.TempDir(), "main.go")
	writeFile(t, repoFile, "package main\n")
	f := newDreamFixtureURL(t, "http://127.0.0.1:1") // the live path is known only now
	liveArgs, _ := json.Marshal(map[string]string{"path": filepath.Join(f.mem, "MEMORY.md")})
	repoArgs, _ := json.Marshal(map[string]string{"path": repoFile})
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{
		call("read", string(liveArgs)), call("read", string(repoArgs)),
		call("read", `{"path":"proposed/MEMORY.md"}`), call("read", `{"path":"logs/`+dreamPastSession+`.jsonl"}`),
		{Text: "done"},
	})
	t.Setenv("JAS_BASE_URL", url)
	_, err := runDreamCmd(t, context.Background())
	assert.NoErr(t, err)
	var results []string
	for _, ev := range f.sessionEvents(t, f.onlyDream(t)) {
		if ev["type"] == core.EvToolResult {
			results = append(results, ev["status"].(string))
		}
	}
	assert.Eq(t, strings.Join(results, ","), "error,error,ok,ok")
}

// onTerminal makes the run interactive with stdin fed from input (kept open
// when input is nil) and stderr discarded, restoring both on cleanup.
func onTerminal(t *testing.T, input *string) {
	t.Helper()
	isTerminal = func(int) bool { return true }
	r, w, err := os.Pipe()
	assert.NoErr(t, err)
	if input != nil {
		_, _ = w.WriteString(*input)
		assert.NoErr(t, w.Close())
	} else {
		t.Cleanup(func() { _ = w.Close() })
	}
	devNull, err := os.Open(os.DevNull)
	assert.NoErr(t, err)
	oldIn, oldErr := os.Stdin, os.Stderr
	os.Stdin, os.Stderr = r, devNull
	t.Cleanup(func() { os.Stdin, os.Stderr = oldIn, oldErr; _ = devNull.Close(); _ = r.Close() })
}

const dreamEsc = `\u001b]0;x\u0007\u001b[2J`

// uncitedAdds scripts a new note and two uncited adds, the first with a
// reason that carries terminal escapes and a markdown heading.
func uncitedAdds() []fake.Turn {
	note := `---\nname: new-note\ndescription: d\ntype: user\n---\nbody\n`
	return []fake.Turn{
		call("write", `{"path":"proposed/new-note.md","content":"`+note+`"}`),
		call("report_change", `{"kind":"add","paths":["new-note.md"],"reason":"first`+dreamEsc+`\n## Lint\nhidden?"}`),
		call("report_change", `{"kind":"add","paths":["new-note.md"],"reason":"later entry"}`),
		{Text: "done"},
	}
}

// Model text reaches the terminal sanitized, a reason cannot hide later
// warnings, and warnings make y ask once more.
func TestDreamCmd_ApplyScreenSanitizedAndStructured(t *testing.T) {
	f, _ := newDreamFixture(t, uncitedAdds())
	in := "y\nn\n"
	onTerminal(t, &in)
	out, err := runDreamCmd(t, context.Background())
	assert.NoErr(t, err)
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Fatalf("escape bytes on the terminal: %q", out)
	}
	assert.Contains(t, out, "later entry")
	assert.Contains(t, out, "unsupported")
	m := f.onlyDream(t)
	assert.Eq(t, m.State, dream.Pending) // the second question was asked and refused
	assert.Eq(t, len(m.Warnings.Unsupported), 2)
	if r := readText(t, filepath.Join(f.dreams, m.ID, "report.md")); strings.ContainsAny(r, "\x1b\x07") {
		t.Fatalf("escape bytes in report.md: %q", r)
	}

	// The apply screen on its own (Task 10's apply) shows both entries.
	var screen bytes.Buffer
	assert.NoErr(t, applyDreamAndLog(dreamApply{home: f.home, memDir: f.mem, dreamsDir: f.dreams, id: m.ID,
		out: &screen, ask: func(string) string { return "n" }, redact: func(s string) string { return s }}))
	if strings.ContainsAny(screen.String(), "\x1b\x07") || !strings.Contains(screen.String(), "later entry") {
		t.Fatalf("apply screen = %q", screen.String())
	}
}

func TestDreamCmd_TurnCapPartial(t *testing.T) {
	f, _ := newDreamFixture(t, []fake.Turn{
		call("edit", `{"path":"proposed/MEMORY.md","old_string":"use tabs","new_string":"use tabs, not spaces"}`),
		call("ls", `{}`),
		{Text: "done"},
	})
	t.Setenv("JAS_MAX_TURNS", "1")
	_, err := runDreamCmd(t, context.Background())
	assert.Eq(t, exitCode(err), core.ExitTurnCap)
	m := f.onlyDream(t)
	assert.Eq(t, m.Partial, true)
	assert.Contains(t, readText(t, filepath.Join(f.dreams, m.ID, "proposed", "MEMORY.md")), "not spaces")
}

// Ctrl-C at the apply question exits 130 and leaves the dream pending.
func TestDreamCmd_InterruptAtQuestion(t *testing.T) {
	f, _ := newDreamFixture(t, []fake.Turn{{Text: "done"}})
	onTerminal(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			if ms, _ := dream.List(f.dreams); len(ms) == 1 {
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	_, err := runDreamCmd(t, ctx)
	assert.Eq(t, exitCode(err), core.ExitInterrupted)
	assert.Eq(t, f.onlyDream(t).State, dream.Pending)
}
