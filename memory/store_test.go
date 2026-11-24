package memory

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// workRoot is an absolute project root that need not exist: Windows needs a
// volume for a path to be absolute.
var workRoot = func() string {
	if runtime.GOOS == "windows" {
		return `C:\work\repo`
	}
	return "/work/repo"
}()

func lines(n int, prefix string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(prefix)
		b.WriteString("\n")
	}
	return b.String()
}

func setup(t *testing.T, profile, project string) Dirs {
	t.Helper()
	d, err := DirsFor(t.TempDir(), workRoot)
	if err != nil {
		t.Fatal(err)
	}
	for dir, body := range map[string]string{d.Profile: profile, d.Project: project} {
		if body == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func textLines(s string) int { return strings.Count(strings.TrimRight(s, "\n"), "\n") + 1 }

func TestDirsFor(t *testing.T) {
	p := filepath.Dir(workRoot) // any absolute dir
	d, err := DirsFor(p, workRoot)
	if err != nil {
		t.Fatal(err)
	}
	if d.Profile != filepath.Join(p, "memory") || d.Project != filepath.Join(p, "memory", "projects", Key(workRoot)) {
		t.Fatalf("dirs = %+v", d)
	}
}

func TestDirsFor_RejectsRelativeRoot(t *testing.T) {
	for _, root := range []string{"", ".", "repo", "../x"} {
		if _, err := DirsFor("/p", root); err == nil {
			t.Errorf("root %q: want error", root)
		}
	}
}

func TestLoadIndexes_CombinedCap(t *testing.T) {
	d := setup(t, lines(180, "- p"), lines(80, "- q"))
	p, q, err := LoadIndexes(d, false)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Truncated || !q.Truncated {
		t.Fatalf("truncated = %v %v", p.Truncated, q.Truncated)
	}
	if !strings.HasSuffix(p.Text, "(memory index truncated at 150 lines; prune it)\n") &&
		!strings.HasSuffix(p.Text, "(memory index truncated at 150 lines; prune it)") {
		t.Errorf("profile text tail: %q", p.Text[len(p.Text)-60:])
	}
	if !strings.Contains(q.Text, "(memory index truncated at 50 lines; prune it)") {
		t.Errorf("project text: %q", q.Text)
	}
	if p.Lines != 180 || q.Lines != 80 {
		t.Errorf("Lines = %d %d, want pre-cap 180 80", p.Lines, q.Lines)
	}
	if got := textLines(p.Text); got != 151 {
		t.Errorf("profile text lines = %d, want 150 + truncation line", got)
	}
}

func TestLoadIndexes_ProfileOnlyGetsWholeBudget(t *testing.T) {
	d := setup(t, lines(250, "- p"), "")
	p, q, err := LoadIndexes(d, false)
	if err != nil {
		t.Fatal(err)
	}
	if q.Exists {
		t.Error("project should not exist")
	}
	if !strings.Contains(p.Text, "truncated at 200 lines") || textLines(p.Text) != 201 {
		t.Errorf("profile text lines = %d", textLines(p.Text))
	}
}

func TestLoadIndexes_ByteCap(t *testing.T) {
	row := strings.Repeat("x", 199) // 200 bytes with newline
	d := setup(t, lines(150, row), "")
	p, _, err := LoadIndexes(d, false)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Truncated || p.Bytes != 30000 {
		t.Fatalf("truncated=%v bytes=%d", p.Truncated, p.Bytes)
	}
	body, tail, _ := strings.Cut(p.Text, "(memory index truncated")
	if len(body) > MaxIndexBytes {
		t.Errorf("body %d bytes > cap", len(body))
	}
	if !strings.HasSuffix(body, row+"\n") {
		t.Error("not cut on a line boundary")
	}
	if !strings.HasPrefix(tail, " at 128 lines; prune it)") {
		t.Errorf("tail = %q", tail)
	}
}

func TestLoadIndexes_HideProject(t *testing.T) {
	d := setup(t, "- p\n", "- q\n")
	_, q, err := LoadIndexes(d, true)
	if err != nil {
		t.Fatal(err)
	}
	if q.Exists || q.Text != "" {
		t.Errorf("project = %+v", q)
	}
}

func TestLoadIndexes_MissingIsNotError(t *testing.T) {
	d := setup(t, "", "")
	p, q, err := LoadIndexes(d, false)
	if err != nil || p.Exists || q.Exists {
		t.Fatalf("p=%+v q=%+v err=%v", p, q, err)
	}
}

func TestLoadIndexes_IgnoresNoteFrontmatter(t *testing.T) {
	d := setup(t, "- [a](a.md) — hook\n", "")
	if err := os.WriteFile(filepath.Join(d.Profile, "a.md"), []byte("---\nbroken: [\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, _, err := LoadIndexes(d, false)
	if err != nil || !p.Exists || p.Lines != 1 {
		t.Fatalf("p=%+v err=%v", p, err)
	}
}

func TestLoadIndexes_CRLFAndNoTrailingNewline(t *testing.T) {
	body := strings.TrimSuffix(strings.Repeat("- p\r\n", 201), "\r\n")
	d := setup(t, body, "")
	p, _, err := LoadIndexes(d, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Lines != 201 || !p.Truncated || !strings.Contains(p.Text, "truncated at 200 lines") {
		t.Fatalf("Lines=%d truncated=%v", p.Lines, p.Truncated)
	}
	if p.SHA256 == "" {
		t.Error("SHA256 empty")
	}
}

func TestSection_NamesDirsAndGuidance(t *testing.T) {
	d := setup(t, "- [a](a.md) — hook\n", "- [b](b.md) — hook\n")
	p, q, _ := LoadIndexes(d, false)
	s := Section(d, p, q, true)
	for _, want := range []string{"Memory", d.Profile, d.Project, "[[name]]", "never a whole-file", "cannot delete", "- [a](a.md)", "- [b](b.md)"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(s, "not available in this session") {
		t.Error("writable section claims read-only")
	}
	ro := Section(d, p, q, false)
	if !strings.Contains(ro, "not available in this session") {
		t.Error("read-only section lacks notice")
	}
	for _, want := range []string{"Check a file, flag or function", "Writes are shown to the owner", "in that same turn", "where something lives outside the repo", "If they decline the write, drop it"} {
		if !strings.Contains(s, want) {
			t.Errorf("writable missing %q", want)
		}
	}
	if strings.Contains(s, "Keep them few") {
		t.Error("writable section still discourages saving")
	}
	for _, bad := range []string{"in that same turn", "decline"} {
		if strings.Contains(ro, bad) {
			t.Errorf("read-only section has %q", bad)
		}
	}
	for _, want := range []string{"Check a file, flag or function", "not available in this session"} {
		if !strings.Contains(ro, want) {
			t.Errorf("read-only missing %q", want)
		}
	}
	for _, bad := range []string{"never a whole-file", "Writes are shown to the owner", "cannot delete"} {
		if strings.Contains(ro, bad) {
			t.Errorf("read-only contains write bullet %q", bad)
		}
	}
}

func TestSection_NoProjectDirLeavesProjectOut(t *testing.T) {
	d := setup(t, "- [a](a.md) — hook\n", "- [b](b.md) — hook\n")
	p, q, _ := LoadIndexes(d, true)
	s := Section(Dirs{Profile: d.Profile}, p, q, false)
	if !strings.Contains(s, "- [a](a.md)") {
		t.Error("profile index missing")
	}
	for _, bad := range []string{d.Project, "Project memory", "(empty)"} {
		if strings.Contains(s, bad) {
			t.Errorf("section names the hidden project: %q", bad)
		}
	}
}

// An index that is a link out of its memory folder is not read: the prompt
// would otherwise carry any file the link points at.
func TestLoadIndexes_RefusesIndexLinkedOutside(t *testing.T) {
	d := setup(t, "", "- q\n")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("OUTSIDE-9931\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(d.Profile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(d.Profile, "MEMORY.md")); err != nil {
		t.Fatal(err)
	}
	p, q, err := LoadIndexes(d, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Exists || p.Text != "" || p.Bytes != 0 {
		t.Errorf("profile index read through the link: %+v", p)
	}
	if !strings.Contains(p.Ignored, "outside") || !strings.Contains(p.Ignored, d.Profile) {
		t.Errorf("Ignored = %q", p.Ignored)
	}
	if q.Text != "- q\n" || q.Ignored != "" {
		t.Errorf("project = %+v", q)
	}
}

func TestLoadIndexes_LinkInsideStoreIsRead(t *testing.T) {
	d := setup(t, "", "")
	if err := os.MkdirAll(d.Profile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.Profile, "index-src.md"), []byte("- p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("index-src.md", filepath.Join(d.Profile, "MEMORY.md")); err != nil {
		t.Fatal(err)
	}
	p, _, err := LoadIndexes(d, false)
	if err != nil || p.Text != "- p\n" || p.Ignored != "" {
		t.Fatalf("p=%+v err=%v", p, err)
	}
}
