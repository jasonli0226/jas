package farm

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
)

func gatedRig(t *testing.T) *runRig {
	t.Helper()
	parts := []Part{{ID: "a", Role: "gated", Goal: "a", Owns: []string{"a/**"}}}
	r := newRunRig(t, "true", parts, func(WorkerSession) []fake.Turn {
		return []fake.Turn{call("shell", `{"command":"echo hi"}`), final("a done")}
	})
	r.opts.Cfg.Roles = map[string]RoleDef{"gated": {Tools: builtinTools(), Approve: "shell"}}
	return r
}

func farmTypes(r *runRig) []string {
	var out []string
	for _, e := range r.events("") {
		out = append(out, e["type"].(string))
	}
	return out
}

func TestFarm_GatedCallNoClientUnchanged(t *testing.T) {
	old := gatedRig(t)
	old.run(context.Background())
	want := farmTypes(old)

	r := gatedRig(t)
	r.opts.Broker = NewBroker(BrokerOptions{Level: core.Never})
	r.run(context.Background())
	if got := farmTypes(r); !slices.Equal(got, want) {
		t.Fatalf("farm.jsonl types %v, want %v", got, want)
	}
	if ps := r.last(EvPartEnded); ps["part"] != "a" {
		t.Fatalf("part_ended %v", ps)
	}
	var types []string
	var closed map[string]any
	for _, e := range testutil.ReadEvents(t, filepath.Join(r.farmDir(), "parts", "a", "events.jsonl")) {
		typ, _ := e["type"].(string)
		types = append(types, typ)
		if typ == EvApprovalClosed {
			closed, _ = e["data"].(map[string]any)
			if closed == nil {
				closed = e
			}
		}
	}
	if !slices.Contains(types, EvApprovalRequested) || closed == nil || closed["by"] != "no_client" {
		t.Fatalf("part log %v closed %v", types, closed)
	}
}

func TestFarm_BrokerAnswerReachesPart(t *testing.T) {
	r := gatedRig(t)
	b := NewBroker(BrokerOptions{Level: core.Never})
	b.SetClients(1)
	r.opts.Broker = b
	h, err := StartFarm(context.Background(), r.opts)
	if err != nil {
		t.Fatal(err)
	}
	if h.Broker() != b {
		t.Fatal("Handle.Broker is not the run's broker")
	}
	deadline := time.Now().Add(10 * time.Second)
	var id string
	for id == "" && time.Now().Before(deadline) {
		if p := b.Pending(); len(p) > 0 {
			id = p[0].ID
		}
		time.Sleep(5 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("no pending approval")
	}
	if ok, msg := b.Answer(id, AnswerYes, "test"); !ok {
		t.Fatal(msg)
	}
	h.Wait()
	if ps := h.Snapshot().Parts["a"]; ps.Status != StatusDone {
		t.Fatalf("part a %+v", ps)
	}
}

func TestFarm_SoftStopReleasesHeldApproval(t *testing.T) {
	r := gatedRig(t)
	b := NewBroker(BrokerOptions{Level: core.Never, After: neverAfter})
	b.SetClients(1)
	r.opts.Broker = b
	h, err := StartFarm(context.Background(), r.opts)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(b.Pending()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(b.Pending()) == 0 {
		t.Fatal("no pending approval")
	}
	h.Stop(false)
	done := make(chan struct{})
	go func() { h.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("soft stop did not release the held approval")
	}
	ans, by, ok := b.Closed("a1")
	if !ok || ans != "refused" || by != "stop" {
		t.Fatalf("a1 closed %q by %q (%v)", ans, by, ok)
	}
}
