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
