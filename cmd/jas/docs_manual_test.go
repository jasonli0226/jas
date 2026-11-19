package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/settings"
)

// manualPath is docs/manual.md, relative to this package.
var manualPath = filepath.Join("..", "..", "docs", "manual.md")

// TestManual_CoversEverything fails when docs/manual.md misses a CLI flag, a
// setting, a chat command, an exit code or an event type, so the manual
// cannot drift from the binary. Sources are read from the code itself.
func TestManual_CoversEverything(t *testing.T) {
	manual := readFile(t, manualPath)
	want := map[string]string{} // text that must appear → what it is

	// CLI flags: every flag registered on a FlagSet in this package, plus
	// the per-run settings flags.
	flagRE := regexp.MustCompile(`fs\.(?:Bool|String|Int)(?:Var)?\((?:&[\w.]+, )?"([\w-]+)"`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		for _, m := range flagRE.FindAllStringSubmatch(readFile(t, f), -1) {
			want["-"+m[1]] = "flag in " + f
		}
	}
	for _, k := range settings.Keys {
		want["`"+k.Name+"`"] = "setting"
		if k.Flag {
			want["--"+strings.ReplaceAll(k.Name, "_", "-")] = "settings flag"
		}
	}

	// Chat slash commands: the keys of tui's commands map.
	cmdRE := regexp.MustCompile(`(?m)^\t"(\w+)":\s+(?:func|\(\*Model\))`)
	for _, m := range cmdRE.FindAllStringSubmatch(readFile(t, filepath.Join("..", "..", "tui", "commands.go")), -1) {
		want["`/"+m[1]] = "chat command"
	}

	// Exit codes and event types from core.
	exitRE := regexp.MustCompile(`(?m)^\tExit\w+\s*=\s*(\d+)`)
	for _, m := range exitRE.FindAllStringSubmatch(readFile(t, filepath.Join("..", "..", "core", "exit.go")), -1) {
		want["| "+m[1]+" |"] = "exit code"
	}
	evRE := regexp.MustCompile(`(?m)^\tEv\w+\s*=\s*"(\w+)"`)
	for _, m := range evRE.FindAllStringSubmatch(readFile(t, filepath.Join("..", "..", "core", "events.go")), -1) {
		want["`"+m[1]+"`"] = "event type"
	}

	if len(want) < 50 { // the regexps above stopped matching the code
		t.Fatalf("only %d items collected; update the patterns in this test", len(want))
	}
	for text, what := range want {
		if !strings.Contains(manual, text) {
			t.Errorf("docs/manual.md is missing %s %s", what, text)
		}
	}
}

// TestUsage_ListsEverySubcommand fails when a subcommand dispatch routes is
// missing from the top-level usage text.
func TestUsage_ListsEverySubcommand(t *testing.T) {
	src := readFile(t, "main.go")
	body := src[strings.Index(src, "func dispatch("):]
	end := strings.Index(body, "\n}\n")
	if end < 0 {
		t.Fatal("end of dispatch not found in main.go")
	}
	body = body[:end]
	caseRE := regexp.MustCompile(`case "(\w+)":`)
	n := 0
	for _, m := range caseRE.FindAllStringSubmatch(body, -1) {
		if m[1] == "version" {
			continue // jas --version is listed
		}
		n++
		if !strings.Contains(usage, "jas "+m[1]) {
			t.Errorf("usage is missing `jas %s`", m[1])
		}
	}
	if n < 5 {
		t.Fatalf("found only %d subcommands; update the pattern in this test", n)
	}
}

// The built-in jas skill ships the manual so the model can answer from it in
// any project; the copy must match docs/manual.md.
func TestManual_SkillCopyMatches(t *testing.T) {
	if readFile(t, manualPath) != readFile(t, filepath.Join("manual", "jas", "references", "manual.md")) {
		t.Fatal("cmd/jas/manual/jas/references/manual.md differs from docs/manual.md: run `make manual`")
	}
}

// The model often answers from the skill's SKILL.md summary without opening
// the full manual, so the summary must name every chat command and
// subcommand too, or the model may say one does not exist.
func TestManualSkill_SummaryListsCommands(t *testing.T) {
	summary := readFile(t, filepath.Join("manual", "jas", "SKILL.md"))
	cmdRE := regexp.MustCompile(`(?m)^\t"(\w+)":\s+(?:func|\(\*Model\))`)
	cmds := cmdRE.FindAllStringSubmatch(readFile(t, filepath.Join("..", "..", "tui", "commands.go")), -1)
	if len(cmds) < 10 {
		t.Fatalf("found only %d chat commands; update the pattern in this test", len(cmds))
	}
	for _, m := range cmds {
		if !strings.Contains(summary, "`/"+m[1]) {
			t.Errorf("manual/jas/SKILL.md is missing chat command /%s", m[1])
		}
	}

	src := readFile(t, "main.go")
	start := strings.Index(src, "func dispatch(")
	if start < 0 {
		t.Fatal("func dispatch not found in main.go")
	}
	body := src[start:]
	end := strings.Index(body, "\n}\n")
	if end < 0 {
		t.Fatal("end of dispatch not found in main.go")
	}
	body = body[:end]
	for _, m := range regexp.MustCompile(`case "(\w+)":`).FindAllStringSubmatch(body, -1) {
		if m[1] == "version" {
			continue
		}
		if !strings.Contains(summary, "`jas "+m[1]) {
			t.Errorf("manual/jas/SKILL.md is missing subcommand `jas %s`", m[1])
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows checkouts may convert line endings to CRLF.
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}
