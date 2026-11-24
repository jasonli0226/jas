package main

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"

	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/skills"
)

// hashedSkill is one skill as profileHash sees it: its name and the sha256
// of its SKILL.md.
type hashedSkill struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// hashedProfile is profileHash's canonical input. encoding/json writes map
// keys sorted, so the encoding is deterministic.
type hashedProfile struct {
	Instructions string                            `json:"instructions"`
	Skills       []hashedSkill                     `json:"skills"`
	Values       map[string]string                 `json:"values"`
	Models       map[string]settings.ModelOverride `json:"models"`
}

// profileHash is the receipt's profile_hash: "sha256:" and the hex SHA-256
// of what the flattened profile adds to a session — instructions, the
// profile instructions as placed in the prompt; profileSkills, the chain's
// effective skills (after exclude; no project or built-in skills) as
// (name, SKILL.md sha256) sorted by name; and merged, the chain's settings
// (see mergedProfileSettings). It holds no paths and no profile.yaml
// metadata, so the same profile hashes the same on any machine.
func profileHash(instructions string, profileSkills []skills.Skill, merged settings.File) string {
	in := hashedProfile{Instructions: instructions, Skills: make([]hashedSkill, 0, len(profileSkills)),
		Values: map[string]string{}, Models: map[string]settings.ModelOverride{}}
	maps.Copy(in.Values, merged.Values) // a nil map would encode as null, not {}
	maps.Copy(in.Models, merged.Models)
	for _, s := range profileSkills {
		sum := sha256.Sum256(s.Content)
		in.Skills = append(in.Skills, hashedSkill{Name: s.Name, SHA256: hex.EncodeToString(sum[:])})
	}
	slices.SortFunc(in.Skills, func(a, b hashedSkill) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.SHA256, b.SHA256))
	})
	b, _ := json.Marshal(in) // strings, ints and maps of them always encode
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// mergedProfileSettings is the chain's settings as the session reads them
// from l (after forbidden-key stripping): the parents' Values and Models
// root first, then the active profile's, each overriding the one before.
func mergedProfileSettings(l settings.Layers) settings.File {
	out := settings.File{Values: map[string]string{}, Models: map[string]settings.ModelOverride{}}
	files := []settings.File{l.Profile}
	for _, p := range l.ProfileParents { // nearest first
		files = append(files, p.File)
	}
	for _, f := range slices.Backward(files) {
		maps.Copy(out.Values, f.Values)
		maps.Copy(out.Models, f.Models)
	}
	return out
}

// hashProfile is profileHash for prof as a session loads it: its AGENTS.md
// text as profileAgentsText places it in the prompt, chain (the chain's
// skills, see discoverUserSkills) and the profile settings in layers.
func hashProfile(prof profileState, chain []skills.Skill, layers settings.Layers, redact func(string) string) string {
	return profileHash(profileAgentsText(prof, redact), chain, mergedProfileSettings(layers))
}

// withProfileReceipt adds the receipt's profile fields to an event payload
// and returns it: profile_hash when a profile is loaded, and profile_chain
// (names, root first) when the profile extends another.
func (e *env) withProfileReceipt(meta map[string]any) map[string]any {
	if e.profHash != "" {
		meta["profile_hash"] = e.profHash
	}
	if names := e.prof.names(); len(names) > 1 {
		meta["profile_chain"] = names
	}
	return meta
}
