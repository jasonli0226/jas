package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/tui"
)

// switchProfileSource is the source of a profile picked with /profile.
const switchProfileSource = settings.Source("/profile")

// liveSettings are the keys the chat applies to a running session (tui
// applyLive); every other key waits for /new.
var liveSettings = []string{"approve", "model", "hints", "farm_bell"}

// Profile is the chat's active profile name.
func (b *chatBackend) Profile() string { return b.e.activeProfile() }

// Profiles lists the profiles under the jas home, default included. An
// unreadable profiles directory lists only the active one.
func (b *chatBackend) Profiles() []string {
	names, err := settings.ListProfiles(b.e.home)
	if err != nil {
		return []string{b.e.activeProfile()}
	}
	return names
}

// SetDefaultProfile stores name as profile in the global config.json, so a
// new jas starts on it. The running chat stays on its active profile.
func (b *chatBackend) SetDefaultProfile(name string) error {
	if !b.knownProfile(name) {
		return fmt.Errorf("unknown profile %q", name)
	}
	return b.writeSetting(settings.ScopeGlobal, "profile", name)
}

// knownProfile reports whether name is default or an existing profile.
func (b *chatBackend) knownProfile(name string) bool {
	return name == settings.DefaultProfile || profileExists(b.e.home, name)
}

// SwitchProfile makes name the chat's active profile for the rest of the
// chat: the profile's settings layer replaces the old one, then s is
// reloaded like /reload (system prompt, read roots and the profile read
// exception) and profile_changed is logged. When loading fails env is left
// as it was; a log error after the switch applied is returned with it,
// like Reload's. Like Reload it runs from a tea.Cmd, never from Update.
func (b *chatBackend) SwitchProfile(s *core.Session, name string, ask func(string) string) (tui.ProfileSwitch, error) {
	if !b.knownProfile(name) {
		return tui.ProfileSwitch{}, fmt.Errorf("unknown profile %q", name)
	}
	from := b.e.activeProfile()
	saved := *b.e
	sw, roots, err := b.loadSwitch(name, ask)
	if err != nil {
		*b.e = saved
		return tui.ProfileSwitch{}, err
	}
	e := b.e
	b.publishWorkerProfile()
	b.publishMemoryGuard()
	files := contextFilesMeta(e.pc.files)
	s.SetReadRoots(roots)
	s.SetProfileSkills(e.policy.ProfileSkills)
	s.SetProfileMemory(e.policy.ProfileMemory)
	err = s.SetSystem(e.pc.system, map[string]any{"context_files": files, "skills": e.skillList})
	if lerr := s.LogProfileChanged(map[string]any{
		"from": from, "to": e.activeProfile(), "context_files": files, "skills": e.skillList, "deferred": sw.Deferred,
	}); err == nil {
		err = lerr
	}
	return sw, err
}

// loadSwitch moves env onto profile name: its settings layer, the settings
// rebuilt from it, then reloadContext's skills, AGENTS.md and read roots.
func (b *chatBackend) loadSwitch(name string, ask func(string) string) (tui.ProfileSwitch, []string, error) {
	e := b.e
	prof, warnings, err := loadProfile(e.home, name, switchProfileSource)
	if err != nil {
		return tui.ProfileSwitch{}, nil, err
	}
	before := settings.Resolve(e.flags, os.Getenv, e.layers)
	e.files = e.files.WithProfile(prof.name, prof.layer)
	var lw []string
	e.layers, lw = e.files.ForProject(e.settingsTrusted)
	warnings = append(warnings, lw...)
	if err := b.reconfigure(); err != nil {
		return tui.ProfileSwitch{}, nil, err
	}
	e.profile, e.profileSrc, e.prof = prof.name, switchProfileSource, prof
	notices, roots, err := e.reloadContext(ask) // re-reads the profile AGENTS.md and skills
	if err != nil {
		return tui.ProfileSwitch{}, nil, err
	}
	live, deferred := settingChanges(before, settings.Resolve(e.flags, os.Getenv, e.layers))
	notices[0] = "profile " + prof.name + strings.TrimPrefix(notices[0], "reloaded")
	for _, w := range warnings {
		notices = append(notices, "jas: "+core.Sanitize(e.red.Redact(w)))
	}
	return tui.ProfileSwitch{Notices: notices, Live: live, Deferred: deferred}, roots, nil
}

// settingChanges compares the effective settings around a profile switch:
// live holds the new values of the live keys that changed, deferred names
// the other changed keys in settings.Keys order. profile itself is left
// out: the switch is for this chat only and never rewrites it. memory is
// left out too: the switch reloads it (reloadContext) on the spot.
func settingChanges(before, after settings.Effective) (live map[string]string, deferred []string) {
	live, deferred = map[string]string{}, []string{}
	for _, k := range settings.Keys {
		if k.Name == "profile" || k.Name == memoryKey || before[k.Name].Raw == after[k.Name].Raw {
			continue
		}
		if slices.Contains(liveSettings, k.Name) {
			live[k.Name] = after[k.Name].Raw
		} else {
			deferred = append(deferred, k.Name)
		}
	}
	return live, deferred
}

// contextFilesMeta is the context_files payload of context_reloaded and
// profile_changed.
func contextFilesMeta(fs []contextFileMeta) []map[string]any {
	out := make([]map[string]any, len(fs))
	for i, f := range fs {
		out[i] = map[string]any{"path": f.Path, "loaded": f.Loaded, "sha256": f.SHA256}
	}
	return out
}

// workerProfile is the chat's profile context as a farm worker starts on it:
// the project context (system prompt, skills, trust), its memory and the
// read fields of the exec policy. It is never changed once published.
type workerProfile struct {
	name      string
	pc        projectContext
	skillList []skillMeta
	mem       memoryState
	// profileSkills, profileMemory, homeDeny and readRoots are ExecPolicy's
	// read exception.
	profileSkills, profileMemory, homeDeny string
	readRoots                              []string
}

// publishWorkerProfile snapshots env for farm workers started from now on.
// It runs in the chat's tea.Cmd; workers read it from farm goroutines.
func (b *chatBackend) publishWorkerProfile() {
	e := b.e
	b.workerProf.Store(&workerProfile{
		name: e.activeProfile(), pc: e.pc, skillList: e.skillList, mem: e.mem,
		profileSkills: e.policy.ProfileSkills, profileMemory: e.policy.ProfileMemory, homeDeny: e.policy.HomeDeny,
		readRoots: slices.Clone(e.policy.ReadRoots),
	})
}

// workerSession is farmSession for a chat farm over e (the env the farm
// started from): each worker starts on the chat's profile as it is when the
// worker starts, so a profile switch reaches the workers started after it.
// Workers already running keep theirs.
func (b *chatBackend) workerSession(e *env) func(farm.WorkerSession) (*core.Session, error) {
	return func(ws farm.WorkerSession) (*core.Session, error) {
		p := b.workerProf.Load()
		if p == nil {
			return farmSession(e)(ws)
		}
		we := *e
		we.profile, we.pc, we.skillList, we.mem = p.name, p.pc, p.skillList, p.mem
		ws.Policy.ProfileSkills, ws.Policy.ProfileMemory, ws.Policy.HomeDeny = p.profileSkills, p.profileMemory, p.homeDeny
		ws.Policy.ReadRoots = slices.Clone(p.readRoots)
		return farmSession(&we)(ws)
	}
}
