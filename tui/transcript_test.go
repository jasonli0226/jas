package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/internal/assert"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func plain(blocks []string) []string {
	out := make([]string, len(blocks))
	for i, b := range blocks {
		out[i] = ansi.Strip(b)
	}
	return out
}

func eqStrings(t *testing.T, got, want []string) {
	t.Helper()
	assert.Eq(t, strings.Join(got, "|"), strings.Join(want, "|"))
	assert.Eq(t, len(got), len(want))
}

func TestTranscript_ThinkingHiddenByDefault(t *testing.T) {
	var tr Transcript
	assert.Eq(t, len(tr.Reasoning("plan", t0)), 0)
	got := tr.Text("Hi\n\n", t0.Add(3*time.Second))
	eqStrings(t, plain(got), []string{"· thought for 3s", "● Hi"})
}

func TestTranscript_ThinkingShownWhenEnabled(t *testing.T) {
	tr := Transcript{ShowThinking: true}
	got := tr.Reasoning("plan\n\n", t0)
	got = append(got, tr.Text("Hi\n\n", t0.Add(500*time.Millisecond))...)
	eqStrings(t, plain(got), []string{"plan", "· thought for 1s", "● Hi"})
}

func TestTranscript_ToolEventsPrintRowInOrder(t *testing.T) {
	var tr Transcript
	tr.Text("before", t0)
	started := map[string]any{"call_id": "c1", "tool": "read", "mutating": false,
		"args": map[string]any{"path": "a.go"}}
	got := plain(tr.Event("tool_started", started, t0))
	eqStrings(t, got, []string{"● before"})
	got = plain(tr.Event("tool_result", map[string]any{"call_id": "c1", "tool": "read",
		"status": "ok", "output": "x\ny\n"}, t0))
	eqStrings(t, got, []string{"", "▸ read a.go (2 lines)"})
}

func TestTranscript_FlushPrintsPartialAndThought(t *testing.T) {
	var tr Transcript
	tr.Reasoning("r", t0)
	tr.Text("tail", t0)
	// text already closed thinking; Flush prints only the partial block.
	eqStrings(t, plain(tr.Flush(t0)), []string{"● tail"})
	tr.Reasoning("r", t0)
	eqStrings(t, plain(tr.Flush(t0.Add(2*time.Second))), []string{"· thought for 2s"})
}

func TestTranscript_User(t *testing.T) {
	var tr Transcript
	eqStrings(t, plain(tr.User("hi")), []string{"› hi"})
	eqStrings(t, plain(tr.User("a\nb")), []string{"› a\n  b"})
}

func TestTranscript_UserChrome(t *testing.T) {
	var tr Transcript
	got := plain(tr.User("hi"))
	assert.Eq(t, strings.HasPrefix(got[0], "› hi"), true)
	assert.Eq(t, userStyle.GetBold(), true)
	assert.Eq(t, userStyle.GetForeground(), lipgloss.Color("6"))
	assert.Eq(t, assistantStyle.GetForeground() != userStyle.GetForeground(), true)
}

func TestTranscript_AssistantChrome(t *testing.T) {
	var tr Transcript
	got := plain(tr.Text("Hello\nthere\n\nWorld\n\n", t0))
	eqStrings(t, got, []string{"● Hello\n  there", "  World"})

	// A tool row ends the agent turn; the next text is a new turn.
	tr.Event("tool_started", map[string]any{"call_id": "c", "args": map[string]any{}}, t0)
	tr.Event("tool_result", map[string]any{"call_id": "c", "tool": "ls", "status": "ok"}, t0)
	got = plain(tr.Text("Again\n\n", t0))
	eqStrings(t, got, []string{"", "● Again"})
}

func TestTranscript_AssistantLiveMatchesPrinted(t *testing.T) {
	var tr Transcript
	tr.User("hi")
	tr.Text("par", t0)
	assert.Eq(t, ansi.Strip(tr.Live()), "\n● par")
	tr.Text("t\n\nnext", t0)
	assert.Eq(t, ansi.Strip(tr.Live()), "  next")
}

func TestTranscript_TurnGap(t *testing.T) {
	var tr Transcript
	got := tr.User("hi")
	got = append(got, tr.Text("Hello", t0)...)
	got = append(got, tr.Flush(t0)...)
	got = append(got, tr.User("more")...)
	eqStrings(t, plain(got), []string{"› hi", "", "● Hello", "", "› more"})
}

func TestToolRow_Formats(t *testing.T) {
	lines := strings.Repeat("l\n", 212)
	tests := []struct {
		name, tool, status, output, want string
		args                             map[string]any
	}{
		{"read", "read", "ok", lines, "▸ read core/budget_test.go (212 lines)", map[string]any{"path": "core/budget_test.go"}},
		{"ls default", "ls", "ok", "a\nb\n", "▸ ls . (2 entries)", nil},
		{"ls path", "ls", "ok", "a\n", "▸ ls core (1 entries)", map[string]any{"path": "core"}},
		{"grep", "grep", "ok", "a:1\nb:2\nc:3", "▸ grep foo (3 matches)", map[string]any{"pattern": "foo"}},
		{"shell ok", "shell", "ok", "x", "▸ shell go build ok", map[string]any{"command": "go build"}},
		{"shell error", "shell", "error", "exit status 1\nmore", "▸ shell go test ./core failed: exit status 1", map[string]any{"command": "go test ./core"}},
		{"write", "write", "ok", "", "▸ write a.txt ok", map[string]any{"path": "a.txt"}},
		{"edit denied", "edit", "denied", "", "▸ edit a.txt denied", map[string]any{"path": "a.txt"}},
		{"cancelled", "shell", "cancelled", "", "▸ shell sleep 9 cancelled", map[string]any{"command": "sleep 9"}},
		{"other", "fetch", "ok", "", `▸ fetch {"url":"http://x"} ok`, map[string]any{"url": "http://x"}},
		{"control chars", "shell", "ok", "", `▸ shell echo \x1b[31m ok`, map[string]any{"command": "echo \x1b[31m"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Eq(t, ansi.Strip(toolRow(tc.tool, tc.args, tc.status, tc.output, 200)), tc.want)
		})
	}
}

func TestToolRow_TruncatesToWidth(t *testing.T) {
	row := ansi.Strip(toolRow("shell", map[string]any{"command": "go test ./... -race -count=1"}, "ok", "", 20))
	assert.Eq(t, len([]rune(row)), 20)
	assert.Eq(t, strings.HasSuffix(row, "…"), true)
}

func TestTranscript_JSONRoundTripShape(t *testing.T) {
	var data map[string]any
	assert.NoErr(t, json.Unmarshal([]byte(`{"call_id":"c","tool":"grep","status":"ok","output":"a\nb","args":{"pattern":"p"}}`), &data))
	var tr Transcript
	tr.Event("tool_started", data, t0)
	got := plain(tr.Event("tool_result", data, t0))
	eqStrings(t, got, []string{"▸ grep p (2 matches)"})
}

func TestTranscript_CompactionRow(t *testing.T) {
	var tr Transcript
	got := tr.Event("compaction", map[string]any{"tokens_before": json.Number("150000"), "tokens_after": json.Number("18000")}, t0)
	eqStrings(t, plain(got), []string{"· compacted earlier messages (150.0k → 18.0k tok)"})
}
