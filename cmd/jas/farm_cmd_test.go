package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	in := bufio.NewReader(strings.NewReader(input))
	ask := func(p string) string { return askLine(context.Background(), in, &bytes.Buffer{}, p) }
	pc, err := loadProjectContext(root, home, nil, nil, interactive, flagTrust, ask, func(s string) string { return s }, func(string) {})
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

func TestWorkerContext_IgnoresFarmYAML(t *testing.T) {
	mk := func(farm string) string {
		d := t.TempDir()
		assert.NoErr(t, os.WriteFile(filepath.Join(d, "AGENTS.md"), []byte("rules\n"), 0o644))
		if farm != "" {
			assert.NoErr(t, os.MkdirAll(filepath.Join(d, ".jas"), 0o755))
			assert.NoErr(t, os.WriteFile(filepath.Join(d, ".jas", "farm.yaml"), []byte(farm), 0o644))
		}
		return d
	}
	// An uncommitted farm.yaml lives only in main; workers never run it, so
	// it must not strip the project context from every worker.
	mainRoot, worktree := mk("check: go test ./...\n"), mk("")
	main := projectContext{system: "MAIN SYSTEM", trusted: true}
	sys, ok := workerContext(main, mainRoot, worktree)
	assert.Eq(t, ok, true)
	assert.Eq(t, sys, "MAIN SYSTEM")
}

// approvedPlan writes an approved two-part plan on HEAD outside the repo.
func approvedPlan(t *testing.T, repo string) string {
	t.Helper()
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	assert.NoErr(t, err)
	raw := strings.Replace(farmPlan, "task: t\n", "task: t\nbase: "+strings.TrimSpace(string(head))+"\n", 1)
	path := filepath.Join(t.TempDir(), "plan.yaml")
	assert.NoErr(t, os.WriteFile(path, farm.Approve([]byte(raw), "owner", time.Now()), 0o644))
	return path
}

func farmRunEnv(url, home string, t *testing.T) []string {
	return append(farmEnv(url, home), "XDG_CACHE_HOME="+t.TempDir())
}

