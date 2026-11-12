package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/dream"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
)

// newDream runs jas dream with a model that rewrites the index line to
// newLine, leaving one pending dream, and returns it.
func (f dreamFixture) newDream(t *testing.T, oldLine, newLine string) dream.Meta {
	t.Helper()
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{
		call("edit", `{"path":"proposed/MEMORY.md","old_string":"`+oldLine+`","new_string":"`+newLine+`"}`),
		{Text: "done"},
	})
	t.Setenv("JAS_BASE_URL", url)
	before, err := dream.List(f.dreams)
	assert.NoErr(t, err)
	_, err = runDreamCmd(t, context.Background(), "--since", "48h")
	assert.NoErr(t, err)
	ms, err := dream.List(f.dreams)
	assert.NoErr(t, err)
	assert.Eq(t, len(ms), len(before)+1)
	for _, m := range ms { // ids made in one second sort by their random suffix
		if !slices.ContainsFunc(before, func(b dream.Meta) bool { return b.ID == m.ID }) {
			return m
		}
	}
	t.Fatal("no new dream")
	return dream.Meta{}
}

func TestDreamList_DisplayStale(t *testing.T) {
	f, _ := newDreamFixture(t, nil)
	m := f.newDream(t, "use tabs", "use tabs always")
	out, err := runDreamCmd(t, context.Background(), "list")
	assert.NoErr(t, err)
	assert.Contains(t, out, "pending")
	if strings.Contains(out, "stale") {
		t.Fatalf("fresh dream listed stale: %q", out)
	}
	writeFile(t, filepath.Join(f.mem, "tabs.md"), dreamNote+"more\n")
	out, err = runDreamCmd(t, context.Background(), "list")
	assert.NoErr(t, err)
	assert.Contains(t, out, "pending (stale)")
	out, err = runDreamCmd(t, context.Background(), "list", "--json")
	assert.NoErr(t, err)
	assert.Contains(t, out, `"stale":true`)
	assert.Eq(t, f.onlyDream(t).State, dream.Pending) // display only
	assert.Eq(t, f.onlyDream(t).ID, m.ID)
}

func TestDreamDiff_PendingStaleWarns(t *testing.T) {
	f, _ := newDreamFixture(t, nil)
	f.newDream(t, "use tabs", "use tabs always")
	out, err := runDreamCmd(t, context.Background(), "diff")
	assert.NoErr(t, err)
	assert.Contains(t, out, "use tabs always")
	assert.Contains(t, out, "## Warnings")
	if strings.Contains(out, "warning: memory changed") {
		t.Fatalf("unexpected stale warning: %q", out)
	}
	writeFile(t, filepath.Join(f.mem, "tabs.md"), dreamNote+"more\n")
	out, err = runDreamCmd(t, context.Background(), "diff")
	assert.NoErr(t, err)
	if !strings.HasPrefix(out, "warning: memory changed") {
		t.Fatalf("stale warning not first: %q", out)
	}
}

func TestDreamDiff_AppliedShowsBeforeVsLive(t *testing.T) {
	f, _ := newDreamFixture(t, nil)
	f.newDream(t, "use tabs", "use tabs always")
	_, err := runDreamCmd(t, context.Background(), "apply", "--yes")
	assert.NoErr(t, err)
	out, err := runDreamCmd(t, context.Background(), "diff", f.onlyDream(t).ID)
	assert.NoErr(t, err)
	assert.Contains(t, out, "--- MEMORY.md")
	assert.Contains(t, out, "use tabs always")
}

func TestDreamDiff_DiscardedFails(t *testing.T) {
	f, _ := newDreamFixture(t, nil)
	m := f.newDream(t, "use tabs", "use tabs always")
	_, err := runDreamCmd(t, context.Background(), "discard")
	assert.NoErr(t, err)
	_, err = runDreamCmd(t, context.Background(), "diff", m.ID)
	assert.Eq(t, exitCode(err), core.ExitFailure)
}

