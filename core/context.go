package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// MaxContextBytes caps the project context file placed in the system prompt.
const MaxContextBytes = 64 * 1024

// contextFileNames are tried in order in the project root only.
var contextFileNames = []string{"AGENTS.md", "CLAUDE.md"}

// FindContextFile returns AGENTS.md, else CLAUDE.md, from root. A file the
// deny floor blocks (including a symlink that resolves to one, such as
// CLAUDE.md -> .env) is never read.
func FindContextFile(root string) (path string, content []byte, ok bool) {
	policy := ExecPolicy{Root: root}
	for _, name := range contextFileNames {
		p := filepath.Join(root, name)
		if policy.Denied(p) {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		return p, b, true
	}
	return "", nil, false
}

// CapContext limits s to MaxContextBytes on a rune boundary and marks a cut.
func CapContext(s string) string {
	if len(s) <= MaxContextBytes {
		return s
	}
	cut := MaxContextBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n[truncated: %d more bytes omitted]", len(s)-cut)
}

// DecideContext reports whether the context file should be loaded:
// trusted content loads; flagTrust loads without recording; an interactive
// session is asked (y = once, always = remember); otherwise it is skipped
// with a warning on stderr.
func DecideContext(root string, content []byte, store TrustStore, interactive, flagTrust bool, ask func(prompt string) string) (bool, error) {
	return DecideContextFile("AGENTS.md", root, content, store, interactive, flagTrust, ask)
}

// DecideContextFile is DecideContext naming the file in the messages.
func DecideContextFile(name, root string, content []byte, store TrustStore, interactive, flagTrust bool, ask func(prompt string) string) (bool, error) {
	trusted, err := store.Check(root, content)
	if err != nil {
		return false, err
	}
	if trusted || flagTrust {
		return true, nil
	}
	if !interactive || ask == nil {
		fmt.Fprintf(os.Stderr, "jas: skipping untrusted %s (pass --trust-project to load)\n", name)
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(ask(fmt.Sprintf("trust %s and load %s? [y/N/always] ", root, name)))) {
	case "y", "yes":
		return true, nil
	case "always":
		return true, store.Trust(root, content)
	}
	return false, nil
}
