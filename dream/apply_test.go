package dream

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonli0226/jas/internal/assert"
)

const testID = "20261113T090000-beef"

var testEnded = time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)

type applyFixture struct {
	memDir, dreamsDir string
	m                 Meta
}

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	assert.NoErr(t, os.MkdirAll(dir, 0o755))
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		assert.NoErr(t, os.MkdirAll(filepath.Dir(full), 0o755))
		assert.NoErr(t, os.WriteFile(full, []byte(body), 0o644))
	}
}

// readTree maps each regular file under dir to its contents; a missing dir
// yields nil.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	root, err := filepath.EvalSymlinks(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	assert.NoErr(t, err)
	out := map[string]string{}
	assert.NoErr(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	}))
	return out
}

func treeEq(t *testing.T, got, want map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tree = %v, want %v", got, want)
	}
}

var (
	liveFiles = map[string]string{"MEMORY.md": "- [A](a.md) — a\n", "a.md": "old a\n", "projects/k/b.md": "b\n"}
	propFiles = map[string]string{"MEMORY.md": "- [A](a.md) — a\n- [C](c.md) — c\n", "a.md": "new a\n", "c.md": "c\n"}
)

// newDream builds a pending dream over memDir (which may be absent or a
// link) whose proposal is prop.
func newDream(t *testing.T, memDir, dreamsDir string, prop map[string]string) Meta {
	t.Helper()
	base, err := HashTree(memDir)
	assert.NoErr(t, err)
	proposedDir := filepath.Join(dreamsDir, testID, "proposed")
	writeTree(t, proposedDir, prop)
	assert.NoErr(t, os.WriteFile(filepath.Join(dreamsDir, testID, "report.md"), []byte("report\n"), 0o644))
	proposed, err := HashTree(proposedDir)
	assert.NoErr(t, err)
	m := Meta{ID: testID, Profile: "p", State: Pending, StartedAt: testEnded.Add(-time.Minute),
		EndedAt: testEnded, Base: base, Proposed: proposed}
	assert.NoErr(t, Save(dreamsDir, m))
	return m
}

func setup(t *testing.T) applyFixture {
	t.Helper()
	root := t.TempDir()
	f := applyFixture{memDir: filepath.Join(root, "memory"), dreamsDir: filepath.Join(root, "dreams")}
	writeTree(t, f.memDir, liveFiles)
	f.m = newDream(t, f.memDir, f.dreamsDir, propFiles)
	return f
}

func (f applyFixture) idPath(elem ...string) string {
	return filepath.Join(append([]string{f.dreamsDir, testID}, elem...)...)
}

func loadState(t *testing.T, dreamsDir string) State {
	t.Helper()
	m, err := Load(dreamsDir, testID)
	assert.NoErr(t, err)
	return m.State
}

func TestApply_SwapsAndKeepsBefore(t *testing.T) {
	f := setup(t)
	got, err := Apply(f.memDir, f.dreamsDir, f.m)
	assert.NoErr(t, err)
	assert.Eq(t, got.State, Applied)
	assert.Eq(t, loadState(t, f.dreamsDir), Applied)
	treeEq(t, readTree(t, f.memDir), propFiles)
	treeEq(t, readTree(t, f.idPath("before")), liveFiles)
	assert.Eq(t, Watermark(f.dreamsDir, time.Now()), testEnded)
	if _, err := os.Stat(f.idPath("proposed")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("proposed/ still present: %v", err)
	}
}

func TestApply_StaleRefuses(t *testing.T) {
	f := setup(t)
	assert.NoErr(t, os.WriteFile(filepath.Join(f.memDir, "a.md"), []byte("edited\n"), 0o644))
	_, err := Apply(f.memDir, f.dreamsDir, f.m)
	var se *StaleError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *StaleError", err)
	}
	assert.Eq(t, strings.Join(se.Paths, ","), "a.md")
	assert.Contains(t, err.Error(), "a.md")
	assert.Contains(t, err.Error(), "re-run jas dream")
	assert.Eq(t, loadState(t, f.dreamsDir), Stale)
	want := map[string]string{}
	for k, v := range liveFiles {
		want[k] = v
	}
	want["a.md"] = "edited\n"
	treeEq(t, readTree(t, f.memDir), want)
	treeEq(t, readTree(t, f.idPath("proposed")), propFiles)
}

