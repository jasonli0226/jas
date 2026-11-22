package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
)

// syncBuf is a bytes.Buffer safe to read while attach writes to it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func farmDir(home, id string) string { return filepath.Join(home, farmsDir, id) }

// fixtureFarm writes a farm dir with a manifest for parts (nil: no manifest).
func fixtureFarm(t *testing.T, home, id string, parts []string) {
	t.Helper()
	dir := farmDir(home, id)
	if err := os.MkdirAll(filepath.Join(dir, "parts"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeAppend(t, filepath.Join(dir, "farm.jsonl"), "")
	if parts == nil {
		return
	}
	m := farm.Manifest{FarmID: id, Parts: map[string]*farm.PartState{}}
	for _, p := range parts {
		m.Parts[p] = &farm.PartState{Role: "coder", Status: "running"}
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, farmManifestFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func lockFixture(t *testing.T, home, id string) func() error {
	t.Helper()
	release, err := farm.LockFarm(home, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = release() })
	return release
}

func writeAppend(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func evLine(typ string, kv ...any) string {
	m := map[string]any{"type": typ}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	return string(b) + "\n"
}

// attachAsync runs attach in the background and returns its output and a
// wait for its error.
func attachAsync(t *testing.T, home string, args ...string) (*syncBuf, func() error) {
	t.Helper()
	out := &syncBuf{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- runAttach(ctx, home, args, out, attachPoll) }()
	return out, func() error {
		select {
		case err := <-done:
			return err
		case <-time.After(3 * time.Second):
			t.Fatalf("attach did not return; output:\n%s", out.String())
			return nil
		}
	}
}

const attachPoll = 10 * time.Millisecond

func waitOutput(t *testing.T, out *syncBuf, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("output lacks %q:\n%s", want, out.String())
}

func TestAttach_NoneRunning(t *testing.T) {
	home := t.TempDir()
	fixtureFarm(t, home, "20261122T010000-aa1", []string{"p1"})
	var out bytes.Buffer
	err := runAttach(context.Background(), home, nil, &out, attachPoll)
	if err != nil || !strings.Contains(out.String(), "no farm is running") {
		t.Fatalf("err %v, output %q", err, out.String())
	}
}

func TestAttach_SeveralRunning(t *testing.T) {
	home := t.TempDir()
	for _, id := range []string{"20261122T010000-aa1", "20261122T020000-bb2"} {
		fixtureFarm(t, home, id, []string{"p1"})
		lockFixture(t, home, id)
	}
	err := runAttach(context.Background(), home, nil, &bytes.Buffer{}, attachPoll)
	if exitCodeOf(err) != core.ExitUsage || !strings.Contains(err.Error(), "aa1") || !strings.Contains(err.Error(), "bb2") {
		t.Fatalf("err %v", err)
	}
}

func TestAttach_AmbiguousPrefix(t *testing.T) {
	home := t.TempDir()
	fixtureFarm(t, home, "20261122-aa1", []string{"p1"})
	fixtureFarm(t, home, "20261122-aa2", []string{"p1"})
	err := runAttach(context.Background(), home, []string{"20261122-aa"}, &bytes.Buffer{}, attachPoll)
	if exitCodeOf(err) != core.ExitUsage || !strings.Contains(err.Error(), "20261122-aa1") || !strings.Contains(err.Error(), "20261122-aa2") {
		t.Fatalf("err %v", err)
	}
}

func TestAttach_UnknownFarm(t *testing.T) {
	home := t.TempDir()
	fixtureFarm(t, home, "20261122-aa1", []string{"p1"})
	err := runAttach(context.Background(), home, []string{"zz"}, &bytes.Buffer{}, attachPoll)
	if exitCodeOf(err) != core.ExitUsage {
		t.Fatalf("err %v", err)
	}
}

func TestAttach_EndedFarm(t *testing.T) {
	home := t.TempDir()
	id := "20261122-aa1"
	fixtureFarm(t, home, id, []string{"p1"})
	writeAppend(t, filepath.Join(farmDir(home, id), "farm.jsonl"), evLine("farm_ended", "reason", "ok"))
	var out bytes.Buffer
	err := runAttach(context.Background(), home, []string{"20261122-aa"}, &out, attachPoll)
	if err != nil || !strings.Contains(out.String(), "farm "+id) || !strings.Contains(out.String(), "farm ended: ok") {
		t.Fatalf("err %v, output:\n%s", err, out.String())
	}
}

func TestAttach_DeadFarm(t *testing.T) {
	home := t.TempDir()
	id := "20261122-aa1"
	fixtureFarm(t, home, id, []string{"p1"})
	var out bytes.Buffer
	err := runAttach(context.Background(), home, []string{id}, &out, attachPoll)
	if err != nil || !strings.Contains(out.String(), "farm process ended without finishing") {
		t.Fatalf("err %v, output:\n%s", err, out.String())
	}
}

func TestAttach_StartingFarm(t *testing.T) {
	home := t.TempDir()
	id := "20261122-aa1"
	fixtureFarm(t, home, id, nil)
	release := lockFixture(t, home, id)
	out, wait := attachAsync(t, home)
	waitOutput(t, out, "farm "+id+" is starting")
	_ = release()
	if err := wait(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "farm process ended") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestAttach_FollowsRunning(t *testing.T) {
	home := t.TempDir()
	id := "20261122-aa1"
	fixtureFarm(t, home, id, []string{"p1"})
	lockFixture(t, home, id)
	out, wait := attachAsync(t, home, id)
	waitOutput(t, out, "farm "+id)
	writeAppend(t, filepath.Join(farmDir(home, id), "parts", "p1", "events.jsonl"),
		evLine("tool_started", "tool", "read", "args", map[string]any{"path": "main.go"}))
	waitOutput(t, out, "main.go")
	writeAppend(t, filepath.Join(farmDir(home, id), "farm.jsonl"), evLine("farm_ended", "reason", "ok", "exit_code", 0))
	if err := wait(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "farm ended: ok") {
		t.Fatalf("no end line when the farm ended while attached:\n%s", out.String())
	}
}

func TestAttach_PartDetail(t *testing.T) {
	home := t.TempDir()
	id := "20261122-aa1"
	fixtureFarm(t, home, id, []string{"p1", "p2"})
	lockFixture(t, home, id)
	out, wait := attachAsync(t, home, id, "p1")
	waitOutput(t, out, "farm "+id)
	parts := filepath.Join(farmDir(home, id), "parts")
	writeAppend(t, filepath.Join(parts, "p2", "events.jsonl"), evLine("assistant_message", "text", "from p2"))
	writeAppend(t, filepath.Join(parts, "p1", "events.jsonl"), evLine("assistant_message", "text", "hello from p1"))
	waitOutput(t, out, "[p1] says: hello from p1")
	writeAppend(t, filepath.Join(farmDir(home, id), "farm.jsonl"), evLine("farm_ended", "reason", "ok"))
	if err := wait(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "from p2") {
		t.Fatalf("detail for another part printed:\n%s", out.String())
	}
}

func TestAttach_UnknownPart(t *testing.T) {
	home := t.TempDir()
	id := "20261122-aa1"
	fixtureFarm(t, home, id, []string{"p1"})
	err := runAttach(context.Background(), home, []string{id, "nope"}, &bytes.Buffer{}, attachPoll)
	if exitCodeOf(err) != core.ExitUsage {
		t.Fatalf("err %v", err)
	}
}

// A farm whose process releases its lock right after farm_ended (already on
// disk at attach) is reported as ended, not as died.
func TestAttach_EndedJustBeforeAttach(t *testing.T) {
	home := t.TempDir()
	id := "20261122-aa1"
	fixtureFarm(t, home, id, []string{"p1"})
	writeAppend(t, filepath.Join(farmDir(home, id), "farm.jsonl"), evLine("farm_ended", "reason", "ok"))
	release := lockFixture(t, home, id)
	out, wait := attachAsync(t, home, id)
	waitOutput(t, out, "farm "+id)
	_ = release()
	if err := wait(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "farm ended: ok") || strings.Contains(out.String(), "farm process ended") {
		t.Fatalf("output:\n%s", out.String())
	}
}
