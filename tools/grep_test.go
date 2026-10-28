package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestGrep_SkipsBinaryAndGit(t *testing.T) {
	p := policy(t)
	write(t, p, "a.go", "package a\nneedle here\n")
	write(t, p, ".git/config", "needle\n")
	write(t, p, "bin.dat", "needle\x00\x01")
	write(t, p, "big.txt", strings.Repeat("needle\n", 200000))
	write(t, p, "sub/b.txt", "needle\n")
	r := call(t, Grep{}, p, `{"pattern":"needle"}`)
	assert.Eq(t, r.IsError, false)
	assert.Contains(t, r.Output, "a.go:2:needle here")
	assert.Contains(t, r.Output, "sub/b.txt:1:needle")
	assert.NotContains(t, r.Output, ".git")
	assert.NotContains(t, r.Output, "bin.dat")
	assert.NotContains(t, r.Output, "big.txt")
}

func TestGrep_Glob(t *testing.T) {
	p := policy(t)
	write(t, p, "a.go", "needle\n")
	write(t, p, "b.txt", "needle\n")
	r := call(t, Grep{}, p, `{"pattern":"needle","glob":"*.go"}`)
	assert.Contains(t, r.Output, "a.go:1:needle")
	assert.NotContains(t, r.Output, "b.txt")
}

func TestGrep_MatchFormatAndCap(t *testing.T) {
	p := policy(t)
	var sb strings.Builder
	for i := 0; i < 250; i++ {
		fmt.Fprintf(&sb, "hit %d\n", i)
	}
	write(t, p, "m.txt", sb.String())
	r := call(t, Grep{}, p, `{"pattern":"hit"}`)
	lines := strings.Split(strings.TrimRight(r.Output, "\n"), "\n")
	assert.Eq(t, len(lines), 201)
	assert.Eq(t, lines[0], "m.txt:1:hit 0")
	assert.Eq(t, lines[200], "[200 of 250 matches]")
}

func TestGrep_Errors(t *testing.T) {
	p := policy(t)
	r := call(t, Grep{}, p, `{"pattern":"("}`)
	assert.Eq(t, r.IsError, true)
	r = call(t, Grep{}, p, `{"pattern":"x"}`)
	assert.Eq(t, r.IsError, false)
	assert.Eq(t, r.Output, "no matches")
}

func TestGrep_KeyBodyNotLeaked(t *testing.T) {
	p := policy(t)
	write(t, p, "notes.txt", "head\n-----BEGIN RSA PRIVATE KEY-----\nBODYLINE1AAAA\n-----END RSA PRIVATE KEY-----\nneedle\n")
	r := call(t, Grep{}, p, `{"pattern":"BODYLINE"}`)
	assert.NotContains(t, r.Output, "BODYLINE")
	r = call(t, Grep{}, p, `{"pattern":"needle"}`)
	assert.Contains(t, r.Output, "needle")
}
