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

func TestGrants_OneWord(t *testing.T) {
	g := NewGrants(false)
	grant, ok := g.Add("shell", shellArgs("make"))
	assert.Eq(t, ok, true)
	assert.Eq(t, grant, "shell: make")
	assert.Eq(t, g.Covers("shell", shellArgs("make")), true)
	assert.Eq(t, g.Covers("shell", shellArgs("make all")), false)

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
	for _, c := range []string{"echo ^x", "echo %PATH%"} {
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
