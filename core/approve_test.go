package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers/fake"
	"github.com/jasonli0226/jas/tools"
)

func toolByName(t *testing.T, name string) core.Tool {
	t.Helper()
	all := append(tools.ReadOnly(), tools.Mutating()...)
	for _, tl := range all {
		if tl.Name() == name {
			return tl
		}
	}
	t.Fatalf("no tool %q", name)
	return nil
}

func TestApprover_LadderTable(t *testing.T) {
	cases := []struct {
		level core.Level
		gated map[string]bool
	}{
		{core.Never, map[string]bool{"read": false, "write": false, "edit": false, "shell": false}},
		{core.ShellOnly, map[string]bool{"read": false, "write": false, "edit": false, "shell": true}},
		{core.AllMutating, map[string]bool{"read": false, "write": true, "edit": true, "shell": true}},
	}
	for _, c := range cases {
		for name, want := range c.gated {
			// Not interactive: a gated call is Unavailable, an ungated one AutoGranted.
			a := &core.Approver{Level: c.level}
			d, err := a.Allow(context.Background(), toolByName(t, name), json.RawMessage(`{}`), nil)
			assert.NoErr(t, err)
			got := d == core.Unavailable
			if got != want || (!want && d != core.AutoGranted) {
				t.Errorf("level %d tool %s: decision %q, gated want %v", c.level, name, d, want)
			}
		}
	}
}

func TestParseLevel(t *testing.T) {
	for s, want := range map[string]core.Level{"never": core.Never, "shell": core.ShellOnly, "mutating": core.AllMutating} {
		got, err := core.ParseLevel(s)
		assert.NoErr(t, err)
		assert.Eq(t, got, want)
	}
	_, err := core.ParseLevel("bogus")
	if err == nil {
		t.Fatal("want error")
	}
	assert.Contains(t, err.Error(), "never|shell|mutating")
}

func TestApprover_YesNo(t *testing.T) {
	for in, want := range map[string]core.Decision{
		"y\n": core.Granted, "yes\n": core.Granted, "\n": core.Denied, "nope\n": core.Denied, "": core.Denied,
	} {
		var out bytes.Buffer
		a := &core.Approver{Level: core.ShellOnly, In: strings.NewReader(in), Out: &out, Interactive: true}
		d, err := a.Allow(context.Background(), toolByName(t, "shell"), json.RawMessage(`{"command":"ls -la\nrm x"}`), nil)
		assert.NoErr(t, err)
		assert.Eq(t, d, want)
		assert.Contains(t, out.String(), "approve shell: ls -la rm x? [y/N] ")
	}
}

func TestApprover_NoTTY(t *testing.T) {
	a := &core.Approver{Level: core.ShellOnly, Interactive: false}
	d, err := a.Allow(context.Background(), toolByName(t, "shell"), json.RawMessage(`{"command":"x"}`), nil)
	assert.NoErr(t, err)
	assert.Eq(t, d, core.Unavailable)
}

func TestApprover_SummaryTruncated(t *testing.T) {
	var out bytes.Buffer
	a := &core.Approver{Level: core.ShellOnly, In: strings.NewReader("n\n"), Out: &out, Interactive: true}
	args, _ := json.Marshal(map[string]string{"command": strings.Repeat("a", 2500)})
	_, err := a.Allow(context.Background(), toolByName(t, "shell"), args, nil)
	assert.NoErr(t, err)
	assert.Contains(t, out.String(), strings.Repeat("a", 2000)+" …[+500 more chars]? [y/N] ")
}

func TestApprover_SanitizesControlChars(t *testing.T) {
	var out bytes.Buffer
	a := &core.Approver{Level: core.ShellOnly, In: strings.NewReader("n\n"), Out: &out, Interactive: true}
	args, _ := json.Marshal(map[string]string{"command": "a\x1b[2Kb\bc\x00d\u202ee"})
	_, err := a.Allow(context.Background(), toolByName(t, "shell"), args, nil)
	assert.NoErr(t, err)
	for _, bad := range []string{"\x1b", "\b", "\x00", "\u202e"} {
		assert.NotContains(t, out.String(), bad)
	}
	assert.Contains(t, out.String(), `a\x1b[2Kb\x08c\x00d\u202ee`)
}

func TestApprover_EmptySummaryFallsBackToArgs(t *testing.T) {
	var out bytes.Buffer
	a := &core.Approver{Level: core.ShellOnly, In: strings.NewReader("n\n"), Out: &out, Interactive: true}
	_, err := a.Allow(context.Background(), toolByName(t, "shell"), json.RawMessage(`{"cmd":"rm -rf /"}`), nil)
	assert.NoErr(t, err)
	assert.Contains(t, out.String(), `approve shell: {"cmd":"rm -rf /"}? [y/N] `)
}

func TestApprover_CtrlCWhilePrompting(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	a := &core.Approver{Level: core.ShellOnly, In: pr, Out: io.Discard, Interactive: true}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := a.Allow(ctx, toolByName(t, "shell"), json.RawMessage(`{"command":"x"}`), nil)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		assert.Eq(t, err, context.Canceled)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Allow did not return after cancel")
	}
}

func TestRun_DeniedContinues(t *testing.T) {
	r := newRig(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "write", Args: `{"path":"a.txt","content":"x"}`}}},
		{Text: "ok, not writing"},
	})
	r.cfg.Exec.Reg = core.NewRegistry(append(tools.ReadOnly(), tools.Mutating()...)...)
	r.cfg.Exec.Gate = &core.Approver{Level: core.AllMutating, In: strings.NewReader("n\n"), Out: io.Discard, Interactive: true}
	_, err := core.Run(context.Background(), r.cfg)
	assert.NoErr(t, err)
	var sawDenied bool
	for _, e := range r.events(t) {
		if e["type"] == "approval" && e["decision"] == "denied" {
			sawDenied = true
		}
	}
	if !sawDenied {
		t.Fatal("no denied approval event")
	}
	b, err := json.Marshal(r.prov.Requests[1])
	assert.NoErr(t, err)
	assert.Contains(t, string(b), "denied")
}

func TestApprover_PrintsDiffBeforeQuestion(t *testing.T) {
	var out bytes.Buffer
	a := &core.Approver{Level: core.AllMutating, In: strings.NewReader("n\n"), Out: &out, Interactive: true}
	preview := func() *core.Diff {
		return core.ComputeDiff(core.Change{Path: "a.txt", Old: "old\n", New: "new\n"}, nil)
	}
	_, err := a.Allow(context.Background(), toolByName(t, "edit"), json.RawMessage(`{"path":"a.txt"}`), preview)
	assert.NoErr(t, err)
	s := out.String()
	minus, plus, q := strings.Index(s, "- old"), strings.Index(s, "+ new"), strings.Index(s, "approve edit:")
	if minus < 0 || plus < 0 || q < 0 || minus > q || plus > q {
		t.Fatalf("want diff rows before the question, got %q", s)
	}
	assert.Eq(t, strings.HasPrefix(s, "edit a.txt\n"), true)
}

func TestSanitizeLine_KeepsInnerSpacing(t *testing.T) {
	assert.Eq(t, core.SanitizeLine("  echo  a\tb\nc\x1b[2J "), `echo  a b c\x1b[2J`)
}

func TestSummarizeCall_KeepsCommandSpacing(t *testing.T) {
	assert.Eq(t, core.SummarizeCall("shell", json.RawMessage(`{"command":"grep \"a  b\" x"}`)), `grep "a  b" x`)
}