func TestApply_NotPending(t *testing.T) {
	f := setup(t)
	f.m.State = Discarded
	assert.NoErr(t, Save(f.dreamsDir, f.m))
	_, err := Apply(f.memDir, f.dreamsDir, f.m)
	if err == nil {
		t.Fatal("want error applying a discarded dream")
	}
	assert.Contains(t, err.Error(), "discarded")
	treeEq(t, readTree(t, f.memDir), liveFiles)
}

func TestApply_UsesRecordOnDisk(t *testing.T) {
	f := setup(t)
	_, err := Apply(f.memDir, f.dreamsDir, f.m)
	assert.NoErr(t, err)
	// A second apply with the caller's stale copy (still pending) is refused.
	_, err = Apply(f.memDir, f.dreamsDir, f.m)
	if err == nil {
		t.Fatal("want error re-applying an applied dream")
	}
	treeEq(t, readTree(t, f.memDir), propFiles)
}

func TestApply_TamperedProposalRefuses(t *testing.T) {
	f := setup(t)
	assert.NoErr(t, os.WriteFile(f.idPath("proposed", "c.md"), []byte("changed\n"), 0o644))
	_, err := Apply(f.memDir, f.dreamsDir, f.m)
	if err == nil {
		t.Fatal("want error on a proposal that no longer matches its record")
	}
	treeEq(t, readTree(t, f.memDir), liveFiles)
	assert.Eq(t, loadState(t, f.dreamsDir), Pending)
}

func TestApply_Locked(t *testing.T) {
	f := setup(t)
	release, err := lockDreams(f.dreamsDir)
	assert.NoErr(t, err)
	defer func() { assert.NoErr(t, release()) }()
	_, err = Apply(f.memDir, f.dreamsDir, f.m)
	if err == nil {
		t.Fatal("want error while another process holds the lock")
	}
	treeEq(t, readTree(t, f.memDir), liveFiles)
}

// symlinkSetup makes memory a relative link to ../elsewhere/mem.
func symlinkSetup(t *testing.T) (applyFixture, string) {
	t.Helper()
	root := t.TempDir()
	profile := filepath.Join(root, "profile")
	target := filepath.Join(root, "elsewhere", "mem")
	writeTree(t, target, liveFiles)
	assert.NoErr(t, os.MkdirAll(profile, 0o755))
	f := applyFixture{memDir: filepath.Join(profile, "memory"), dreamsDir: filepath.Join(profile, "dreams")}
	if err := os.Symlink(filepath.Join("..", "elsewhere", "mem"), f.memDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	f.m = newDream(t, f.memDir, f.dreamsDir, propFiles)
	return f, target
}

func assertLink(t *testing.T, memDir string) {
	t.Helper()
	link, err := os.Readlink(memDir)
	assert.NoErr(t, err)
	assert.Eq(t, link, filepath.Join("..", "elsewhere", "mem"))
}

func assertNoSiblings(t *testing.T, target string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(target))
	assert.NoErr(t, err)
	if len(entries) != 1 {
		t.Fatalf("leftovers beside the target: %v", entries)
	}
}

func TestApply_SymlinkedMemoryKeepsLink(t *testing.T) {
	f, target := symlinkSetup(t)
	_, err := Apply(f.memDir, f.dreamsDir, f.m)
	assert.NoErr(t, err)
	assertLink(t, f.memDir)
	treeEq(t, readTree(t, target), propFiles)
	treeEq(t, readTree(t, f.idPath("before")), liveFiles)
	assertNoSiblings(t, target)
	assert.Eq(t, loadState(t, f.dreamsDir), Applied)

	got, err := Undo(f.memDir, f.dreamsDir, Meta{ID: testID})
	assert.NoErr(t, err)
	assert.Eq(t, got.State, Undone)
	assertLink(t, f.memDir)
	treeEq(t, readTree(t, target), liveFiles)
	assertNoSiblings(t, target)
}

