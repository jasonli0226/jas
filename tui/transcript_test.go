package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"fmt"
	"image/color"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// plain strips styling and the background bands' padding: trailing spaces
// on each line and the blank rows above and below a band.
func plain(blocks []string) []string {
	out := make([]string, len(blocks))
	for i, b := range blocks {
		lines := strings.Split(ansi.Strip(b), "\n")
		for j, l := range lines {
			lines[j] = strings.TrimRight(l, " ")
		}
		for len(lines) > 1 && lines[0] == "" {
			lines = lines[1:]
		}
		for len(lines) > 1 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		out[i] = strings.Join(lines, "\n")
	}
	return out
}

// bgSeq is the SGR parameter lipgloss emits for a truecolor background.
func bgSeq(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, b>>8)
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
	assert.Eq(t, userStyle.GetBackground(), userBg)
	assert.Eq(t, assistantStyle.GetForeground() != userStyle.GetForeground(), true)
}

func TestTranscript_UserBand(t *testing.T) {
	tr := Transcript{Width: 20}
	got := tr.User("hi")
	assert.Eq(t, len(got), 1)
	lines := strings.Split(ansi.Strip(got[0]), "\n")
	assert.Eq(t, len(lines), 3) // padding row, text, padding row
	assert.Eq(t, strings.TrimRight(lines[1], " "), "› hi")
	for _, l := range lines {
		assert.Eq(t, ansi.StringWidth(l), 20)
	}
	assert.Contains(t, got[0], bgSeq(userBg))
}

func TestTranscript_AssistantChrome(t *testing.T) {
	var tr Transcript
	got := plain(tr.Text("Hello\nthere\n\nWorld\n\n", t0))
	eqStrings(t, got, []string{"● Hello\n  there", "", "  World"})

	// A tool row ends the agent turn; the next text is a new turn.
	tr.Event("tool_started", map[string]any{"call_id": "c", "args": map[string]any{}}, t0)
	tr.Event("tool_result", map[string]any{"call_id": "c", "tool": "ls", "status": "ok"}, t0)
	got = plain(tr.Text("Again\n\n", t0))
	eqStrings(t, got, []string{"", "● Again"})
}

func TestTranscript_WrapsUnderMarker(t *testing.T) {
	tr := Transcript{Width: 12}
	got := plain(tr.Text("aaa bbb ccc ddd\n\n", t0))
	eqStrings(t, got, []string{"● aaa bbb\n  ccc ddd"})
	got = plain(tr.User("one two three four"))
	eqStrings(t, got, []string{"", "› one two\n  three four"})
	// Live wraps the same way, so nothing shifts when the block is printed.
	tr.Text("eee fff ggg hhh", t0)
	assert.Eq(t, ansi.Strip(tr.Live()), "\n● eee fff\n  ggg hhh")
}

func TestTranscript_AssistantLiveMatchesPrinted(t *testing.T) {
	var tr Transcript
	tr.User("hi")
	tr.Text("par", t0)
	assert.Eq(t, ansi.Strip(tr.Live()), "\n● par")
	tr.Text("t\n\nnext", t0)
	assert.Eq(t, ansi.Strip(tr.Live()), "\n  next")
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
			assert.Eq(t, strings.TrimRight(ansi.Strip(toolRow(tc.tool, tc.args, tc.status, tc.output, 200)), " "), tc.want)
		})
	}
}

func TestToolRow_StatusBand(t *testing.T) {
	ok := toolRow("ls", nil, "ok", "a\n", 30)
	assert.Eq(t, ansi.StringWidth(ok), 30)
	assert.Contains(t, ok, bgSeq(toolOkBg))
	for _, status := range []string{"error", "denied", "cancelled"} {
		row := toolRow("shell", map[string]any{"command": "x"}, status, "", 30)
		assert.Contains(t, row, bgSeq(toolErrBg))
	}
}

func TestBands_DistinctOnANSI16(t *testing.T) {
	ansi16 := func(c color.Color) color.Color { return colorprofile.ANSI.Convert(c) }
	seen := map[color.Color]string{}
	for name, c := range map[string]color.Color{"user": userBg, "ok": toolOkBg, "err": toolErrBg, "other": toolOtherBg} {
		got := ansi16(c)
		if prev, dup := seen[got]; dup {
			t.Fatalf("%s and %s bands both map to %v on 16-colour terminals", prev, name, got)
		}
		seen[got] = name
	}
}

