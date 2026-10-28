package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

const (
	coldStartRuns   = 20
	coldStartBudget = 50 * time.Millisecond
	maxBinarySize   = 15 << 20
)

// TestVersion_ColdStart guards against accidental dependency/init bloat. It is
// gated because timing on shared CI runners is noisy.
func TestVersion_ColdStart(t *testing.T) {
	if os.Getenv("JAS_PERF_TESTS") != "1" {
		t.Skip("set JAS_PERF_TESTS=1 to run timing checks")
	}
	bin := filepath.Join(t.TempDir(), "jas")
	build := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	fi, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() >= maxBinarySize {
		t.Errorf("binary size %d bytes, want < %d", fi.Size(), maxBinarySize)
	}
	durs := make([]time.Duration, coldStartRuns)
	for i := range durs {
		start := time.Now()
		if out, err := exec.Command(bin, "--version").CombinedOutput(); err != nil {
			t.Fatalf("jas --version: %v\n%s", err, out)
		}
		durs[i] = time.Since(start)
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	median := durs[len(durs)/2]
	t.Logf("median %v over %d runs; binary %d bytes", median, coldStartRuns, fi.Size())
	if median >= coldStartBudget {
		t.Errorf("median cold start %v, want < %v", median, coldStartBudget)
	}
}