func TestFarmRun_JSONOnlyFarmEvents(t *testing.T) {
	repo, home := farmRepo(t), farmHome(t, "true")
	plan := approvedPlan(t, repo)
	url, reqs := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a done"}, {Text: "b done"}})
	out, errOut, code := runJasIn(t, repo, farmRunEnv(url, home, t), "farm", "run", plan, "--json", "--trust-project")
	if code != 0 {
		t.Fatalf("exit %d; stderr %s; stdout %s", code, errOut, out)
	}
	assert.Eq(t, len(reqs()), 2)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var types []string
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("stdout line is not JSON: %q", l)
		}
		if m["farm_id"] == nil || m["farm_id"] == "" {
			t.Fatalf("line without farm_id: %q", l)
		}
		types = append(types, m["type"].(string))
	}
	for _, typ := range types {
		if strings.HasPrefix(typ, "session_") || typ == core.EvAssistantMessage || typ == core.EvUserMessage {
			t.Fatalf("session event %s on stdout", typ)
		}
	}
	assert.Eq(t, types[0], farm.EvFarmStarted)
	assert.Eq(t, types[len(types)-1], farm.EvFarmEnded)
	dirs := farmDirs(t, home)
	assert.Eq(t, len(dirs), 1)
	for _, f := range []string{"plan.yaml", "farm.jsonl", "manifest.json", filepath.Join("parts", "a", "events.jsonl")} {
		if _, err := os.Stat(filepath.Join(dirs[0], f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
}

func TestFarmRun_HashMismatchExit2(t *testing.T) {
	repo, home := farmRepo(t), farmHome(t, "true")
	plan := approvedPlan(t, repo)
	b, err := os.ReadFile(plan)
	assert.NoErr(t, err)
	assert.NoErr(t, os.WriteFile(plan, []byte(strings.Replace(string(b), "goal: g", "goal: h", 1)), 0o644))
	url, reqs := testutil.FakeOpenAI(t, nil)
	_, errOut, code := runJasIn(t, repo, farmRunEnv(url, home, t), "farm", "run", plan, "--trust-project")
	assert.Eq(t, code, 2)
	assert.Contains(t, errOut, "hash")
	assert.Eq(t, len(reqs()), 0)
	assert.Eq(t, len(farmDirs(t, home)), 0)
}

func TestFarmRun_UsageNeedsOnePlan(t *testing.T) {
	repo, home := farmRepo(t), farmHome(t, "true")
	_, errOut, code := runJasIn(t, repo, farmEnv("http://127.0.0.1:1", home), "farm", "run")
	assert.Eq(t, code, 2)
	assert.Contains(t, errOut, "plan.yaml")
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// writeManifest saves a minimal manifest for id under home's farms directory.
func writeManifest(t *testing.T, home, id string, m *farm.Manifest) {
	t.Helper()
	assert.NoErr(t, m.Save(filepath.Join(home, farmsDir, id, "manifest.json")))
}

func TestFarmStatus_FromManifest(t *testing.T) {
	repo, home := farmRepo(t), farmHome(t, "")
	id := "20260102T030405-abc123def456"
	writeManifest(t, home, id, &farm.Manifest{
		FarmID: id, PlanSHA256: strings.Repeat("a", 64), Base: strings.Repeat("b", 40),
		Parts: map[string]*farm.PartState{
			"core": {Role: "coder", Branch: "jas/farm/" + id + "/core", Status: "done", Tokens: 1200},
			"docs": {Role: "coder", Branch: "jas/farm/" + id + "/docs", Status: "failed", FailureReason: "write_limit", ExitCode: 1, Tokens: 300},
		},
		Check:       &core.CheckResult{ExitCode: 1, Duration: time.Second, Output: "boom"},
		FinalChoice: "keep", Reason: "check_failed", ExitCode: 6, TotalTokens: 1500,
	})
	url, _ := testutil.FakeOpenAI(t, nil)
	out, _, code := runJasIn(t, repo, farmEnv(url, home), "farm", "status", id)
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "core")
	assert.Contains(t, out, "coder")
	assert.Contains(t, out, "done")
	assert.Contains(t, out, "1200")
	assert.Contains(t, out, "failed")
	assert.Contains(t, out, "write_limit")
	assert.Contains(t, out, "check")
	assert.Contains(t, out, "exit 1")
}

func TestFarmStatus_LatestFarmWhenIDOmitted(t *testing.T) {
	repo, home := farmRepo(t), farmHome(t, "")
	writeManifest(t, home, "20260101T000000-older000001", &farm.Manifest{FarmID: "20260101T000000-older000001",
		Parts: map[string]*farm.PartState{"stale": {Role: "coder", Status: "done"}}})
	writeManifest(t, home, "20260102T000000-newer000001", &farm.Manifest{FarmID: "20260102T000000-newer000001",
		Parts: map[string]*farm.PartState{"fresh": {Role: "coder", Status: "done"}}})
	url, _ := testutil.FakeOpenAI(t, nil)
	out, _, code := runJasIn(t, repo, farmEnv(url, home), "farm", "status")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "fresh")
	if strings.Contains(out, "stale") {
		t.Fatal("status printed an older farm than the latest")
	}
}

func TestFarmStatus_UnknownFarmIDExit2(t *testing.T) {
	repo, home := farmRepo(t), farmHome(t, "")
	url, _ := testutil.FakeOpenAI(t, nil)
	_, errOut, code := runJasIn(t, repo, farmEnv(url, home), "farm", "status", "20260101T000000-nosuch")
	assert.Eq(t, code, 2)
	assert.Contains(t, errOut, "20260101T000000-nosuch")
}

// cleanFarm makes a farm whose parts have real worktrees and branches.
func cleanFarm(t *testing.T, repo, home, id string) (string, []string) {
	t.Helper()
	wtDir := t.TempDir()
	m := &farm.Manifest{FarmID: id, Base: strings.Repeat("c", 40), Parts: map[string]*farm.PartState{}}
	var parts []string
	for _, p := range []string{"core", "docs"} {
		path := filepath.Join(wtDir, p)
		gitIn(t, repo, "worktree", "add", "-q", "-b", "jas/farm/"+id+"/"+p, path)
		m.Parts[p] = &farm.PartState{Role: "coder", Branch: "jas/farm/" + id + "/" + p, Worktree: path, Status: "done"}
		parts = append(parts, p)
	}
	writeManifest(t, home, id, m)
	return wtDir, parts
}

func TestFarmClean_RemovesWorktreesKeepsBranches(t *testing.T) {
	repo, home := farmRepo(t), farmHome(t, "true")
	id := "20260103T000000-clean00001"
	wtDir, parts := cleanFarm(t, repo, home, id)
	url, _ := testutil.FakeOpenAI(t, nil)
	out, _, code := runJasIn(t, repo, farmEnv(url, home), "farm", "clean", id)
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "core")
	refs := gitOut(t, repo, "for-each-ref", "refs/heads/jas/farm/"+id+"/", "--format=%(refname)")
	for _, p := range parts {
		if _, err := os.Stat(filepath.Join(wtDir, p)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("worktree %s still exists", p)
		}
		assert.Contains(t, refs, "jas/farm/"+id+"/"+p)
	}
}

