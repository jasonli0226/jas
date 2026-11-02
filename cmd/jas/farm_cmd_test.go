package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
	"github.com/jasonli0226/jas/skills"
)

const (
	testKey  = "farm-test-key-0123456789"
	farmPlan = "task: t\nparts:\n  - id: a\n    role: coder\n    goal: g\n    owns: [\"core/**\"]\n  - id: b\n    role: coder\n    goal: g\n    owns: [\"cmd/**\"]\nwhy_split: two packages\n"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func farmRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"core/a.go": "package core\n", "cmd/b.go": "package cmd\n", "AGENTS.md": "be nice\n"} {
		assert.NoErr(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// farmHome makes a JAS_HOME whose farm.yaml sets check ("" writes none).
func farmHome(t *testing.T, check string) string {
	t.Helper()
	home := t.TempDir()
	if check != "" {
		assert.NoErr(t, os.WriteFile(filepath.Join(home, "farm.yaml"), []byte("check: "+check+"\n"), 0o644))
	}
	return home
}

func fenceYAML(y string) string { return "plan:\n```yaml\n" + y + "```\n" }

func farmEnv(url, home string) []string {
	return []string{"JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=" + testKey, "JAS_HOME=" + home}
}

func farmDirs(t *testing.T, home string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(home, "farms", "*"))
	return m
}

func TestFarmPlan_RefusalExit8(t *testing.T) {
	repo, home := farmRepo(t), farmHome(t, "go test ./...")
	refuse := "refuse:\n  reason: small_change\n  detail: one typo\nsuggest: 'jas run \"fix typo\"'\n"
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: fenceYAML(refuse)}})
	out, errOut, code := runJasIn(t, repo, farmEnv(url, home), "farm", "plan", "fix typo", "--trust-project")
	assert.Eq(t, code, 8)
	assert.Contains(t, out, "planner refused: small_change")
	assert.Contains(t, out, `jas run "fix typo"`)
	dirs := farmDirs(t, home)
	assert.Eq(t, len(dirs), 1)
	ev := testutil.ReadEvents(t, filepath.Join(dirs[0], "farm.jsonl"))
	assert.Eq(t, ev[len(ev)-1]["type"].(string), farm.EvFarmPlanRefused)
	_, err := os.Stat(filepath.Join(repo, "plan.yaml"))
	if err == nil {
		t.Fatalf("plan.yaml written on refusal; stderr %s", errOut)
	}
	wt, _ := exec.Command("git", "-C", repo, "worktree", "list").Output()
	assert.Eq(t, strings.Count(string(wt), "\n"), 1)
}

func TestFarmPlan_NoCheckExit2(t *testing.T) {
	repo, home := farmRepo(t), farmHome(t, "")
	url, reqs := testutil.FakeOpenAI(t, nil)
	_, errOut, code := runJasIn(t, repo, farmEnv(url, home), "farm", "plan", "x", "--trust-project")
	assert.Eq(t, code, 2)
	assert.Contains(t, errOut, "farm.yaml")
	assert.Contains(t, errOut, "check")
	assert.Eq(t, len(reqs()), 0)
}

func TestFarmPlan_DirtyTreeExit2(t *testing.T) {
	repo, home := farmRepo(t), farmHome(t, "go test ./...")
	assert.NoErr(t, os.WriteFile(filepath.Join(repo, "junk.txt"), []byte("x"), 0o644))
	url, reqs := testutil.FakeOpenAI(t, []fake.Turn{{Text: fenceYAML(farmPlan)}})
	_, errOut, code := runJasIn(t, repo, farmEnv(url, home), "farm", "plan", "x", "--trust-project")
	assert.Eq(t, code, 2)
	assert.Contains(t, errOut, "--allow-dirty")
	assert.Eq(t, len(reqs()), 0)
	_, errOut, code = runJasIn(t, repo, farmEnv(url, home), "farm", "plan", "x", "--allow-dirty", "--trust-project", "-o", "p.yaml")
	assert.Eq(t, code, 0)
	assert.Contains(t, errOut, "uncommitted")
	assert.Eq(t, len(reqs()), 1)
}