func TestApply_DanglingLinkRefuses(t *testing.T) {
	root := t.TempDir()
	f := applyFixture{memDir: filepath.Join(root, "memory"), dreamsDir: filepath.Join(root, "dreams")}
	if err := os.Symlink(filepath.Join(root, "gone"), f.memDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	f.m = newDream(t, f.memDir, f.dreamsDir, propFiles)
	_, err := Apply(f.memDir, f.dreamsDir, f.m)
	if err == nil {
		t.Fatal("want error on a link to a missing folder")
	}
	treeEq(t, readTree(t, f.idPath("proposed")), propFiles)
	assert.Eq(t, loadState(t, f.dreamsDir), Pending)
}

func TestApplyUndo_RoundTrip(t *testing.T) {
	f := setup(t)
	applied, err := Apply(f.memDir, f.dreamsDir, f.m)
	assert.NoErr(t, err)
	later := testEnded.Add(time.Hour)
	assert.NoErr(t, SetWatermark(f.dreamsDir, later))
	got, err := Undo(f.memDir, f.dreamsDir, applied)
	assert.NoErr(t, err)
	assert.Eq(t, got.State, Undone)
	assert.Eq(t, loadState(t, f.dreamsDir), Undone)
	treeEq(t, readTree(t, f.memDir), liveFiles)
	treeEq(t, readTree(t, f.idPath("proposed")), propFiles)
	assert.Eq(t, Watermark(f.dreamsDir, time.Now()), later)
	if _, err := os.Stat(f.idPath("before")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("before/ still present: %v", err)
	}
}

func TestApplyUndo_MissingStore(t *testing.T) {
	root := t.TempDir()
	f := applyFixture{memDir: filepath.Join(root, "profile", "memory"), dreamsDir: filepath.Join(root, "dreams")}
	f.m = newDream(t, f.memDir, f.dreamsDir, propFiles)
	assert.Eq(t, len(f.m.Base), 0)
	_, err := Apply(f.memDir, f.dreamsDir, f.m)
	assert.NoErr(t, err)
	treeEq(t, readTree(t, f.memDir), propFiles)
	if _, err := os.Stat(f.idPath("before")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("before/ should be absent for a missing store: %v", err)
	}
	got, err := Undo(f.memDir, f.dreamsDir, f.m)
	assert.NoErr(t, err)
	assert.Eq(t, got.State, Undone)
	if _, err := os.Lstat(f.memDir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("memory/ should be gone after undo: %v", err)
	}
	treeEq(t, readTree(t, f.idPath("proposed")), propFiles)
}

func TestUndo_LiveChangedRefuses(t *testing.T) {
	f := setup(t)
	_, err := Apply(f.memDir, f.dreamsDir, f.m)
	assert.NoErr(t, err)
	assert.NoErr(t, os.WriteFile(filepath.Join(f.memDir, "c.md"), []byte("owner edit\n"), 0o644))
	_, err = Undo(f.memDir, f.dreamsDir, f.m)
	if err == nil {
		t.Fatal("want error undoing over a changed store")
	}
	assert.Contains(t, err.Error(), "c.md")
	assert.Eq(t, loadState(t, f.dreamsDir), Applied)
	assert.Eq(t, readTree(t, f.memDir)["c.md"], "owner edit\n")
	treeEq(t, readTree(t, f.idPath("before")), liveFiles)
}

func TestUndo_NotApplied(t *testing.T) {
	f := setup(t)
	_, err := Undo(f.memDir, f.dreamsDir, f.m)
	if err == nil {
		t.Fatal("want error undoing a pending dream")
	}
	treeEq(t, readTree(t, f.memDir), liveFiles)
}

func TestUndo_Pruned(t *testing.T) {
	f := setup(t)
	_, err := Apply(f.memDir, f.dreamsDir, f.m)
	assert.NoErr(t, err)
	assert.NoErr(t, os.RemoveAll(f.idPath("before")))
	_, err = Undo(f.memDir, f.dreamsDir, f.m)
	var pe *PrunedError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *PrunedError", err)
	}
	assert.Eq(t, err.Error(), "dream "+testID+" was pruned; only the newest 10 dreams keep their old store")
	treeEq(t, readTree(t, f.memDir), propFiles)

	assert.NoErr(t, os.RemoveAll(f.idPath()))
	_, err = Undo(f.memDir, f.dreamsDir, f.m)
	if !errors.As(err, &pe) {
		t.Fatalf("whole dir gone: err = %v, want *PrunedError", err)
	}
	if _, err := os.Stat(f.idPath()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("undo of a pruned dream must not recreate its folder")
	}
}

