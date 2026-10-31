package core

import (
	"encoding/json"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func shellArgs(cmd string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return b
}

func TestGrants_ShellFirstTwoWords(t *testing.T) {
	g := NewGrants(false)
	grant, ok := g.Add("shell", shellArgs("go test ./..."))
	assert.Eq(t, ok, true)
	assert.Eq(t, grant, "shell: go test")
	assert.Eq(t, g.Covers("shell", shellArgs("go test -run X ./core")), true)
	assert.Eq(t, g.Covers("shell", shellArgs("go testx")), false)
	assert.Eq(t, g.Covers("shell", shellArgs("go build")), false)
	assert.Eq(t, g.Covers("shell", shellArgs("go test ./... | tee out")), false)
	assert.Eq(t, g.Covers("shell", shellArgs("go")), false)
	assert.Eq(t, g.Covers("edit", shellArgs("go test")), false)
}

// A grant needs a program and a subcommand: a lone word would cover every
// argument, and a flag or path second word is not a subcommand.
func TestGrants_NeedsSubcommand(t *testing.T) {
	for _, c := range []string{"make", "go ./...", "git -c alias.x=!rm x", "ls -la", "cat ~/.ssh/id", "go ../x"} {
		_, ok := NewGrants(false).Add("shell", shellArgs(c))
		if ok {
			t.Errorf("%q got a grant", c)
		}
	}
	g := NewGrants(false)
	g.Add("shell", shellArgs("make test"))
	assert.Eq(t, g.Covers("shell", shellArgs("make")), false)
}

// Interpreters, wrappers and destructive commands run whatever their
// arguments say, so no grant may cover them.
func TestGrants_RiskyProgramsNeverGrant(t *testing.T) {
	cmds := []string{"find . -delete", "rm -rf x", `python -c "import os"`, "python3 -c x",
		"python3.12 -m pip", "sh -c x", "bash script.sh", "zsh x", "env go test", "sudo make install",
		"xargs rm x", "npx foo", "node -e x", "perl -e x", "ruby -e x", "./run.sh test",
		"/bin/sh -c x", "timeout 5 rm", "nohup rm x", "eval x y", "exec rm x", "pwsh -c x",
		"powershell -c x", "cmd /c x"}
	for _, win := range []bool{false, true} {
		for _, c := range cmds {
			if _, ok := NewGrants(win).Add("shell", shellArgs(c)); ok {
				t.Errorf("windows=%v: %q got a grant", win, c)
			}
		}
	}
	for _, c := range []string{"Python.exe -c x", "BASH.EXE x", `C:\Windows\cmd.exe /c x`} {
		if _, ok := NewGrants(true).Add("shell", shellArgs(c)); ok {
			t.Errorf("windows: %q got a grant", c)
		}
	}
}

func TestGrants_EverydayCommandsStillGrant(t *testing.T) {
	for _, c := range []string{"go test ./...", "git commit -m x", "npm run build", "make test",
		"cargo build --release", "echo hi", "go mod tidy", "golangci-lint run"} {
		if _, ok := NewGrants(false).Add("shell", shellArgs(c)); !ok {
			t.Errorf("%q got no grant", c)
		}
	}
}

func TestGrants_NotSimpleNeverGrants(t *testing.T) {
	cmds := []string{"a;b", "a && b", "a | b", "echo $(x)", "echo `x`", "a > f", "a < f", "a\nb", "FOO=bar go test", "echo 'x", "", "  "}
	for _, c := range cmds {
		g := NewGrants(false)
		_, ok := g.Add("shell", shellArgs(c))
		assert.Eq(t, ok, false)
		assert.Eq(t, g.Covers("shell", shellArgs(c)), false)
		assert.Eq(t, len(g.List()), 0)
	}
	// A granted prefix still doesn't cover a non-simple continuation.
	g := NewGrants(false)
	g.Add("shell", shellArgs("go test"))
	assert.Eq(t, g.Covers("shell", shellArgs("go test\nrm -rf x")), false)
	assert.Eq(t, g.Covers("shell", shellArgs("go test $(x)")), false)
	_, ok := g.Add("shell", json.RawMessage(`not json`))
	assert.Eq(t, ok, false)
	assert.Eq(t, g.Covers("shell", json.RawMessage(`not json`)), false)
}

func TestGrants_WindowsExtras(t *testing.T) {
	for _, c := range []string{"echo hi ^x", "echo hi %PATH%"} {
		_, ok := NewGrants(true).Add("shell", shellArgs(c))
		assert.Eq(t, ok, false)
		_, ok = NewGrants(false).Add("shell", shellArgs(c))
		assert.Eq(t, ok, true)
	}
	w := NewGrants(true)
	w.Add("shell", shellArgs("echo hi"))
	assert.Eq(t, w.Covers("shell", shellArgs("echo %X%")), false)
}

func TestGrants_EditWholeTool(t *testing.T) {
	g := NewGrants(false)
	grant, ok := g.Add("edit", json.RawMessage(`{"path":"a"}`))
	assert.Eq(t, ok, true)
	assert.Eq(t, grant, "edit")
	assert.Eq(t, g.Covers("edit", json.RawMessage(`{"path":"zzz"}`)), true)
	assert.Eq(t, g.Covers("write", json.RawMessage(`{}`)), false)
	assert.Eq(t, len(g.List()), 1)
	assert.Eq(t, g.List()[0], "edit")
	assert.Eq(t, g.Remove("edit"), true)
	assert.Eq(t, g.Remove("edit"), false)
	assert.Eq(t, g.Covers("edit", json.RawMessage(`{}`)), false)
}

func TestGrants_OtherToolsNotGrantable(t *testing.T) {
	g := NewGrants(false)
	for _, name := range []string{"read", "ls", "grep", "bogus"} {
		_, ok := g.Add(name, json.RawMessage(`{}`))
		assert.Eq(t, ok, false)
		assert.Eq(t, g.Covers(name, json.RawMessage(`{}`)), false)
	}
}

func TestGrants_ListSortedAndRemoveShell(t *testing.T) {
	g := NewGrants(false)
	g.Add("shell", shellArgs("go test ./..."))
	g.Add("edit", json.RawMessage(`{}`))
	got := g.List()
	assert.Eq(t, len(got), 2)
	assert.Eq(t, got[0], "edit")
	assert.Eq(t, got[1], "shell: go test")
	assert.Eq(t, g.Remove("shell: go test"), true)
	assert.Eq(t, g.Covers("shell", shellArgs("go test")), false)
}

func TestGrants_Concurrent(t *testing.T) {
	g := NewGrants(false)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				g.Add("shell", shellArgs("go test"))
				g.Covers("shell", shellArgs("go test x"))
				g.List()
				g.Remove("shell: go test")
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

func TestGrants_LiteralKeyWords(t *testing.T) {
	for _, win := range []bool{false, true} {
		for _, c := range []string{`g\o test`, `'go' test`, `"go" test`, `go "test"`, `go te\st`, "go\rtest x"} {
			g := NewGrants(win)
			_, ok := g.Add("shell", shellArgs(c))
			assert.Eq(t, ok, false)
			base := NewGrants(win)
			base.Add("shell", shellArgs("go test"))
			assert.Eq(t, base.Covers("shell", shellArgs(c)), false)
		}
		// Quoting after the key words is fine.
		g := NewGrants(win)
		grant, ok := g.Add("shell", shellArgs(`git commit -m "a b"`))
		assert.Eq(t, ok, true)
		assert.Eq(t, grant, "shell: git commit")
	}
}

func TestGrants_DuplicateKeysRejected(t *testing.T) {
	g := NewGrants(false)
	g.Add("shell", shellArgs("go test"))
	g.Add("edit", json.RawMessage(`{}`))
	dup := json.RawMessage(`{"command":"go test","COMMAND":"rm -rf x"}`)
	assert.Eq(t, g.Covers("shell", dup), false)
	_, ok := NewGrants(false).Add("shell", dup)
	assert.Eq(t, ok, false)
	dupEdit := json.RawMessage(`{"path":"a","Path":"b"}`)
	assert.Eq(t, g.Covers("edit", dupEdit), false)
	_, ok = NewGrants(false).Add("edit", dupEdit)
	assert.Eq(t, ok, false)
}

func TestGrants_NULInWordCannotForgeKey(t *testing.T) {
	g := NewGrants(false)
	_, ok := g.Add("shell", shellArgs("rm\u0000-rf"))
	assert.Eq(t, ok, false)
	assert.Eq(t, g.Covers("shell", shellArgs("rm -rf /")), false)
	g.Add("shell", shellArgs("go test"))
	assert.Eq(t, g.Covers("shell", shellArgs("rm -rf /")), false)
}

func TestGrants_ControlCharsNotGrantable(t *testing.T) {
	ctrl := []rune{0x7f}
	for r := rune(0); r < 0x20; r++ {
		ctrl = append(ctrl, r)
	}
	for _, win := range []bool{false, true} {
		for _, r := range ctrl {
			c := "go" + string(r) + "test"
			_, ok := NewGrants(win).Add("shell", shellArgs(c))
			assert.Eq(t, ok, false)
			base := NewGrants(win)
			base.Add("shell", shellArgs("go test"))
			assert.Eq(t, base.Covers("shell", shellArgs("go test "+string(r))), false)
		}
	}
}