func TestFarmPlan_HeadlessWritesUnapproved(t *testing.T) {
	repo, home := farmRepo(t), farmHome(t, "go test ./...")
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: fenceYAML(farmPlan)}})
	_, errOut, code := runJasIn(t, repo, farmEnv(url, home), "farm", "plan", "split it", "--trust-project", "-o", "out/plan.yaml")
	assert.Eq(t, code, 0)
	b, err := os.ReadFile(filepath.Join(repo, "out", "plan.yaml"))
	assert.NoErr(t, err)
	assert.NotContains(t, string(b), "approved:")
	assert.Contains(t, string(b), "base: ")
	plan, err := farm.ParsePlan(b)
	assert.NoErr(t, err)
	assert.Eq(t, len(plan.Parts), 2)
	dirs := farmDirs(t, home)
	assert.Eq(t, len(dirs), 1)
	for _, f := range []string{"plan.yaml", "farm.jsonl", filepath.Join("planner", "events.jsonl")} {
		if _, err := os.Stat(filepath.Join(dirs[0], f)); err != nil {
			t.Fatalf("%s: %v (stderr %s)", f, err, errOut)
		}
	}
}

func TestFarmPlan_TrivialCheckWarns(t *testing.T) {
	repo, home := farmRepo(t), farmHome(t, "go build ./...")
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: fenceYAML(farmPlan)}})
	_, errOut, code := runJasIn(t, repo, farmEnv(url, home), "farm", "plan", "x", "--trust-project", "-o", "p.yaml")
	assert.Eq(t, code, 0)
	assert.Contains(t, errOut, "check looks trivial")
}

func TestFarm_TaskWithoutTTYIsUsage(t *testing.T) {
	repo, home := farmRepo(t), farmHome(t, "go test ./...")
	_, errOut, code := runJasIn(t, repo, farmEnv("http://127.0.0.1:1", home), "farm", "do something")
	assert.Eq(t, code, 2)
	assert.Contains(t, errOut, "jas farm plan")
	assert.Contains(t, errOut, "jas farm run")
}

func reviewDeps(in string, out *bytes.Buffer) reviewIO {
	return reviewIO{
		In: bufio.NewReader(strings.NewReader(in)), Out: out,
		Now:  func() time.Time { return time.Date(2026, 11, 3, 1, 2, 3, 0, time.UTC) },
		Edit: func(string) error { return nil },
	}
}

func goodResult(t *testing.T) farm.PlannerResult {
	p, err := farm.ParsePlan([]byte(farmPlan))
	assert.NoErr(t, err)
	return farm.PlannerResult{Raw: []byte(farmPlan), Plan: p}
}

func TestFarmPlan_TTYApproveWritesHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.yaml")
	var out bytes.Buffer
	final, approved, err := reviewPlan(context.Background(), reviewDeps("a\n", &out), goodResult(t), path)
	assert.NoErr(t, err)
	assert.Eq(t, approved, true)
	b, err := os.ReadFile(path)
	assert.NoErr(t, err)
	assert.Eq(t, string(b), string(final))
	plan, err := farm.ParsePlan(b)
	assert.NoErr(t, err)
	assert.Eq(t, plan.Approved.SHA256, farm.CanonicalHash(b))
	assert.Eq(t, plan.Approved.By, "owner")
	assert.Contains(t, out.String(), "core/**")
}

func TestReview_QuitWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.yaml")
	_, approved, err := reviewPlan(context.Background(), reviewDeps("q\n", &bytes.Buffer{}), goodResult(t), path)
	assert.NoErr(t, err)
	assert.Eq(t, approved, false)
	if _, err := os.Stat(path); err == nil {
		t.Fatal("plan written on quit")
	}
}