func TestDiscard_KeepsReportMovesWatermark(t *testing.T) {
	f := setup(t)
	got, err := Discard(f.dreamsDir, f.m)
	assert.NoErr(t, err)
	assert.Eq(t, got.State, Discarded)
	assert.Eq(t, loadState(t, f.dreamsDir), Discarded)
	assert.Eq(t, Watermark(f.dreamsDir, time.Now()), testEnded)
	if _, err := os.Stat(f.idPath("proposed")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("proposed/ still present: %v", err)
	}
	b, err := os.ReadFile(f.idPath("report.md"))
	assert.NoErr(t, err)
	assert.Eq(t, string(b), "report\n")
	treeEq(t, readTree(t, f.memDir), liveFiles)
	if _, err := Discard(f.dreamsDir, f.m); err == nil {
		t.Fatal("want error discarding twice")
	}
}

// failOps returns the real operations with the first rename and the first
// copy into dst failing; later calls (the rollback) succeed.
func failOps(dst string) fsOps {
	ops := osOps
	renameFailed, copyFailed := false, false
	ops.rename = func(a, b string) error {
		if b == dst && !renameFailed {
			renameFailed = true
			return errors.New("injected rename failure")
		}
		return os.Rename(a, b)
	}
	ops.copyTree = func(a, b string) error {
		if b == dst && !copyFailed {
			copyFailed = true
			return errors.New("injected copy failure")
		}
		return copyPlainTree(a, b)
	}
	return ops
}

func TestApply_SecondRenameFailsRestores(t *testing.T) {
	f := setup(t)
	_, err := apply(failOps(f.memDir), f.memDir, f.dreamsDir, f.m)
	if err == nil {
		t.Fatal("want injected failure")
	}
	treeEq(t, readTree(t, f.memDir), liveFiles)
	treeEq(t, readTree(t, f.idPath("proposed")), propFiles)
	if _, err := os.Stat(f.idPath("before")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("before/ should be moved back: %v", err)
	}
	assert.Eq(t, loadState(t, f.dreamsDir), Pending)
	_, err = Apply(f.memDir, f.dreamsDir, f.m)
	assert.NoErr(t, err)
}

func TestApply_SaveFailsRollsBack(t *testing.T) {
	f := setup(t)
	ops := osOps
	ops.save = func(dreamsDir string, m Meta) error {
		if m.State == Applied {
			return errors.New("injected save failure")
		}
		return Save(dreamsDir, m)
	}
	_, err := apply(ops, f.memDir, f.dreamsDir, f.m)
	if err == nil {
		t.Fatal("want injected failure")
	}
	treeEq(t, readTree(t, f.memDir), liveFiles)
	treeEq(t, readTree(t, f.idPath("proposed")), propFiles)
	assert.Eq(t, loadState(t, f.dreamsDir), Pending)
}

