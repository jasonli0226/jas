package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// DenyFloor is the always-on deny list for file tools; ExecPolicy.Deny only
// adds to it. Patterns match the resolved path relative to Root, lower-cased
// so case-insensitive filesystems cannot dodge them; absolute patterns match
// the absolute path (jas run adds its own home this way when JAS_HOME moves
// it out of ~/.jas). The "~/" patterns match
// the absolute path under $HOME, so for file tools they only matter when the
// root is or contains $HOME (confinement refuses everything else first);
// otherwise they act through the shell tool's best-effort filter.
var DenyFloor = []string{
	".env*", "**/.env", "**/.env.*", "secrets/**", "**/secrets/**", "**/*.pem", "**/id_rsa*",
	"**/*.p12", "**/.git-credentials", "~/.aws/**", "~/.ssh/**", JasHomeFloor,
}

// JasHomeFloor is the deny floor entry for the jas home (auth, config,
// trust). Read scopes inside the active profile's skills lift it there.
const JasHomeFloor = "~/.jas/**"

// checkDeny returns the policy error when the resolved path is denied.
func (p ExecPolicy) checkDeny(original, resolved string) error {
	if p.Denied(resolved) {
		return fmt.Errorf("%w: %s", errDenied, original)
	}
	return nil
}

var errDenied = errors.New("path denied by policy")

// Denied reports whether abs, or what it resolves to through symlinks, matches
// the deny floor or the policy's extra patterns.
func (p ExecPolicy) Denied(abs string) bool {
	match := p.DenyMatcher()
	if match(abs) {
		return true
	}
	r, err := filepath.EvalSymlinks(abs)
	return err == nil && match(r)
}

// DenyMatcher returns a lexical check for absolute paths with the root and
// home computed once. Use it when walking a tree: WalkDir does not follow
// symlinks, so no resolution is needed. Denied also resolves symlinks.
//
// On a read scope from readRoot or ProfileScope (liftHome), a path inside
// ProfileSkills is checked without JasHomeFloor and HomeDeny; every other
// pattern still applies. The base policy, and so Resolve and DeniedToken,
// never lifts anything.
func (p ExecPolicy) DenyMatcher() func(abs string) bool {
	root, err := filepath.EvalSymlinks(p.Root)
	if err != nil {
		root = p.Root
	}
	home := homeDir()
	pats := append(append([]string{}, DenyFloor...), p.Deny...)
	skills := p.profileSkillsDir()
	if !p.liftHome || skills == "" {
		return func(abs string) bool { return matchDeny(pats, root, home, filepath.Clean(abs)) }
	}
	lifted := withoutHomeDeny(pats, p.HomeDeny)
	project := p.projectRoot
	return func(abs string) bool {
		abs = filepath.Clean(abs)
		use := pats
		if within(skills, abs) {
			use = lifted
		}
		if project != "" && matchDeny(use, project, home, abs) {
			return true
		}
		return matchDeny(use, root, home, abs)
	}
}

// withoutHomeDeny returns pats without JasHomeFloor and homeDeny.
func withoutHomeDeny(pats []string, homeDeny string) []string {
	out := make([]string, 0, len(pats))
	for _, pat := range pats {
		if pat == JasHomeFloor || (homeDeny != "" && pat == homeDeny) {
			continue
		}
		out = append(out, pat)
	}
	return out
}

// DeniedToken applies Denied to a shell word: ~ and relative forms are
// expanded first. Best-effort only; the shell can always build paths it hides.
func (p ExecPolicy) DeniedToken(tok string) bool {
	p.liftHome = false // the shell filter keeps the full floor
	home := homeDir()
	if tok == "~" || strings.HasPrefix(tok, "~/") {
		tok = home + tok[1:]
	}
	if tok == "" {
		return false
	}
	if !filepath.IsAbs(tok) {
		tok = filepath.Join(p.Root, tok)
	}
	return p.Denied(tok)
}

// RedactSkipped reports whether abs matches the redact_skip_paths globs.
func (p ExecPolicy) RedactSkipped(abs string) bool {
	root, err := filepath.EvalSymlinks(p.Root)
	if err != nil {
		root = p.Root
	}
	if !within(root, abs) {
		return false
	}
	rel, err := filepath.Rel(root, abs)
	return err == nil && matchAny(p.RedactSkip, filepath.ToSlash(rel))
}

func matchDeny(pats []string, root, home, abs string) bool {
	rel := ""
	if within(root, abs) {
		if r, err := filepath.Rel(root, abs); err == nil {
			rel = strings.ToLower(filepath.ToSlash(r))
		}
	}
	lowAbs := strings.ToLower(filepath.ToSlash(abs))
	for _, pat := range pats {
		pat = strings.ToLower(pat)
		if rest, ok := strings.CutPrefix(pat, "~/"); ok {
			if home == "" {
				continue
			}
			pat = strings.ToLower(filepath.ToSlash(home)) + "/" + rest
			if ok, _ := doublestar.Match(pat, lowAbs); ok {
				return true
			}
			continue
		}
		if filepath.IsAbs(filepath.FromSlash(pat)) {
			if ok, _ := doublestar.Match(pat, lowAbs); ok {
				return true
			}
			continue
		}
		if rel == "" {
			continue
		}
		if ok, _ := doublestar.Match(pat, rel); ok {
			return true
		}
	}
	return false
}

// homeDir is the symlink-free $HOME, or "" when unknown.
func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(h); err == nil {
		return r
	}
	return h
}
