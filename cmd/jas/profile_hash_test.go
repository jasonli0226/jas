package main

import (
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/skills"
)

func hashInputs() (string, []skills.Skill, settings.File) {
	return "Profile instructions (AGENTS.md):\nRULES",
		[]skills.Skill{
			{Name: "sql", Dir: "/a/sql", Content: []byte("SQL")},
			{Name: "tdd", Dir: "/a/tdd", Content: []byte("TDD")},
		},
		settings.File{
			Values: map[string]string{"approve": "shell", "max_turns": "7"},
			Models: map[string]settings.ModelOverride{"m": {ContextWindow: 1000}, "n": {ContextWindow: 2000}},
		}
}

func TestProfileHash_Stable(t *testing.T) {
	text, sk, merged := hashInputs()
	h := profileHash(text, sk, merged)
	assert.Eq(t, strings.HasPrefix(h, "sha256:"), true)
	assert.Eq(t, len(h), len("sha256:")+64)
	assert.Eq(t, profileHash(text, sk, merged), h)

	reversed := []skills.Skill{sk[1], sk[0]}
	reversed[0].Dir, reversed[1].Dir = "/elsewhere/tdd", "/elsewhere/sql" // paths are not hashed
	assert.Eq(t, profileHash(text, reversed, merged), h)
}

func TestProfileHash_Changes(t *testing.T) {
	text, sk, merged := hashInputs()
	h := profileHash(text, sk, merged)

	assert.Eq(t, profileHash(text+"!", sk, merged) != h, true)

	edited := []skills.Skill{sk[0], {Name: "tdd", Content: []byte("TDD v2")}}
	assert.Eq(t, profileHash(text, edited, merged) != h, true)

	_, _, other := hashInputs()
	other.Values["max_turns"] = "8"
	assert.Eq(t, profileHash(text, sk, other) != h, true)

	_, _, other = hashInputs()
	other.Models["m"] = settings.ModelOverride{ContextWindow: 1001}
	assert.Eq(t, profileHash(text, sk, other) != h, true)
}
