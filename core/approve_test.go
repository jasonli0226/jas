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
			// Not interactive: a gated call is Unavailable, an ungated one Granted.
			a := &core.Approver{Level: c.level}
			d, err := a.Allow(context.Background(), toolByName(t, name), json.RawMessage(`{}`))
			assert.NoErr(t, err)
			got := d == core.Unavailable
			if got != want || (!want && d != core.Granted) {
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
		d, err := a.Allow(context.Background(), toolByName(t, "shell"), json.RawMessage(`{"command":"ls -la\nrm x"}`))
		assert.NoErr(t, err)
		assert.Eq(t, d, want)
		assert.Contains(t, out.String(), "approve shell: ls -la rm x? [y/N] ")
	}
}

func TestApprover_NoTTY(t *testing.T) {
	a := &core.Approver{Level: core.ShellOnly, Interactive: false}
	d, err := a.Allow(context.Background(), toolByName(t, "shell"), json.RawMessage(`{"command":"x"}`))
	assert.NoErr(t, err)
	assert.Eq(t, d, core.Unavailable)
}

func TestApprover_SummaryTruncated(t *testing.T) {
	var out bytes.Buffer
	a := &core.Approver{Level: core.AllMutating, In: strings.NewReader("n\n"), Out: &out, Interactive: true}
	args, _ := json.Marshal(map[string]string{"path": strings.Repeat("a", 500)})
	_, err := a.Allow(context.Background(), toolByName(t, "write"), args)
	assert.NoErr(t, err)
	assert.Contains(t, out.String(), "approve write: "+strings.Repeat("a", 200)+"? [y/N] ")
	assert.NotContains(t, out.String(), strings.Repeat("a", 201))
}

func TestApprover_CtrlCWhilePrompting(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	a := &core.Approver{Level: core.ShellOnly, In: pr, Out: io.Discard, Interactive: true}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := a.Allow(ctx, toolByName(t, "shell"), json.RawMessage(`{"command":"x"}`))
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