func TestDreamApply_OffTerminalNeedsYes(t *testing.T) {
	f, _ := newDreamFixture(t, nil)
	f.newDream(t, "use tabs", "use tabs always")
	_, err := runDreamCmd(t, context.Background(), "apply")
	assert.Eq(t, exitCode(err), core.ExitUsage)
	assert.Eq(t, f.onlyDream(t).State, dream.Pending)

	_, err = runDreamCmd(t, context.Background(), "apply", "--yes")
	assert.NoErr(t, err)
	m := f.onlyDream(t)
	assert.Eq(t, m.State, dream.Applied)
	assert.Contains(t, readText(t, filepath.Join(f.mem, "MEMORY.md")), "use tabs always")
	ev := eventOf(f.sessionEvents(t, m), core.EvDreamApplied)
	if ev == nil || ev["dream_id"] != m.ID {
		t.Fatalf("dream_applied = %v", ev)
	}
}

func TestDreamApply_StaleExit1(t *testing.T) {
	f, _ := newDreamFixture(t, nil)
	f.newDream(t, "use tabs", "use tabs always")
	writeFile(t, filepath.Join(f.mem, "tabs.md"), dreamNote+"more\n")
	_, err := runDreamCmd(t, context.Background(), "apply", "--yes")
	assert.Eq(t, exitCode(err), core.ExitFailure)
	assert.Eq(t, f.onlyDream(t).State, dream.Stale)
}

func TestDreamApply_InvalidIDIsUsage(t *testing.T) {
	newDreamFixture(t, nil)
	_, err := runDreamCmd(t, context.Background(), "apply", "../x", "--yes")
	assert.Eq(t, exitCode(err), core.ExitUsage)
}

func TestDreamApply_NoPendingExit1(t *testing.T) {
	newDreamFixture(t, nil)
	_, err := runDreamCmd(t, context.Background(), "apply", "--yes")
	assert.Eq(t, exitCode(err), core.ExitFailure)
	assert.Contains(t, err.Error(), "no pending dream")
}

func TestDreamUndo_DefaultsToApplied(t *testing.T) {
	f, _ := newDreamFixture(t, nil)
	first := f.newDream(t, "use tabs", "use tabs always")
	_, err := runDreamCmd(t, context.Background(), "apply", "--yes")
	assert.NoErr(t, err)
	second := f.newDream(t, "use tabs always", "use tabs, never spaces")
	_, err = runDreamCmd(t, context.Background(), "undo")
	assert.NoErr(t, err)
	got, err := dream.Load(f.dreams, first.ID)
	assert.NoErr(t, err)
	assert.Eq(t, got.State, dream.Undone)
	got, err = dream.Load(f.dreams, second.ID)
	assert.NoErr(t, err)
	assert.Eq(t, got.State, dream.Pending)
	assert.Contains(t, readText(t, filepath.Join(f.mem, "MEMORY.md")), "use tabs\n")
	if eventOf(f.sessionEvents(t, first), core.EvDreamUndone) == nil {
		t.Fatal("no dream_undone event")
	}
}

func TestDreamDiscard_ThenPlainRunSkipsWindow(t *testing.T) {
	f, _ := newDreamFixture(t, nil)
	m := f.newDream(t, "use tabs", "use tabs always")
	_, err := runDreamCmd(t, context.Background(), "discard")
	assert.NoErr(t, err)
	got, err := dream.Load(f.dreams, m.ID)
	assert.NoErr(t, err)
	assert.Eq(t, got.State, dream.Discarded)
	if eventOf(f.sessionEvents(t, m), core.EvDreamDiscarded) == nil {
		t.Fatal("no dream_discarded event")
	}
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "done"}})
	t.Setenv("JAS_BASE_URL", url)
	_, err = runDreamCmd(t, context.Background())
	assert.NoErr(t, err)
	ms, err := dream.List(f.dreams)
	assert.NoErr(t, err)
	assert.Eq(t, len(ms), 2)
	for _, r := range ms {
		if r.ID != m.ID { // ids made in one second sort by their random suffix
			assert.Eq(t, strings.Join(r.Window.Sessions, ","), "")
		}
	}
}
