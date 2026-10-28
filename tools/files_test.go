package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func policy(t *testing.T) core.ExecPolicy {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	assert.NoErr(t, err)
	return core.ExecPolicy{Root: root}
}

func write(t *testing.T, p core.ExecPolicy, rel, content string) {
	t.Helper()
	full := filepath.Join(p.Root, rel)
	assert.NoErr(t, os.MkdirAll(filepath.Dir(full), 0o755))
	assert.NoErr(t, os.WriteFile(full, []byte(content), 0o644))
}

func call(t *testing.T, tl core.Tool, p core.ExecPolicy, args string) core.Result {
	t.Helper()
	r, err := tl.Call(context.Background(), p, json.RawMessage(args))
	assert.NoErr(t, err)
	return r
}

func TestRead_OffsetLimit(t *testing.T) {
	p := policy(t)
	var sb strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&sb, "line%d\n", i)
	}
	write(t, p, "f.txt", sb.String())
	r := call(t, Read{}, p, `{"path":"f.txt","offset":3,"limit":2}`)
	assert.Eq(t, r.IsError, false)
	assert.Eq(t, strings.TrimRight(r.Output, "\n"), "line3\nline4")
}

func TestRead_Errors(t *testing.T) {
	p := policy(t)
	r := call(t, Read{}, p, `{"path":"missing.txt"}`)
	assert.Eq(t, r.IsError, true)
	r = call(t, Read{}, p, `{"path":"../x"}`)
	assert.Eq(t, r.IsError, true)
	assert.Contains(t, r.Output, "outside project root")
	r = call(t, Read{}, p, `not json`)
	assert.Eq(t, r.IsError, true)
}

func TestRead_CapsOutput(t *testing.T) {
	p := policy(t)
	p.MaxOutput = 10
	write(t, p, "f.txt", "aaaa\nbbbb\ncccc\n")
	r := call(t, Read{}, p, `{"path":"f.txt"}`)
	assert.Contains(t, r.Output, "[truncated:")
}

func TestLs_SortedDirsSuffixed(t *testing.T) {
	p := policy(t)
	write(t, p, "b.txt", "")
	write(t, p, "a/x", "")
	r := call(t, Ls{}, p, `{}`)
	assert.Eq(t, r.Output, "a/\nb.txt")
	r = call(t, Ls{}, p, `{"path":"nope"}`)
	assert.Eq(t, r.IsError, true)
}

func TestReadOnly_Tools(t *testing.T) {
	var names []string
	for _, tl := range ReadOnly() {
		names = append(names, tl.Name())
		assert.Eq(t, tl.Kind(), core.ReadOnly)
		var s map[string]any
		assert.NoErr(t, json.Unmarshal(tl.Schema(), &s))
		if s["properties"] == nil || s["required"] == nil || tl.Description() == "" {
			t.Fatalf("%s: incomplete schema/description", tl.Name())
		}
	}
	assert.Eq(t, strings.Join(names, ","), "read,ls,grep")
}

func TestEdit_ExactlyOnce(t *testing.T) {
	p := policy(t)
	write(t, p, "a.txt", "hello world\n")
	r := call(t, Edit{}, p, `{"path":"a.txt","old_string":"world","new_string":"go"}`)
	assert.Eq(t, r.IsError, false)
	assert.Eq(t, r.Output, "edited a.txt")
	b, err := os.ReadFile(filepath.Join(p.Root, "a.txt"))
	assert.NoErr(t, err)
	assert.Eq(t, string(b), "hello go\n")
}

func TestEdit_NotFound(t *testing.T) {
	p := policy(t)
	write(t, p, "a.txt", "hello\n")
	r := call(t, Edit{}, p, `{"path":"a.txt","old_string":"nope","new_string":"x"}`)
	assert.Eq(t, r.IsError, true)
	assert.Eq(t, r.Output, "old_string not found in a.txt")
}

func TestEdit_Ambiguous(t *testing.T) {
	p := policy(t)
	write(t, p, "a.txt", "x\nx\n")
	r := call(t, Edit{}, p, `{"path":"a.txt","old_string":"x","new_string":"y"}`)
	assert.Eq(t, r.IsError, true)
	assert.Eq(t, r.Output, "old_string matches 2 times in a.txt; include more surrounding context")
	b, _ := os.ReadFile(filepath.Join(p.Root, "a.txt"))
	assert.Eq(t, string(b), "x\nx\n")
}

