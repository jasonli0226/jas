package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	assert.Perm(t, info.Mode(), 0o644)
	di, err := os.Stat(filepath.Join(p.Root, "d/e"))
	assert.NoErr(t, err)
	assert.Perm(t, di.Mode(), 0o755)

	assert.NoErr(t, os.Chmod(filepath.Join(p.Root, "d/e/new.txt"), 0o755))
	call(t, Write{}, p, `{"path":"d/e/new.txt","content":"changed"}`)
	info, _ = os.Stat(filepath.Join(p.Root, "d/e/new.txt"))
	assert.Perm(t, info.Mode(), 0o755)
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

func TestEdit_RefusesRedactedMarker(t *testing.T) {
	p := policy(t)
	write(t, p, "a.txt", "key=[REDACTED:assignment]\nother\n")
	write(t, p, "testdata/b.txt", "key=[REDACTED:assignment]\nother\n")
	const want = "region contains redacted content; re-read the file and edit around the redacted value"

	r := call(t, Edit{}, p, `{"path":"a.txt","old_string":"[REDACTED:assignment]","new_string":"x"}`)
	assert.Eq(t, r.IsError, true)
	assert.Eq(t, r.Output, want)
	r = call(t, Edit{}, p, `{"path":"a.txt","old_string":"other","new_string":"[REDACTED:x]"}`)
	assert.Eq(t, r.Output, want)
	r = call(t, Write{}, p, `{"path":"c.txt","content":"a [REDACTED:jwt] b"}`)
	assert.Eq(t, r.Output, want)
	_, err := os.Stat(filepath.Join(p.Root, "c.txt"))
	assert.Eq(t, os.IsNotExist(err), true)

	r = call(t, Edit{}, p, `{"path":"a.txt","old_string":"other","new_string":"fine"}`)
	assert.Eq(t, r.IsError, false)

	p.RedactSkip = []string{"testdata/**"}
	r = call(t, Edit{}, p, `{"path":"testdata/b.txt","old_string":"[REDACTED:assignment]","new_string":"x"}`)
	assert.Eq(t, r.IsError, false)
	r = call(t, Write{}, p, `{"path":"testdata/c.txt","content":"[REDACTED:jwt]"}`)
	assert.Eq(t, r.IsError, false)
}

func TestRead_DeniedByFloor(t *testing.T) {
	p := policy(t)
	write(t, p, ".env", "TOKEN=abc")
	r := call(t, Read{}, p, `{"path":".env"}`)
	assert.Eq(t, r.IsError, true)
	assert.Contains(t, r.Output, "path denied by policy")
	r = call(t, Write{}, p, `{"path":"sub/.env","content":"x"}`)
	assert.Contains(t, r.Output, "path denied by policy")
}

func TestLsGrep_SkipDeniedEntries(t *testing.T) {
	p := policy(t)
	write(t, p, ".env", "needle=1")
	write(t, p, "secrets/a.txt", "needle=2")
	write(t, p, "sub/.env.local", "needle=3")
	write(t, p, "sub/key.pem", "needle=4")
	write(t, p, "ok.txt", "needle=5")
	assert.NoErr(t, os.Symlink(".env", filepath.Join(p.Root, "link")))

	r := call(t, Ls{}, p, `{}`)
	assert.Eq(t, r.Output, "ok.txt\nsub/")
	r = call(t, Ls{}, p, `{"path":"sub"}`)
	assert.Eq(t, r.Output, "")
	r = call(t, Grep{}, p, `{"pattern":"needle"}`)
	assert.Eq(t, r.Output, "ok.txt:1:needle=5")
	r = call(t, Grep{}, p, `{"pattern":"needle","path":"secrets"}`)
	assert.Eq(t, r.IsError, true)
}

func TestRead_OffsetCannotSliceIntoPrivateKey(t *testing.T) {
	p := policy(t)
	write(t, p, "notes.txt", "head\n-----BEGIN RSA PRIVATE KEY-----\nBODYLINE1AAAA\nBODYLINE2BBBB\n-----END RSA PRIVATE KEY-----\ntail\n")
	for _, args := range []string{`{"path":"notes.txt","offset":3}`, `{"path":"notes.txt","offset":4,"limit":1}`, `{"path":"notes.txt","offset":2}`} {
		r := call(t, Read{}, p, args)
		assert.NotContains(t, r.Output, "BODYLINE")
		assert.NotContains(t, r.Output, "END RSA")
	}
}

func TestDeny_NestedSecretsDir(t *testing.T) {
	p := policy(t)
	write(t, p, "sub/secrets/x", "v")
	r := call(t, Read{}, p, `{"path":"sub/secrets/x"}`)
	assert.Contains(t, r.Output, "path denied by policy")
}

