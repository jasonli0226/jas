package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/skills"
)

const trustFile = "trust.json"

// trustVersion tags trust entries hashed from the framed trustBundle. Older
// entries hash raw bytes and never match a tagged one.
const trustVersion = "v2"

// trustStore is the store of trustBundle approvals under home.
func trustStore(home string) core.TrustStore {
	return core.TrustStore{Path: filepath.Join(home, trustFile), Version: trustVersion}
}

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

// projectTrust is one trust decision and the context file it covered.
type projectTrust struct {
	path    string
	content []byte
	hasFile bool
	trusted bool
}

// loadProjectContext decides project trust and builds the project context.
// prepare splits the two so project settings can follow the decision.
func loadProjectContext(root, home string, project, user []skills.Skill, interactive, flagTrust bool,
	ask func(string) string, redact func(string) string, warn func(string)) (projectContext, error) {
	pt, err := decideProjectTrust(root, home, project, interactive, flagTrust, ask)
	if err != nil {
		return projectContext{}, err
	}
	return buildProjectContext(pt, project, user, redact, warn), nil
}

// decideProjectTrust applies the trust gate. One decision covers the context
// file, every project skill, .jas/farm.yaml and the project settings files,
// since each puts project-authored instructions, code or settings in a run.
func decideProjectTrust(root, home string, project []skills.Skill, interactive, flagTrust bool, ask func(string) string) (projectTrust, error) {
	path, content, hasFile := core.FindContextFile(root)
	pt := projectTrust{path: path, content: content, hasFile: hasFile, trusted: flagTrust}
	farmCfg := readFarmConfig(root)
	settingsFiles := readProjectSettings(root)
	if !hasFile && len(project) == 0 && farmCfg == nil && len(settingsFiles) == 0 {
		return pt, nil
	}
	store := trustStore(home)
	bundle := trustBundle(content, project, farmCfg, settingsFiles)
	if !flagTrust && len(project) == 0 && farmCfg == nil && len(settingsFiles) == 0 {
		if err := upgradeLegacyTrust(store, root, content, bundle); err != nil {
			return projectTrust{}, err
		}
	}
	load, err := core.DecideContextFile(trustLabel(path, project, farmCfg != nil, settingsFiles), root,
		bundle, store, interactive, flagTrust, ask)
	if err != nil {
		return projectTrust{}, err
	}
	pt.trusted = load
	return pt, nil
}

// upgradeLegacyTrust re-records trust an older jas stored as an untagged
// hash of the raw context file. Only a layout holding nothing but the
// context file qualifies: an old hash of a bundle with skills or farm.yaml
// was ambiguous and has to be asked again.
func upgradeLegacyTrust(store core.TrustStore, root string, content, bundle []byte) error {
	if ok, err := store.Check(root, bundle); err != nil || ok {
		return err
	}
	legacy := core.TrustStore{Path: store.Path}
	ok, err := legacy.Check(root, content)
	if err != nil || !ok {
		return err
	}
	return store.Trust(root, bundle)
}

// buildProjectContext builds the system prompt under the decision pt. User
// skills need no trust; project skills join (and on a name clash override)
// them only once trusted. Context file content is redacted and capped, and
// the skill catalog redacted and size-bounded, before either reaches the
// prompt.
func buildProjectContext(pt projectTrust, project, user []skills.Skill, redact func(string) string, warn func(string)) projectContext {
	pc := projectContext{skills: user, files: []contextFileMeta{}, trusted: pt.trusted}
	instructions := ""
	if pt.hasFile {
		pc.files = append(pc.files, contextFileMeta{Path: pt.path, Loaded: pt.trusted, SHA256: core.ContentHash(pt.content)})
		if pt.trusted {
			instructions = fmt.Sprintf("Project instructions (%s):\n%s", filepath.Base(pt.path), core.CapContext(redact(string(pt.content))))
		}
	}
	for _, s := range project {
		pc.files = append(pc.files, contextFileMeta{Path: s.Location, Loaded: pt.trusted, SHA256: core.ContentHash(s.Content)})
	}
	if pt.trusted {
		var clashes []string
		pc.skills, clashes = skills.Merge(project, user)
		for _, w := range clashes {
			warn(w)
		}
	}
	home := catalogHome()
	kept, dropped := skills.Fit(pc.skills, skills.MaxCatalogBytes, home)
	if len(dropped) > 0 {
		warn(fmt.Sprintf("skill list over %d bytes; not listing %d skills: %s", skills.MaxCatalogBytes, len(dropped), strings.Join(dropped, ", ")))
	}
	pc.skills = kept
	pc.system = core.BuildSystem(core.BasePrompt, redact(skills.Catalog(pc.skills, home)), instructions)
	return pc
}

