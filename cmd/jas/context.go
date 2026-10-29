package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
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
}

// loadProjectContext finds the project context file and the skills, applies
// the trust gate and builds the system prompt. One decision covers the
// context file and every project skill, since both put project-authored
// instructions in the prompt; user skills need no trust. Context file
// content is redacted and capped, and the skill catalog redacted, before
// either reaches the prompt.
func loadProjectContext(ctx context.Context, root, home string, found []skills.Skill, interactive, flagTrust bool,
	in *bufio.Reader, out io.Writer, redact func(string) string) (projectContext, error) {
	path, content, hasFile := core.FindContextFile(root)
	var project, user []skills.Skill
	for _, s := range found {
		if s.Scope == skills.Project {
			project = append(project, s)
		} else {
			user = append(user, s)
		}
	}
	pc := projectContext{skills: user, files: []contextFileMeta{}}
	instructions := ""
	if hasFile || len(project) > 0 {
		store := core.TrustStore{Path: filepath.Join(home, trustFile)}
		load, err := core.DecideContextFile(trustLabel(path, len(project)), root, trustBundle(content, project), store,
			interactive, flagTrust, func(p string) string { return askLine(ctx, in, out, p) })
		if err != nil {
			return projectContext{}, err
		}
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
			pc.skills = found
		}
	}
	pc.system = core.BuildSystem(core.BasePrompt, redact(skills.Catalog(pc.skills)), instructions)
	return pc, nil
}

// trustLabel names what the trust prompt covers, e.g. "AGENTS.md and 2 project skills".
func trustLabel(path string, projectSkills int) string {
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
	return strings.Join(parts, " and ")
}

// trustBundle is the content the trust store hashes: the context file, then
// each project skill's location and SKILL.md. With no project skills it is
// the context file alone, so trust recorded before skills existed still holds.
func trustBundle(content []byte, project []skills.Skill) []byte {
	b := append([]byte{}, content...)
	for _, s := range project {
		b = append(b, "\x00skill "+s.Location+"\n"...)
		b = append(b, s.Content...)
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
