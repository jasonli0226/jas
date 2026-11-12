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
		ID: "20260102T030405-a1b2", Profile: "p", State: Pending, StartedAt: at, EndedAt: at.Add(time.Minute),
		SessionID: "s1", Window: Window{Since: at, Until: at.Add(time.Hour), Sessions: []string{"x", "y"}},
		Base: map[string]string{"a.md": "h1"}, Proposed: map[string]string{"b.md": "h2"},
		Counts: Counts{Changed: 1, Added: 2, Removed: 3, LintFixed: 4, LintOpen: 5, Promote: 6}, Partial: true,
	}
	assert.NoErr(t, Save(dir, m))
	got, err := Load(dir, "20260102T030405-a1b2")
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
	raw, err := os.ReadFile(filepath.Join(dir, "20260102T030405-a1b2", "dream.json"))
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
		meta("20260103T000000-aaaa", Pending), meta("20260102T000000-aaaa", Applied), meta("20260101T000000-aaaa", Pending),
	}
	p, ok := Newest(ms, Pending)
	assert.Eq(t, ok, true)
	assert.Eq(t, p.ID, "20260103T000000-aaaa")
	a, ok := Newest(ms, Applied)
	assert.Eq(t, ok, true)
	assert.Eq(t, a.ID, "20260102T000000-aaaa")
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

func TestValidID(t *testing.T) {
	assert.Eq(t, ValidID(NewID(time.Now())), true)
	for _, id := range []string{"", "..", "../x", "a/b", "20260102T030405-A1B2", "20260102T030405-a1b", "20260102T030405-a1b2/x", "x20260102T030405-a1b2"} {
		assert.Eq(t, ValidID(id), false)
	}
}

func TestLoadSave_RejectInvalidID(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, meta("../evil", Pending)); err == nil {
		t.Fatal("Save accepted bad id")
	}
	if _, err := Load(dir, "../evil"); err == nil {
		t.Fatal("Load accepted bad id")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil")); err == nil {
		t.Fatal("wrote outside dir")
	}
}

func TestList_IDFromDirName(t *testing.T) {
	dir := t.TempDir()
	good := "20260101T000000-aaaa"
	assert.NoErr(t, Save(dir, meta(good, Applied)))
	// Valid dir name whose dream.json claims another id.
	other := "20260102T000000-bbbb"
	assert.NoErr(t, os.MkdirAll(filepath.Join(dir, other), 0o755))
	raw, err := os.ReadFile(filepath.Join(dir, good, "dream.json"))
	assert.NoErr(t, err)
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, other, "dream.json"), raw, 0o644))
	// Invalid dir name with a well-formed record.
	assert.NoErr(t, os.MkdirAll(filepath.Join(dir, "evil"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, "evil", "dream.json"), raw, 0o644))
	ms, err := List(dir)
	assert.NoErr(t, err)
	assert.Eq(t, len(ms), 1)
	assert.Eq(t, ms[0].ID, good)
	assert.NoErr(t, Prune(dir, 0))
	for _, n := range []string{other, "evil"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Fatalf("%s removed: %v", n, err)
		}
	}
}

func TestWatermark_CorruptFallsBack(t *testing.T) {
	dir := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, "state.json"), []byte("{not json"), 0o644))
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if got := Watermark(dir, now); !got.Equal(now.Add(-30 * 24 * time.Hour)) {
		t.Fatalf("got %v", got)
	}
}

func TestPrune_SkipsWhileLocked(t *testing.T) {
	dir := t.TempDir()
	ids := []string{"20260101T000000-0001", "20260102T000000-0002", "20260103T000000-0003"}
	for _, id := range ids {
		assert.NoErr(t, Save(dir, meta(id, Applied)))
	}
	release, err := lockDreams(dir)
	assert.NoErr(t, err)
	assert.NoErr(t, Prune(dir, 0))
	ms, err := List(dir)
	assert.NoErr(t, err)
	assert.Eq(t, len(ms), len(ids))
	assert.NoErr(t, release())
	assert.NoErr(t, Prune(dir, 0))
	ms, err = List(dir)
	assert.NoErr(t, err)
	assert.Eq(t, len(ms), 0)
}