func TestApply_SymlinkMoveBeforeFailsRollsBack(t *testing.T) {
	f, target := symlinkSetup(t)
	_, err := apply(failOps(f.idPath("before")), f.memDir, f.dreamsDir, f.m)
	if err == nil {
		t.Fatal("want injected failure")
	}
	assertLink(t, f.memDir)
	treeEq(t, readTree(t, target), liveFiles)
	assertNoSiblings(t, target)
	treeEq(t, readTree(t, f.idPath("proposed")), propFiles)
	assert.Eq(t, loadState(t, f.dreamsDir), Pending)
}

func TestApply_SymlinkSwapFailsRestores(t *testing.T) {
	f, target := symlinkSetup(t)
	_, err := apply(failOps(target), f.memDir, f.dreamsDir, f.m)
	if err == nil {
		t.Fatal("want injected failure")
	}
	assertLink(t, f.memDir)
	treeEq(t, readTree(t, target), liveFiles)
	assertNoSiblings(t, target)
	assert.Eq(t, loadState(t, f.dreamsDir), Pending)
}

func TestApply_RollbackFailureNamesOldStore(t *testing.T) {
	f := setup(t)
	before := f.idPath("before")
	ops := osOps
	ops.rename = func(a, b string) error {
		if b == f.memDir {
			return errors.New("injected rename failure")
		}
		return os.Rename(a, b)
	}
	_, err := apply(ops, f.memDir, f.dreamsDir, f.m)
	if err == nil {
		t.Fatal("want injected failure")
	}
	assert.Contains(t, err.Error(), before)
	treeEq(t, readTree(t, before), liveFiles)
	assert.Eq(t, loadState(t, f.dreamsDir), Pending)
}

func TestApply_SymlinkMoveBeforeFallsBackToCopy(t *testing.T) {
	f, target := symlinkSetup(t)
	ops := osOps
	ops.rename = func(a, b string) error {
		if b == f.idPath("before") {
			return errors.New("injected cross-device rename")
		}
		return os.Rename(a, b)
	}
	_, err := apply(ops, f.memDir, f.dreamsDir, f.m)
	assert.NoErr(t, err)
	assertLink(t, f.memDir)
	treeEq(t, readTree(t, target), propFiles)
	treeEq(t, readTree(t, f.idPath("before")), liveFiles)
	assertNoSiblings(t, target)
	assert.Eq(t, loadState(t, f.dreamsDir), Applied)
}

func TestUndo_SaveFailsRollsBack(t *testing.T) {
	for _, linked := range []bool{false, true} {
		var f applyFixture
		if linked {
			f, _ = symlinkSetup(t)
		} else {
			f = setup(t)
		}
		_, err := Apply(f.memDir, f.dreamsDir, f.m)
		assert.NoErr(t, err)
		ops := osOps
		ops.save = func(dreamsDir string, m Meta) error {
			if m.State == Undone {
				return errors.New("injected save failure")
			}
			return Save(dreamsDir, m)
		}
		_, err = undo(ops, f.memDir, f.dreamsDir, f.m)
		if err == nil {
			t.Fatalf("linked=%v: want injected failure", linked)
		}
		treeEq(t, readTree(t, f.memDir), propFiles)
		treeEq(t, readTree(t, f.idPath("before")), liveFiles)
		assert.Eq(t, loadState(t, f.dreamsDir), Applied)
		got, err := Undo(f.memDir, f.dreamsDir, f.m)
		assert.NoErr(t, err)
		assert.Eq(t, got.State, Undone)
		treeEq(t, readTree(t, f.memDir), liveFiles)
	}
}