func TestReview_EditInvalidThenFix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.yaml")
	d := reviewDeps("e\ne\na\n", &bytes.Buffer{})
	edits := 0
	d.Edit = func(p string) error {
		edits++
		body := "task: [unclosed\n"
		if edits == 2 {
			body = farmPlan + "# edited\n"
		}
		return os.WriteFile(p, []byte(body), 0o644)
	}
	d.Recheck = func(raw []byte) farm.PlannerResult {
		p, err := farm.ParsePlan(raw)
		if err != nil {
			return farm.PlannerResult{Raw: raw, Problems: []farm.Problem{{Message: err.Error()}}}
		}
		return farm.PlannerResult{Raw: raw, Plan: p}
	}
	final, approved, err := reviewPlan(context.Background(), d, goodResult(t), path)
	assert.NoErr(t, err)
	assert.Eq(t, approved, true)
	assert.Contains(t, string(final), "# edited")
	assert.Eq(t, edits, 2)
}

func TestReview_ReplanSendsFeedback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.yaml")
	d := reviewDeps("r\nsplit finer\na\n", &bytes.Buffer{})
	var got string
	d.Replan = func(_ context.Context, fb string) (farm.PlannerResult, error) {
		got = fb
		r := goodResult(t)
		r.Raw = []byte(farmPlan + "# v2\n")
		return r, nil
	}
	final, approved, err := reviewPlan(context.Background(), d, goodResult(t), path)
	assert.NoErr(t, err)
	assert.Eq(t, approved, true)
	assert.Eq(t, got, "split finer")
	assert.Contains(t, string(final), "# v2")
}

func TestWorkerContext_TrustFollowsMain(t *testing.T) {
	mk := func(agents string) string {
		d := t.TempDir()
		assert.NoErr(t, os.WriteFile(filepath.Join(d, "AGENTS.md"), []byte(agents), 0o644))
		return d
	}
	mainRoot, same, diff := mk("rules\n"), mk("rules\n"), mk("rules changed\n")
	main := projectContext{system: "MAIN SYSTEM", trusted: true}
	sys, ok := workerContext(main, mainRoot, same)
	assert.Eq(t, ok, true)
	assert.Eq(t, sys, "MAIN SYSTEM")
	sys, ok = workerContext(main, mainRoot, diff)
	assert.Eq(t, ok, false)
	assert.NotContains(t, sys, "MAIN SYSTEM")
	_, ok = workerContext(projectContext{system: "x"}, mainRoot, same)
	assert.Eq(t, ok, false)
}

func TestWorkerContext_ProjectSkillsCompared(t *testing.T) {
	mk := func(body string) string {
		d := t.TempDir()
		sk := filepath.Join(d, ".agents", "skills", "s")
		assert.NoErr(t, os.MkdirAll(sk, 0o755))
		assert.NoErr(t, os.WriteFile(filepath.Join(sk, "SKILL.md"), []byte("---\nname: s\ndescription: d\n---\n"+body), 0o644))
		return d
	}
	_ = skills.MaxCatalogBytes
	a, b, c := mk("one"), mk("one"), mk("two")
	main := projectContext{system: "M", trusted: true}
	_, ok := workerContext(main, a, b)
	assert.Eq(t, ok, true)
	_, ok = workerContext(main, a, c)
	assert.Eq(t, ok, false)
}

func loadCtx(t *testing.T, root, home string, interactive, flagTrust bool, input string) projectContext {
	t.Helper()
	pc, err := loadProjectContext(context.Background(), root, home, nil, nil, interactive, flagTrust,
		bufio.NewReader(strings.NewReader(input)), &bytes.Buffer{}, func(s string) string { return s }, func(string) {})
	assert.NoErr(t, err)
	return pc
}

func TestTrust_FarmYAMLChangeReprompts(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("rules\n"), 0o644))
	store := core.TrustStore{Path: filepath.Join(home, trustFile)}
	assert.NoErr(t, store.Trust(root, []byte("rules\n")))
	assert.Eq(t, loadCtx(t, root, home, false, false, "").trusted, true)
	assert.NoErr(t, os.MkdirAll(filepath.Join(root, ".jas"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(root, ".jas", "farm.yaml"), []byte("check: rm -rf x\n"), 0o644))
	assert.Eq(t, loadCtx(t, root, home, false, false, "").trusted, false)
	assert.Eq(t, loadCtx(t, root, home, true, false, "n\n").trusted, false)
	assert.Eq(t, loadCtx(t, root, home, true, false, "y\n").trusted, true)
}

