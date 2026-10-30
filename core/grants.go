package core

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// envAssign matches a leading VAR=value word, which makes a command non-simple.
var envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// Grants holds the approvals a user gave for the rest of a session: the shell
// commands that start with an approved pair of words, and whole edit/write
// tools. It is safe for concurrent use.
type Grants struct {
	windows bool

	mu    sync.RWMutex
	items map[string]string // internal key -> display grant
}

// NewGrants returns an empty set. With windows true, cmd.exe metacharacters
// (^ and %) also make a shell command non-simple.
func NewGrants(windows bool) *Grants {
	return &Grants{windows: windows, items: map[string]string{}}
}

// shellKey returns the internal key and display text for a shell call: its
// first two words (one if there is only one). ok is false for anything that
// is not a simple, parseable command.
func (g *Grants) shellKey(args json.RawMessage) (key, display string, ok bool) {
	var a struct{ Command string }
	if json.Unmarshal(args, &a) != nil {
		return "", "", false
	}
	if !g.simple(a.Command) {
		return "", "", false
	}
	words, wok := ShellWords(a.Command)
	if !wok || len(words) == 0 || envAssign.MatchString(words[0]) {
		return "", "", false
	}
	if len(words) > 2 {
		words = words[:2]
	}
	// NUL cannot appear in a shell command, so it keeps ["a b","c"] and
	// ["a","b c"] apart where the display text would not.
	return "shell\x00" + strings.Join(words, "\x00"), "shell: " + strings.Join(words, " "), true
}

// simple reports whether the raw command text is free of operators that could
// chain, redirect or substitute another command.
func (g *Grants) simple(cmd string) bool {
	bad := ";&|`><\n"
	if g.windows {
		bad += "^%"
	}
	return !strings.ContainsAny(cmd, bad) && !strings.Contains(cmd, "$(")
}

// Covers reports whether a granted approval already allows this call.
func (g *Grants) Covers(tool string, args json.RawMessage) bool {
	var key string
	switch tool {
	case "edit", "write":
		key = tool
	case "shell":
		k, _, ok := g.shellKey(args)
		if !ok {
			return false
		}
		key = k
	default:
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.items[key]
	return ok
}

// Add records a grant for this call and returns its display text. ok is false
// when the call can't be granted: a non-simple or unparseable shell command,
// or a tool other than shell, edit or write.
func (g *Grants) Add(tool string, args json.RawMessage) (grant string, ok bool) {
	var key string
	switch tool {
	case "edit", "write":
		key, grant = tool, tool
	case "shell":
		key, grant, ok = g.shellKey(args)
		if !ok {
			return "", false
		}
	default:
		return "", false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.items[key] = grant
	return grant, true
}

// List returns the display text of every grant, sorted.
func (g *Grants) List() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, 0, len(g.items))
	for _, d := range g.items {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// Remove drops the grant with this display text and reports whether one existed.
func (g *Grants) Remove(grant string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	found := false
	for k, d := range g.items {
		if d == grant {
			delete(g.items, k)
			found = true
		}
	}
	return found
}