func TestEdit_CRLFPreserved(t *testing.T) {
	p := policy(t)
	write(t, p, "a.txt", "one\r\ntwo\r\nthree\r\n")
	r := call(t, Edit{}, p, `{"path":"a.txt","old_string":"one\ntwo","new_string":"x"}`)
	assert.Eq(t, r.IsError, true)
	assert.Contains(t, r.Output, "not found")
	r = call(t, Edit{}, p, `{"path":"a.txt","old_string":"one\r\ntwo","new_string":"1\r\n2"}`)
	assert.Eq(t, r.IsError, false)
	b, _ := os.ReadFile(filepath.Join(p.Root, "a.txt"))
	assert.Eq(t, string(b), "1\r\n2\r\nthree\r\n")
}

func TestEdit_EmptyOldString(t *testing.T) {
	p := policy(t)
	write(t, p, "a.txt", "hi")
	r := call(t, Edit{}, p, `{"path":"a.txt","old_string":"","new_string":"x"}`)
	assert.Eq(t, r.IsError, true)
}

func TestWrite_CreatesDirsAndModes(t *testing.T) {
	p := policy(t)
	r := call(t, Write{}, p, `{"path":"d/e/new.txt","content":"hi"}`)
	assert.Eq(t, r.IsError, false)
	info, err := os.Stat(filepath.Join(p.Root, "d/e/new.txt"))
	assert.NoErr(t, err)
	assert.Eq(t, info.Mode().Perm(), os.FileMode(0o644))
	di, err := os.Stat(filepath.Join(p.Root, "d/e"))
	assert.NoErr(t, err)
	assert.Eq(t, di.Mode().Perm(), os.FileMode(0o755))

	assert.NoErr(t, os.Chmod(filepath.Join(p.Root, "d/e/new.txt"), 0o755))
	call(t, Write{}, p, `{"path":"d/e/new.txt","content":"changed"}`)
	info, _ = os.Stat(filepath.Join(p.Root, "d/e/new.txt"))
	assert.Eq(t, info.Mode().Perm(), os.FileMode(0o755))
	b, _ := os.ReadFile(filepath.Join(p.Root, "d/e/new.txt"))
	assert.Eq(t, string(b), "changed")
}

func TestWrite_ThroughSymlinkKeepsLink(t *testing.T) {
	p := policy(t)
	write(t, p, "real.txt", "old")
	assert.NoErr(t, os.Symlink("real.txt", filepath.Join(p.Root, "link.txt")))
	r := call(t, Write{}, p, `{"path":"link.txt","content":"new"}`)
	assert.Eq(t, r.IsError, false)
	fi, err := os.Lstat(filepath.Join(p.Root, "link.txt"))
	assert.NoErr(t, err)
	assert.Eq(t, fi.Mode()&os.ModeSymlink != 0, true)
	b, _ := os.ReadFile(filepath.Join(p.Root, "real.txt"))
	assert.Eq(t, string(b), "new")
}

func TestWrite_AtomicNoTornFile(t *testing.T) {
	p := policy(t)
	write(t, p, "a.txt", "original")
	orig := renameFile
	renameFile = func(string, string) error { return fmt.Errorf("injected rename failure") }
	defer func() { renameFile = orig }()
	r := call(t, Write{}, p, `{"path":"a.txt","content":"replacement"}`)
	assert.Eq(t, r.IsError, true)
	assert.Contains(t, r.Output, "injected rename failure")
	b, _ := os.ReadFile(filepath.Join(p.Root, "a.txt"))
	assert.Eq(t, string(b), "original")
	entries, err := os.ReadDir(p.Root)
	assert.NoErr(t, err)
	assert.Eq(t, len(entries), 1)
}

func TestWrite_RefusesOutsideRoot(t *testing.T) {
	p := policy(t)
	outside := filepath.Join(filepath.Dir(p.Root), "escape.txt")
	r := call(t, Write{}, p, fmt.Sprintf(`{"path":%q,"content":"x"}`, outside))
	assert.Eq(t, r.IsError, true)
	assert.Contains(t, r.Output, "outside project root")
	_, err := os.Stat(outside)
	assert.Eq(t, os.IsNotExist(err), true)
	r = call(t, Edit{}, p, `{"path":"../escape.txt","old_string":"a","new_string":"b"}`)
	assert.Eq(t, r.IsError, true)
}