func TestTrust_FarmYAMLOnlyRepo(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	assert.NoErr(t, os.MkdirAll(filepath.Join(root, ".jas"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(root, ".jas", "farm.yaml"), []byte("check: go test ./...\n"), 0o644))
	assert.Eq(t, loadCtx(t, root, home, false, false, "").trusted, false)
	assert.Eq(t, loadCtx(t, root, home, true, false, "y\n").trusted, true)
	assert.Eq(t, loadCtx(t, root, home, false, true, "").trusted, true)
}

func TestTrust_BundleUnchangedWithoutFarmYAML(t *testing.T) {
	assert.Eq(t, string(trustBundle([]byte("rules\n"), nil, nil)), "rules\n")
}

func planRes(goal string) farm.PlannerResult {
	y := "task: t\nparts:\n  - id: a\n    role: coder\n    goal: " + goal + "\n    owns: [\"core/**\"]\n"
	p, _ := farm.ParsePlan([]byte(y))
	return farm.PlannerResult{Raw: []byte(y), Plan: p}
}

func TestReview_RedactsAndSanitizesPlannerText(t *testing.T) {
	var out bytes.Buffer
	d := reviewDeps("q\n", &out)
	d.Redact = newRedactor(testKey).Redact
	res := planRes("'use " + testKey + " \\u001b[31mred'")
	res.Plan.Parts[0].Goal = "use " + testKey + " \x1b[31mred"
	res.Problems = []farm.Problem{{Message: "bad \x1b[2J " + testKey}}
	_, _, err := reviewPlan(context.Background(), d, res, filepath.Join(t.TempDir(), "p"))
	assert.NoErr(t, err)
	assert.NotContains(t, out.String(), testKey)
	assert.NotContains(t, out.String(), "\x1b")
	assert.Contains(t, out.String(), "bad")
}

func finishEnv(input string) *env {
	return &env{interactive: true, stdin: bufio.NewReader(strings.NewReader(input)), red: newRedactor(testKey)}
}

func TestFinishPlan_ProblemsOnTTYGoToReview(t *testing.T) {
	flog, err := core.OpenEventLog(filepath.Join(t.TempDir(), "farm.jsonl"), nil, nil)
	assert.NoErr(t, err)
	defer flog.Close()
	res := planRes("g")
	res.Problems = []farm.Problem{{Message: "still bad"}}
	_, approved, err := finishPlan(context.Background(), finishEnv("a\nq\n"), planArgs{out: filepath.Join(t.TempDir(), "p.yaml")}, nil, farm.PlannerInput{}, flog, res)
	assert.NoErr(t, err)
	assert.Eq(t, approved, false)
	e := finishEnv("")
	e.interactive = false
	_, _, err = finishPlan(context.Background(), e, planArgs{out: filepath.Join(t.TempDir(), "p.yaml")}, nil, farm.PlannerInput{}, flog, res)
	var ee *core.ExitError
	if !errors.As(err, &ee) || ee.Code != 2 {
		t.Fatalf("want exit 2, got %v", err)
	}
}

func TestFinishPlan_ReplanRefusalExit8(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "farm.jsonl")
	flog, err := core.OpenEventLog(logPath, nil, nil)
	assert.NoErr(t, err)
	replan := func(context.Context, string) (farm.PlannerResult, error) {
		return farm.PlannerResult{Refusal: &farm.Refusal{Reason: "small_change", Detail: "tiny"}}, nil
	}
	_, approved, err := finishPlan(context.Background(), finishEnv("r\nmake it small\n"), planArgs{task: "x", out: filepath.Join(t.TempDir(), "p.yaml")}, replan, farm.PlannerInput{}, flog, planRes("g"))
	assert.Eq(t, approved, false)
	var ee *core.ExitError
	if !errors.As(err, &ee) || ee.Code != 8 {
		t.Fatalf("want exit 8, got %v", err)
	}
	flog.Close()
	ev := testutil.ReadEvents(t, logPath)
	assert.Eq(t, ev[len(ev)-1]["type"].(string), farm.EvFarmPlanRefused)
}