func TestFileTools_CancelledContext(t *testing.T) {
	cases := []struct {
		name string
		tool core.Tool
		args string
	}{
		{"read", Read{}, `{"path":"f.txt"}`},
		{"ls", Ls{}, `{}`},
		{"write", Write{}, `{"path":"new.txt","content":"x"}`},
		{"edit", Edit{}, `{"path":"f.txt","old_string":"hello","new_string":"bye"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := policy(t)
			write(t, p, "f.txt", "hello\n")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := tc.tool.Call(ctx, p, json.RawMessage(tc.args))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
			got, rerr := os.ReadFile(filepath.Join(p.Root, "f.txt"))
			assert.NoErr(t, rerr)
			assert.Eq(t, string(got), "hello\n")
			if _, serr := os.Stat(filepath.Join(p.Root, "new.txt")); !os.IsNotExist(serr) {
				t.Fatalf("new.txt exists after cancelled write (stat err %v)", serr)
			}
		})
	}
}

// cancelOnRead cancels its context on the first Read and counts bytes served.
type cancelOnRead struct {
	r      io.Reader
	cancel context.CancelFunc
	n      int
}

func (c *cancelOnRead) Read(b []byte) (int, error) {
	c.cancel()
	n, err := c.r.Read(b)
	c.n += n
	return n, err
}

func TestCopyCtx_StopsBetweenChunks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := &cancelOnRead{r: bytes.NewReader(make([]byte, 4*ioChunk)), cancel: cancel}
	var dst bytes.Buffer
	err := copyCtx(ctx, &dst, src)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if src.n > ioChunk {
		t.Fatalf("read %d bytes after cancel, want at most one chunk (%d)", src.n, ioChunk)
	}
}

func TestEdit_ResultCarriesChange(t *testing.T) {
	p := policy(t)
	write(t, p, "f.txt", "a\nb\nc\n")
	res := call(t, Edit{}, p, `{"path":"f.txt","old_string":"b","new_string":"B"}`)
	assert.Eq(t, res.IsError, false)
	assert.Eq(t, res.Output, "edited f.txt")
	if res.Change == nil {
		t.Fatal("no change")
	}
	assert.Eq(t, res.Change.Path, "f.txt")
	assert.Eq(t, res.Change.Old, "a\nb\nc\n")
	assert.Eq(t, res.Change.New, "a\nB\nc\n")
	assert.Eq(t, res.Change.HideRemoved, false)
}

func TestWrite_NewFileChange(t *testing.T) {
	p := policy(t)
	res := call(t, Write{}, p, `{"path":"n.txt","content":"hi\n"}`)
	if res.Change == nil {
		t.Fatal("no change")
	}
	assert.Eq(t, res.Change.Old, "")
	assert.Eq(t, res.Change.New, "hi\n")
	assert.Eq(t, res.Change.HideRemoved, false)
}

func TestWrite_OverwriteChangeHidesRemoved(t *testing.T) {
	p := policy(t)
	write(t, p, "o.txt", "old\n")
	res := call(t, Write{}, p, `{"path":"o.txt","content":"new\n"}`)
	assert.Eq(t, res.Output, "wrote o.txt (4 bytes)")
	if res.Change == nil {
		t.Fatal("no change")
	}
	assert.Eq(t, res.Change.Old, "old\n")
	assert.Eq(t, res.Change.New, "new\n")
	assert.Eq(t, res.Change.HideRemoved, true)
}

func TestEdit_PreviewDoesNotWrite(t *testing.T) {
	p := policy(t)
	write(t, p, "f.txt", "a\nb\n")
	args := json.RawMessage(`{"path":"f.txt","old_string":"b","new_string":"B"}`)
	ch, err := Edit{}.Preview(context.Background(), p, args)
	assert.NoErr(t, err)
	if ch == nil {
		t.Fatal("no change")
	}
	got, _ := os.ReadFile(filepath.Join(p.Root, "f.txt"))
	assert.Eq(t, string(got), "a\nb\n")
	res := call(t, Edit{}, p, string(args))
	assert.Eq(t, *ch, *res.Change)
}

func TestEdit_PreviewNotFoundIsError(t *testing.T) {
	p := policy(t)
	write(t, p, "f.txt", "a\n")
	ch, err := Edit{}.Preview(context.Background(), p, json.RawMessage(`{"path":"f.txt","old_string":"zzz","new_string":"y"}`))
	if err == nil {
		t.Fatal("want error")
	}
	if ch != nil {
		t.Fatal("want nil change")
	}
}

func TestWrite_PreviewDoesNotWrite(t *testing.T) {
	p := policy(t)
	ch, err := Write{}.Preview(context.Background(), p, json.RawMessage(`{"path":"sub/n.txt","content":"x\n"}`))
	assert.NoErr(t, err)
	if ch == nil || ch.New != "x\n" {
		t.Fatalf("change = %+v", ch)
	}
	if _, err := os.Stat(filepath.Join(p.Root, "sub")); !os.IsNotExist(err) {
		t.Fatalf("preview created files: %v", err)
	}
}
