package farm

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
)

func sampleManifest() *Manifest {
	return &Manifest{
		FarmID: NewFarmID(time.Now()), PlanSHA256: "abc", Base: "main",
		Parts: map[string]*PartState{
			"a": {Role: "coder", Branch: "b", Status: StatusDone, ExitCode: 0, Tokens: 5, Undelivered: []int{1, 2}},
		},
		Integrator:  &PartState{Role: "coder", Status: StatusRunning},
		Check:       &core.CheckResult{ExitCode: 1, Duration: 1500 * time.Millisecond, Output: "x", TimedOut: true},
		FinalChoice: "keep", Reason: "ok", TotalTokens: 9,
	}
}

func TestManifest_AtomicSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	m := sampleManifest()
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&got, m) {
		t.Fatalf("mismatch:\n got %+v\nwant %+v", got, m)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			m.Update(func(m *Manifest) { m.TotalTokens = i })
			if err := m.Save(path); err != nil {
				t.Error(err)
			}
		}(i)
		go func() {
			defer wg.Done()
			if _, err := LoadManifest(path); err != nil {
				t.Errorf("partial file: %v", err)
			}
		}()
	}
	wg.Wait()
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("leftover temp files: %v", ents)
	}
}

func TestFarmEvents_JSONFields(t *testing.T) {
	p := FarmEnded(6, "check_failed", map[string]any{"tokens": 3})
	if p["exit_code"] != 6 || p["reason"] != "check_failed" || p["tokens"] != 3 {
		t.Fatalf("payload %v", p)
	}
	if EvCheckRan != core.EvCheckRan || EvFarmEnded != "farm_ended" {
		t.Fatal("constants")
	}
	if p := FarmEnded(0, "ok", nil); p["exit_code"] != 0 || p["reason"] != "ok" {
		t.Fatalf("nil totals: %v", p)
	}
}