func TestUndo_SymlinkedStoreWithInnerLinkRefused(t *testing.T) {
	f, target := symlinkSetup(t)
	inner := filepath.Join(target, "link.md")
	assert.NoErr(t, os.Symlink("a.md", inner))
	_, err := Apply(f.memDir, f.dreamsDir, f.m)
	assert.NoErr(t, err) // the old store moved by rename, link and all
	keptLink := f.idPath("before", "link.md")
	if _, err := os.Lstat(keptLink); err != nil {
		t.Fatalf("link not kept in before/: %v", err)
	}
	_, err = Undo(f.memDir, f.dreamsDir, f.m)
	if err == nil {
		t.Fatal("want undo refused: a copy of before/ would drop its link")
	}
	assert.Contains(t, err.Error(), "link.md")
	assertLink(t, f.memDir)
	treeEq(t, readTree(t, target), propFiles)
	treeEq(t, readTree(t, f.idPath("before")), liveFiles)
	if _, err := os.Lstat(keptLink); err != nil {
		t.Fatalf("link lost from before/: %v", err)
	}
	assertNoSiblings(t, target)
	assert.Eq(t, loadState(t, f.dreamsDir), Applied)
}

// crossDevice fails every rename into dst, as across file systems.
func crossDevice(dst string) fsOps {
	ops := osOps
	ops.rename = func(a, b string) error {
		if b == dst {
			return errors.New("injected cross-device rename")
		}
		return os.Rename(a, b)
	}
	return ops
}

func TestApply_CopyFallbackKeepsMode(t *testing.T) {
	f, target := symlinkSetup(t)
	assert.NoErr(t, os.Chmod(filepath.Join(target, "a.md"), 0o600))
	assert.NoErr(t, os.Chmod(f.idPath("proposed", "c.md"), 0o600))
	_, err := apply(crossDevice(f.idPath("before")), f.memDir, f.dreamsDir, f.m)
	assert.NoErr(t, err)
	treeEq(t, readTree(t, f.idPath("before")), liveFiles)
	for _, p := range []string{f.idPath("before", "a.md"), filepath.Join(target, "c.md")} {
		fi, err := os.Stat(p)
		assert.NoErr(t, err)
		assert.Perm(t, fi.Mode().Perm(), 0o600)
	}
}

func TestApply_CopyFallbackRefusesInnerLink(t *testing.T) {
	f, target := symlinkSetup(t)
	assert.NoErr(t, os.Symlink("a.md", filepath.Join(target, "link.md")))
	_, err := apply(crossDevice(f.idPath("before")), f.memDir, f.dreamsDir, f.m)
	if err == nil {
		t.Fatal("want refusal: the copy fallback would drop the link")
	}
	assertLink(t, f.memDir)
	treeEq(t, readTree(t, target), liveFiles)
	if _, err := os.Lstat(filepath.Join(target, "link.md")); err != nil {
		t.Fatalf("link lost: %v", err)
	}
	assertNoSiblings(t, target)
	treeEq(t, readTree(t, f.idPath("proposed")), propFiles)
	assert.Eq(t, loadState(t, f.dreamsDir), Pending)
}

func TestApply_SymlinkLeftoverAndSaveFailRollsBack(t *testing.T) {
	f, target := symlinkSetup(t)
	proposed := f.idPath("proposed")
	ops := osOps
	ops.removeAll = func(p string) error {
		if p == proposed { // delete part of the copy, then fail
			assert.NoErr(t, os.Remove(filepath.Join(p, "c.md")))
			return errors.New("injected partial delete")
		}
		return os.RemoveAll(p)
	}
	ops.save = func(dreamsDir string, m Meta) error {
		if m.State == Applied {
			return errors.New("injected save failure")
		}
		return Save(dreamsDir, m)
	}
	_, err := apply(ops, f.memDir, f.dreamsDir, f.m)
	if err == nil {
		t.Fatal("want injected failure")
	}
	assert.Contains(t, err.Error(), "old store is back in place")
	assertLink(t, f.memDir)
	treeEq(t, readTree(t, target), liveFiles)
	treeEq(t, readTree(t, proposed), propFiles)
	assertNoSiblings(t, target)
	assert.Eq(t, loadState(t, f.dreamsDir), Pending)
}