func TestFarmClean_BranchesFlag(t *testing.T) {
	repo, home := farmRepo(t), farmHome(t, "true")
	id := "20260104T000000-clean00002"
	cleanFarm(t, repo, home, id)
	url, _ := testutil.FakeOpenAI(t, nil)
	out, _, code := runJasIn(t, repo, farmEnv(url, home), "farm", "clean", id, "--branches")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "jas/farm/"+id)
	refs := gitOut(t, repo, "for-each-ref", "refs/heads/jas/farm/"+id+"/", "--format=%(refname)")
	assert.Eq(t, refs, "")
}

func TestFarmVars_Sources(t *testing.T) {
	repo := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/t\n\ngo 1.27\n"), 0o644))
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "init")
	assert.NoErr(t, os.MkdirAll(filepath.Join(repo, ".jas"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(repo, ".jas", "farm.yaml"), []byte("vars:\n  lint: golangci-lint run\n"), 0o644))
	home := farmHome(t, "")
	url, _ := testutil.FakeOpenAI(t, nil)

	out, _, code := runJasIn(t, repo, farmEnv(url, home), "farm", "vars", "--trust-project")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "check")
	assert.Contains(t, out, "detected")
	assert.Contains(t, out, "go build ./... && go test ./...")
	assert.Contains(t, out, "test_globs")
	assert.Contains(t, out, "**/*_test.go")
	assert.Contains(t, out, "lint")
	assert.Contains(t, out, "project")
	assert.Contains(t, out, "golangci-lint run")
	assert.Contains(t, out, "build")
	assert.Contains(t, out, "unset")

	// without trust the project vars are skipped with a notice
	_, errOut, code := runJasIn(t, repo, farmEnv(url, home), "farm", "vars")
	assert.Eq(t, code, 0)
	assert.Contains(t, errOut, "untrusted")
}

func TestFarmSession_UsesWorkerUI(t *testing.T) {
	e := testEnv(t, fake.Turn{Text: "hello from a"})
	ui := &textUI{}
	log, err := core.OpenEventLog(filepath.Join(t.TempDir(), "events.jsonl"), nil, nil)
	assert.NoErr(t, err)
	t.Cleanup(func() { log.Close() })
	ws := farm.WorkerSession{Part: farm.Part{ID: "a"}, Root: e.root, Log: log, UI: ui,
		Policy: e.policy, Gate: core.AllowAll{}}
	s, err := farmSession(e)(ws)
	assert.NoErr(t, err)
	_, err = s.Send(context.Background(), "go")
	assert.NoErr(t, err)
	assert.NoErr(t, s.Close("ok", 0))
	assert.Eq(t, ui.b.String(), "hello from a")
}

// textUI collects streamed text.
type textUI struct{ b strings.Builder }

func (u *textUI) Text(d string)     { u.b.WriteString(d) }
func (u *textUI) Reasoning(string)  {}
func (u *textUI) Event(string, any) {}