// trustLabelSkills is how many project skill names the trust prompt lists.
const trustLabelSkills = 5

// trustLabel names what the trust prompt covers, e.g.
// "AGENTS.md and 2 project skills (deploy, lint)". Skill names are
// validated lowercase words, so they are safe to print.
func trustLabel(path string, project []skills.Skill, farmCfg bool, settingsFiles []projectFile) string {
	var parts []string
	if path != "" {
		parts = append(parts, filepath.Base(path))
	}
	if n := len(project); n > 0 {
		names := make([]string, n)
		for i, s := range project {
			names[i] = s.Name
		}
		slices.Sort(names)
		list := strings.Join(names, ", ")
		if n > trustLabelSkills {
			list = fmt.Sprintf("%s and %d more", strings.Join(names[:trustLabelSkills], ", "), n-trustLabelSkills)
		}
		noun := "project skills"
		if n == 1 {
			noun = "project skill"
		}
		parts = append(parts, fmt.Sprintf("%d %s (%s)", n, noun, list))
	}
	if farmCfg {
		parts = append(parts, ".jas/farm.yaml")
	}
	for _, f := range settingsFiles {
		parts = append(parts, f.name)
	}
	return strings.Join(parts, " and ")
}

// trustBundle is the content the trust store hashes: the context file, then
// each project skill's location and SKILL.md, .jas/farm.yaml and the settings
// files. Every section is length-prefixed, the context file too, so no split
// of the bytes reads as another (a context file ending in what looks like a
// settings section hashes differently from a real settings file).
func trustBundle(content []byte, project []skills.Skill, farmCfg []byte, settingsFiles []projectFile) []byte {
	b := fmt.Appendf(nil, "context %d:", len(content))
	b = append(b, content...)
	for _, s := range project {
		b = fmt.Appendf(b, "\x00skill %d:%s %d:", len(s.Location), s.Location, len(s.Content))
		b = append(b, s.Content...)
	}
	if farmCfg != nil {
		b = fmt.Appendf(b, "\x00farm.yaml %d:", len(farmCfg))
		b = append(b, farmCfg...)
	}
	for _, f := range settingsFiles {
		b = fmt.Appendf(b, "\x00%s %d:", f.name, len(f.content))
		b = append(b, f.content...)
	}
	return b
}

// currentTrustBundle is what the trust prompt would hash for root now.
func currentTrustBundle(root string, denied func(string) bool) []byte {
	_, content, _ := core.FindContextFile(root)
	project, _ := skills.Discover(skills.ProjectSources(root), denied)
	return trustBundle(content, project, readFarmConfig(root), readProjectSettings(root))
}

// catalogHome is the home directory the skill catalog writes as ~/,
// symlink-resolved like skill locations; "" (no home) keeps paths absolute.
func catalogHome() string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(h); err == nil {
		return r
	}
	return h
}

// projectFile is a file under .jas that joins the trust decision.
type projectFile struct {
	name    string // as the trust prompt shows it, e.g. ".jas/config.json"
	content []byte
}

// readProjectSettings returns the project and local settings files that
// exist. Both live in the repository (a clone can ship config.local.json
// too), so either can change the model, approvals or redaction.
func readProjectSettings(root string) []projectFile {
	if root == "" {
		return nil
	}
	var out []projectFile
	for _, p := range []string{settings.ProjectConfigPath(root), settings.LocalConfigPath(root)} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		out = append(out, projectFile{name: ".jas/" + filepath.Base(p), content: b})
	}
	return out
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
	if r, err := filepath.EvalSymlinks(root); err == nil { // Rel needs the root resolved too
		root = r
	}
	_, content, _ := core.FindContextFile(root)
	project, _ := skills.Discover(skills.ProjectSources(root), func(string) bool { return false })
	for i := range project { // locations are absolute; compare them relative to root
		if rel, err := filepath.Rel(root, project[i].Location); err == nil {
			project[i].Location = filepath.ToSlash(rel)
		}
	}
	return core.ContentHash(trustBundle(content, project, nil, nil))
}