func TestDiffBgs_RedAndGreenOnANSI16(t *testing.T) {
	// On 16 colours a removed line stays red and an added line green, so the
	// two still read apart; the word bands keep the line's hue or near it.
	for name, c := range map[string]color.Color{"del": diffDelBg, "del word": diffDelWordBg} {
		if got := colorprofile.ANSI.Convert(c); got != color.Color(ansi.Red) {
			t.Fatalf("%s band is %v on 16 colours, want red", name, got)
		}
	}
	assert.Eq(t, colorprofile.ANSI.Convert(diffAddBg), color.Color(ansi.Green))
	for name, c := range map[string]color.Color{"del": diffDelBg, "add": diffAddBg, "del word": diffDelWordBg, "add word": diffAddWordBg} {
		// 256 colours: off the grey ramp (232-255), so the hue survives.
		if idx := colorprofile.ANSI256.Convert(c).(ansi.IndexedColor); idx >= 232 {
			t.Fatalf("%s band lands on grey %d on 256 colours", name, idx)
		}
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

func TestTranscript_ToolStartedShowsRunningRow(t *testing.T) {
	var tr Transcript
	eqStrings(t, plain(tr.Text("before\n\n", t0)), []string{"● before"})
	started := map[string]any{"call_id": "c1", "tool": "read", "args": map[string]any{"path": "a.go"}}
	eqStrings(t, plain(tr.Event("tool_started", started, t0)), []string{})
	// The running row lives in the live region with the gap it will print
	// with, so the final row lands where the running one was.
	assert.Eq(t, strings.TrimRight(ansi.Strip(tr.Live()), " "), "\n▸ read a.go running")
	got := plain(tr.Event("tool_result", map[string]any{"call_id": "c1", "tool": "read",
		"status": "ok", "output": "x\n"}, t0))
	eqStrings(t, got, []string{"", "▸ read a.go (1 lines)"})
	assert.Eq(t, tr.Live(), "")
}

func TestTranscript_ParallelToolsStayRunningUntilTheirResult(t *testing.T) {
	var tr Transcript
	for _, id := range []string{"a", "b"} {
		tr.Event("tool_started", map[string]any{"call_id": id, "tool": "ls",
			"args": map[string]any{"path": id}}, t0)
	}
	eqStrings(t, plain([]string{tr.Live()}), []string{"▸ ls a running\n▸ ls b running"})
	tr.Event("tool_result", map[string]any{"call_id": "a", "tool": "ls", "status": "ok"}, t0)
	eqStrings(t, plain([]string{tr.Live()}), []string{"▸ ls b running"})
}

func TestTranscript_HistoryReprintsTurns(t *testing.T) {
	msgs := []providers.Message{
		{Role: providers.RoleUser, Content: []providers.ContentBlock{
			{Type: providers.BlockText, Text: "[jas] The earlier conversation was compacted. Summary:\n\nold"}}},
		{Role: providers.RoleUser, Content: []providers.ContentBlock{{Type: providers.BlockText, Text: "hello"}}},
		{Role: providers.RoleAssistant, Content: []providers.ContentBlock{
			{Type: providers.BlockText, Text: "hi there"},
			{Type: providers.BlockToolUse, ToolUseID: "c1", Name: "ls", Input: json.RawMessage(`{"path":"src"}`)}}},
		{Role: providers.RoleUser, Content: []providers.ContentBlock{
			{Type: providers.BlockToolResult, ToolUseID: "c1", Text: "a\nb"}}},
		{Role: providers.RoleAssistant, Content: []providers.ContentBlock{{Type: providers.BlockText, Text: "done"}}},
	}
	var tr Transcript
	got := plain(tr.History(msgs, func(s string) string { return s }, t0))
	eqStrings(t, got, []string{"· compacted earlier messages", "", "› hello", "", "● hi there",
		"", "▸ ls src (2 entries)", "", "● done"})
}

func TestTranscript_HistoryEmpty(t *testing.T) {
	var tr Transcript
	assert.Eq(t, len(tr.History(nil, func(s string) string { return s }, t0)), 0)
	eqStrings(t, plain(tr.Notice("resumed")), []string{"resumed"})
}

func TestTranscript_HistoryRedacts(t *testing.T) {
	msgs := []providers.Message{
		{Role: providers.RoleUser, Content: []providers.ContentBlock{{Type: providers.BlockText, Text: "key sk-1"}}},
		{Role: providers.RoleAssistant, Content: []providers.ContentBlock{{Type: providers.BlockText, Text: "got sk-1"}}},
	}
	var tr Transcript
	redact := func(s string) string { return strings.ReplaceAll(s, "sk-1", "***") }
	eqStrings(t, plain(tr.History(msgs, redact, t0)), []string{"› key ***", "", "● got ***"})
}

// A run can end without a tool's result (a log error, a cancelled call), so
// Flush, which ends the run, drops rows still marked running.
func TestTranscript_FlushDropsRunningRows(t *testing.T) {
	var tr Transcript
	tr.Event("tool_started", map[string]any{"call_id": "c", "tool": "ls"}, t0)
	tr.Flush(t0)
	assert.Eq(t, tr.Live(), "")
}

func TestToolRow_RunningTruncatesToWidth(t *testing.T) {
	tr := Transcript{Width: 20}
	tr.Event("tool_started", map[string]any{"call_id": "c", "tool": "shell",
		"args": map[string]any{"command": "go test ./... -race -count=1"}}, t0)
	row := ansi.Strip(tr.Live())
	assert.Eq(t, len([]rune(row)), 20)
	assert.Eq(t, strings.HasSuffix(row, "…"), true)
}

var testSkills = map[string]string{"/skills/review/SKILL.md": "review"}

func TestTranscript_SkillReadShowsSkillRow(t *testing.T) {
	tr := Transcript{Skills: testSkills}
	tr.Event("tool_started", map[string]any{"call_id": "c1", "tool": "read",
		"args": map[string]any{"path": "/skills/review/SKILL.md"}}, t0)
	eqStrings(t, plain([]string{tr.Live()}), []string{"◆ skill review running"})
	got := plain(tr.Event("tool_result", map[string]any{"call_id": "c1", "tool": "read",
		"status": "ok", "output": "x\ny\n"}, t0))
	eqStrings(t, got, []string{"◆ skill review loaded"})
}

func TestTranscript_SkillReadFailedKeepsReason(t *testing.T) {
	tr := Transcript{Skills: testSkills}
	tr.Event("tool_started", map[string]any{"call_id": "c1", "tool": "read",
		"args": map[string]any{"path": "/skills/review/SKILL.md"}}, t0)
	got := plain(tr.Event("tool_result", map[string]any{"call_id": "c1", "tool": "read",
		"status": "error", "output": "no such file"}, t0))
	eqStrings(t, got, []string{"◆ skill review failed: no such file"})
}

func TestTranscript_HistoryShowsSkillRow(t *testing.T) {
	msgs := []providers.Message{
		{Role: providers.RoleAssistant, Content: []providers.ContentBlock{
			{Type: providers.BlockToolUse, ToolUseID: "c1", Name: "read", Input: json.RawMessage(`{"path":"/skills/review/SKILL.md"}`)}}},
		{Role: providers.RoleUser, Content: []providers.ContentBlock{
			{Type: providers.BlockToolResult, ToolUseID: "c1", Text: "body"}}},
	}
	tr := Transcript{Skills: testSkills}
	got := plain(tr.History(msgs, func(s string) string { return s }, t0))
	eqStrings(t, got, []string{"◆ skill review loaded"})
}

func TestTranscript_SkillUserMarksTheInvocation(t *testing.T) {
	var tr Transcript
	eqStrings(t, plain(tr.SkillUser("review", "/skill:review fix it")),
		[]string{"◆ skill review", "› /skill:review fix it"})
}

func diffPayload(t *testing.T, d *core.Diff) any {
	t.Helper()
	b, err := json.Marshal(d)
	assert.NoErr(t, err)
	var v any
	assert.NoErr(t, json.Unmarshal(b, &v))
	return v
}

func TestTranscript_EditResultShowsDiff(t *testing.T) {
	var tr Transcript
	tr.Width = 40
	tr.Event("tool_started", map[string]any{"call_id": "c", "tool": "edit", "args": map[string]any{"path": "f.go"}}, t0)
	got := plain(tr.Event("tool_result", map[string]any{
		"call_id": "c", "tool": "edit", "status": "ok", "output": "edited f.go",
		"diff": diffPayload(t, middleDiff()),
	}, t0))
	assert.Eq(t, len(got), 9)
	assert.Eq(t, strings.TrimRight(got[0], " "), "▸ edit f.go ok")
	assert.Eq(t, strings.TrimRight(got[4], " "), "     4 - d")
}

func TestTranscript_ErrorResultNoDiff(t *testing.T) {
	var tr Transcript
	tr.Event("tool_started", map[string]any{"call_id": "c", "tool": "edit", "args": map[string]any{"path": "f.go"}}, t0)
	got := plain(tr.Event("tool_result", map[string]any{
		"call_id": "c", "tool": "edit", "status": "error", "output": "boom",
		"diff": diffPayload(t, middleDiff()),
	}, t0))
	assert.Eq(t, len(got), 1)
}
