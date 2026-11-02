package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/skills"
)

const trustFile = "trust.json"

// contextFileMeta is one session_started.context_files entry.
type contextFileMeta struct {
	Path   string `json:"path"`
	Loaded bool   `json:"loaded"`
	SHA256 string `json:"sha256"`
}

// skillMeta is one session_started.skills entry.
type skillMeta struct {
	Name     string `json:"name"`
	Scope    string `json:"scope"`
	Location string `json:"location"`
}

// projectContext is what the project and the skills directories add to a run.
type projectContext struct {
	system string         // full system prompt
	skills []skills.Skill // skills listed to the model
	files  []contextFileMeta
	// trusted is the project trust decision: the context file and project
	// skills were loaded (or --trust-project was given). farm config follows it.
	trusted bool
}

// loadProjectContext finds the project context file, applies the trust gate
// and builds the system prompt. One decision covers the context file and
// every project skill, since both put project-authored instructions in the
// prompt; user skills need no trust. Project skills join (and on a name
// clash override) user skills only once trusted. Context file content is
// redacted and capped, and the skill catalog redacted and size-bounded,
// before either reaches the prompt.
func loadProjectContext(ctx context.Context, root, home string, project, user []skills.Skill, interactive, flagTrust bool,
	in *bufio.Reader, out io.Writer, redact func(string) string, warn func(string)) (projectContext, error) {
	path, content, hasFile := core.FindContextFile(root)
	pc := projectContext{skills: user, files: []contextFileMeta{}, trusted: flagTrust}
	farmCfg := readFarmConfig(root)
	instructions := ""
	if hasFile || len(project) > 0 || farmCfg != nil {
		store := core.TrustStore{Path: filepath.Join(home, trustFile)}
		load, err := core.DecideContextFile(trustLabel(path, len(project), farmCfg != nil), root, trustBundle(content, project, farmCfg), store,
			interactive, flagTrust, func(p string) string { return askLine(ctx, in, out, p) })
		if err != nil {
			return projectContext{}, err
		}
		pc.trusted = load
		if hasFile {
			pc.files = append(pc.files, contextFileMeta{Path: path, Loaded: load, SHA256: core.ContentHash(content)})
			if load {
				instructions = fmt.Sprintf("Project instructions (%s):\n%s", filepath.Base(path), core.CapContext(redact(string(content))))
			}
		}
		for _, s := range project {
			pc.files = append(pc.files, contextFileMeta{Path: s.Location, Loaded: load, SHA256: core.ContentHash(s.Content)})
		}
		if load {
			var clashes []string
			pc.skills, clashes = skills.Merge(project, user)
			for _, w := range clashes {
				warn(w)
			}
		}
	}
	kept, dropped := skills.Fit(pc.skills, skills.MaxCatalogBytes)
	if len(dropped) > 0 {
		warn(fmt.Sprintf("skill list over %d bytes; not listing %d skills: %s", skills.MaxCatalogBytes, len(dropped), strings.Join(dropped, ", ")))
	}
	pc.skills = kept
	pc.system = core.BuildSystem(core.BasePrompt, redact(skills.Catalog(pc.skills)), instructions)
	return pc, nil
}

// trustLabel names what the trust prompt covers, e.g. "AGENTS.md and 2 project skills".
func trustLabel(path string, projectSkills int, farmCfg bool) string {
	var parts []string
	if path != "" {
		parts = append(parts, filepath.Base(path))
	}
	switch projectSkills {
	case 0:
	case 1:
		parts = append(parts, "1 project skill")
	default:
		parts = append(parts, fmt.Sprintf("%d project skills", projectSkills))
	}
	if farmCfg {
		parts = append(parts, ".jas/farm.yaml")
	}
	return strings.Join(parts, " and ")
}

// trustBundle is the content the trust store hashes: the context file, then
// each project skill's location and SKILL.md, length-prefixed so no split of
// the bytes reads as another. With no project skills it is the context file
// alone, so trust recorded before skills existed still holds.
func trustBundle(content []byte, project []skills.Skill, farmCfg []byte) []byte {
	b := append([]byte{}, content...)
	for _, s := range project {
		b = fmt.Appendf(b, "\x00skill %d:%s %d:", len(s.Location), s.Location, len(s.Content))
		b = append(b, s.Content...)
	}
	if farmCfg != nil { // absent leaves the bytes as they were before farm existed
		b = fmt.Appendf(b, "\x00farm.yaml %d:", len(farmCfg))
		b = append(b, farmCfg...)
	}
	return b
}

// readFarmConfig returns the project's .jas/farm.yaml, or nil when absent.
// Its check, setup and role prompts run code, so it joins the trust decision.
func readFarmConfig(root string) []byte {
	b, err := os.ReadFile(filepath.Join(root, ".jas", "farm.yaml"))
	if err != nil {
		return nil
	}
	if b == nil {
		b = []byte{}
	}
	return b
}

// listedSkills returns the session_started.skills entries and the skill
// directories read-only tools may open: only skills shown to the model.
func listedSkills(ss []skills.Skill) ([]skillMeta, []string) {
	meta, dirs := []skillMeta{}, []string{}
	for _, s := range ss {
		if s.DisableModelInvocation {
			continue
		}
		meta = append(meta, skillMeta{Name: s.Name, Scope: s.Scope.String(), Location: s.Location})
		dirs = append(dirs, s.Dir)
	}
	return meta, dirs
}

// askLine writes prompt to out and reads one line from in. It returns "" (a
// refusal) when ctx is cancelled first; the reader goroutine is abandoned,
// which is fine because a cancelled run ends the process.
func askLine(ctx context.Context, in *bufio.Reader, out io.Writer, prompt string) string {
	ch := make(chan string, 1)
	go func() {
		_, _ = io.WriteString(out, prompt)
		line, _ := in.ReadString('\n')
		ch <- line
	}()
	select {
	case <-ctx.Done():
		return ""
	case line := <-ch:
		return line
	}
}

// workerContext is the system prompt and trust decision for a farm worker in
// worktree. The supervisor decided trust once for the main checkout; the
// worker inherits it only when its context file and project skills hash-equal
// main's. Otherwise it gets the base prompt with no project context.
// .jas/farm.yaml is left out of the comparison: workers never run it, and an
// uncommitted farm.yaml in main would otherwise strip every worker's context.
func workerContext(main projectContext, mainRoot, worktree string) (system string, trusted bool) {
	if main.trusted && workerBundleHash(mainRoot) == workerBundleHash(worktree) {
		return main.system, true
	}
	return core.BuildSystem(core.BasePrompt, ""), false
}

// workerBundleHash hashes what a worker loads from root: the context file and
// every project skill. Unlike the trust-prompt bundle it excludes farm.yaml.
func workerBundleHash(root string) string {
	_, content, _ := core.FindContextFile(root)
	project, _ := skills.Discover(skills.ProjectSources(root), func(string) bool { return false })
	for i := range project { // locations are absolute; compare them relative to root
		if rel, err := filepath.Rel(root, project[i].Location); err == nil {
			project[i].Location = filepath.ToSlash(rel)
		}
	}
	return core.ContentHash(trustBundle(content, project, nil))
}
