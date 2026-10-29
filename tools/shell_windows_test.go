//go:build windows

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonli0226/jas/internal/assert"
)

func winShellArgs(t *testing.T, cmd string, timeout int) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"command": cmd, "timeout_seconds": timeout})
	assert.NoErr(t, err)
	return string(b)
}

func TestShellWindows_QuotedArgumentsReachProgram(t *testing.T) {
	p := policy(t)
	write(t, p, "log.txt", "fix bug\r\nother\r\n")
	r := call(t, Shell{}, p, winShellArgs(t, `findstr /c:"fix bug" log.txt`, 0))
	assert.Eq(t, r.IsError, false)
	assert.Eq(t, strings.TrimSpace(r.Output), "fix bug")
}

func TestShellWindows_InheritsPathCaseInsensitively(t *testing.T) {
	t.Setenv("JAS_API_KEY", "sk-test")
	p := policy(t)
	r := call(t, Shell{}, p, winShellArgs(t, `echo path=%PATH% key=%JAS_API_KEY%`, 0))
	out := strings.TrimSpace(r.Output)
	assert.NotContains(t, out, "path=%PATH%")
	assert.Contains(t, out, "key=%JAS_API_KEY%")
}

func TestShellWindows_BackgroundChildDiesWithCall(t *testing.T) {
	p := policy(t)
	marker := filepath.Join(p.Root, "marker.txt")
	r := call(t, Shell{}, p, winShellArgs(t, `start /b cmd /c "ping -n 4 127.0.0.1 >nul & echo late> marker.txt"`, 0))
	assert.Eq(t, r.IsError, false)
	time.Sleep(6 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("background child outlived the shell call")
	}
}

func TestShellWindows_CancelKillsGrandchildren(t *testing.T) {
	p := policy(t)
	started := filepath.Join(p.Root, "started.txt")
	marker := filepath.Join(p.Root, "marker.txt")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Shell{}.Call(ctx, p, json.RawMessage(winShellArgs(t,
			`start /b cmd /c "echo up> started.txt & ping -n 6 127.0.0.1 >nul & echo late> marker.txt" & ping -n 30 127.0.0.1 >nul`, 0)))
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("grandchild never started")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		assert.Eq(t, err != nil, true)
	case <-time.After(5 * time.Second):
		t.Fatal("Call did not return after cancel")
	}
	time.Sleep(8 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("grandchild outlived a cancelled shell call")
	}
}

func TestShellWindows_TimeoutKillsTree(t *testing.T) {
	p := policy(t)
	start := time.Now()
	r := call(t, Shell{}, p, winShellArgs(t, `ping -n 30 127.0.0.1`, 1))
	assert.Eq(t, r.IsError, true)
	assert.Contains(t, r.Output, "[timed out")
	if time.Since(start) > 10*time.Second {
		t.Fatalf("timeout took %s", time.Since(start))
	}
}

func TestWriteWindows_ReportsSuccess(t *testing.T) {
	p := policy(t)
	r := call(t, Write{}, p, `{"path":"a/b.txt","content":"hi"}`)
	assert.Eq(t, r.IsError, false)
	b, err := os.ReadFile(filepath.Join(p.Root, "a", "b.txt"))
	assert.NoErr(t, err)
	assert.Eq(t, string(b), "hi")
}
