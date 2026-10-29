package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
)

func appendN(t *testing.T, p string, n int) {
	t.Helper()
	l, err := OpenEventLog(p, nil, nil)
	assert.NoErr(t, err)
	for i := 0; i < n; i++ {
		assert.NoErr(t, l.Append(EvUsage, map[string]any{"n": i}))
	}
	assert.NoErr(t, l.Close())
}

func TestOpenEventLog_ReopenContinuesIDs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "e.jsonl")
	appendN(t, p, 2)
	appendN(t, p, 1)
	evs := testutil.ReadEvents(t, p)
	assert.Eq(t, len(evs), 3)
	for i, e := range evs {
		want := []string{"e000001", "e000002", "e000003"}[i]
		assert.Eq(t, e["id"].(string), want)
		assert.Eq(t, e["seq"].(float64), float64(i+1))
	}
}

func TestOpenEventLog_ParentIDChain(t *testing.T) {
	p := filepath.Join(t.TempDir(), "e.jsonl")
	appendN(t, p, 2)
	appendN(t, p, 2)
	evs := testutil.ReadEvents(t, p)
	if v, ok := evs[0]["parent_id"]; !ok || v != nil {
		t.Fatalf("first parent_id = %v, %v", v, ok)
	}
	for i := 1; i < len(evs); i++ {
		assert.Eq(t, evs[i]["parent_id"].(string), evs[i-1]["id"].(string))
	}
}

func TestOpenEventLog_ReopenSkipsTruncatedLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "e.jsonl")
	body := `{"v":1,"id":"e000001","parent_id":null,"seq":1,"type":"usage"}` + "\n" +
		`{"v":1,"id":"e000002","parent_id":"e000001","seq":2,"type":"usage"}` + "\n" +
		`{"v":1,"id":"e000003"`
	assert.NoErr(t, os.WriteFile(p, []byte(body), 0o600))
	appendN(t, p, 1)
	raw, err := os.ReadFile(p)
	assert.NoErr(t, err)
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	assert.Eq(t, len(lines), 4)
	assert.Eq(t, lines[2], `{"v":1,"id":"e000003"`)
	assert.Contains(t, lines[3], `"id":"e000003"`)
	assert.Contains(t, lines[3], `"parent_id":"e000002"`)
	assert.Contains(t, lines[3], `"seq":3`)
}

func TestOpenEventLog_SecondOpenLocked(t *testing.T) {
	p := filepath.Join(t.TempDir(), "e.jsonl")
	l, err := OpenEventLog(p, nil, nil)
	assert.NoErr(t, err)
	_, err = OpenEventLog(p, nil, nil)
	if !errors.Is(err, ErrSessionLocked) {
		t.Fatalf("err = %v, want ErrSessionLocked", err)
	}
	assert.NoErr(t, l.Close())
	l2, err := OpenEventLog(p, nil, nil)
	assert.NoErr(t, err)
	assert.NoErr(t, l2.Close())
}
