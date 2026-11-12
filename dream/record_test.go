package dream

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestNewID_Shape(t *testing.T) {
	id := NewID(time.Date(2006, 10, 29, 15, 4, 5, 0, time.UTC))
	if !regexp.MustCompile(`^\d{8}T\d{6}-[0-9a-f]{4}$`).MatchString(id) {
		t.Fatalf("bad id %q", id)
	}
	assert.Eq(t, id[:16], "20061029T150405-")
}

func meta(id string, s State) Meta {
	return Meta{ID: id, Profile: "p", State: s}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m := Meta{
		ID: "a-1", Profile: "p", State: Pending, StartedAt: at, EndedAt: at.Add(time.Minute),
		SessionID: "s1", Window: Window{Since: at, Until: at.Add(time.Hour), Sessions: []string{"x", "y"}},
		Base: map[string]string{"a.md": "h1"}, Proposed: map[string]string{"b.md": "h2"},
		Counts: Counts{Changed: 1, Added: 2, Removed: 3, LintFixed: 4, LintOpen: 5, Promote: 6}, Partial: true,
	}
	assert.NoErr(t, Save(dir, m))
	got, err := Load(dir, "a-1")
	assert.NoErr(t, err)
	assert.Eq(t, got.ID, m.ID)
	assert.Eq(t, got.State, Pending)
	assert.Eq(t, got.Counts, m.Counts)
	assert.Eq(t, got.Partial, true)
	assert.Eq(t, got.Window.Sessions[1], "y")
	assert.Eq(t, got.Base["a.md"], "h1")
	assert.Eq(t, got.Proposed["b.md"], "h2")
	if !got.StartedAt.Equal(at) || !got.Window.Until.Equal(at.Add(time.Hour)) {
		t.Fatalf("times lost: %+v", got)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "a-1", "dream.json"))
	assert.NoErr(t, err)
	assert.Contains(t, string(raw), `"started_at"`)
	assert.Contains(t, string(raw), `"lint_fixed"`)
	assert.Contains(t, string(raw), `"session_id"`)
}

func TestList_NewestFirstSkipsBroken(t *testing.T) {
	dir := t.TempDir()
	assert.NoErr(t, Save(dir, meta("20260101T000000-aaaa", Applied)))
	assert.NoErr(t, Save(dir, meta("20260103T000000-aaaa", Pending)))
	assert.NoErr(t, Save(dir, meta("20260102T000000-aaaa", Pending)))
	assert.NoErr(t, os.MkdirAll(filepath.Join(dir, "nodream"), 0o755))
	assert.NoErr(t, os.MkdirAll(filepath.Join(dir, "bad"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, "bad", "dream.json"), []byte("{"), 0o644))
	ms, err := List(dir)
	assert.NoErr(t, err)
	assert.Eq(t, len(ms), 3)
	assert.Eq(t, ms[0].ID, "20260103T000000-aaaa")
	assert.Eq(t, ms[2].ID, "20260101T000000-aaaa")
}

func TestList_MissingDir(t *testing.T) {
	ms, err := List(filepath.Join(t.TempDir(), "none"))
	assert.NoErr(t, err)
	assert.Eq(t, len(ms), 0)
}

func TestNewest_ByState(t *testing.T) {
	ms := []Meta{
		meta("3", Pending), meta("2", Applied), meta("1", Pending),
	}
	p, ok := Newest(ms, Pending)
	assert.Eq(t, ok, true)
	assert.Eq(t, p.ID, "3")
	a, ok := Newest(ms, Applied)
	assert.Eq(t, ok, true)
	assert.Eq(t, a.ID, "2")
	_, ok = Newest(ms, Undone)
	assert.Eq(t, ok, false)
}

func TestWatermark_FallbackAndSet(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if got := Watermark(dir, now); !got.Equal(now.Add(-30 * 24 * time.Hour)) {
		t.Fatalf("fallback = %v", got)
	}
	at := time.Date(2026, 4, 20, 1, 2, 3, 0, time.UTC)
	assert.NoErr(t, SetWatermark(dir, at))
	if got := Watermark(dir, now); !got.Equal(at) {
		t.Fatalf("watermark = %v", got)
	}
}

func TestPrune_KeepsPending(t *testing.T) {
	dir := t.TempDir()
	ids := make([]string, 12)
	for i := range ids {
		ids[i] = time.Date(2026, 1, i+1, 0, 0, 0, 0, time.UTC).Format("20060102T150405") + "-0000"
		s := Applied
		if i < 3 {
			s = Pending
		}
		assert.NoErr(t, Save(dir, meta(ids[i], s)))
	}
	assert.NoErr(t, Prune(dir, 10))
	ms, err := List(dir)
	assert.NoErr(t, err)
	// 9 non-pending (<=10 kept) + 3 pending: nothing to delete.
	assert.Eq(t, len(ms), 12)
	assert.NoErr(t, Prune(dir, 5))
	ms, err = List(dir)
	assert.NoErr(t, err)
	assert.Eq(t, len(ms), 8)
	for _, id := range ids[:3] {
		if _, err := Load(dir, id); err != nil {
			t.Fatalf("pending %s pruned: %v", id, err)
		}
	}
	for _, id := range ids[3:7] {
		if _, err := os.Stat(filepath.Join(dir, id)); !os.IsNotExist(err) {
			t.Fatalf("%s should be gone", id)
		}
	}
}
