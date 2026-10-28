package core

import (
	"math"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// RedactedMarker prefixes every replacement; write and edit refuse content
// containing it (a model that saw a redacted value must not write it back).
const RedactedMarker = "[REDACTED:"

const (
	assignMinEntropy = 3.5
	// identMaxLen bounds the code-identifier exemption: a long letters-only
	// value is more likely a secret than a symbol name.
	identMaxLen = 40
)

// codeValueRes match values that look high-entropy but are source code, as in
// `tok := ctx.NextToken`: identifiers (letters and underscores only), dotted
// paths, and CamelCase names (digits allowed inside acronyms).
var codeValueRes = []*regexp.Regexp{
	regexp.MustCompile(`^[A-Za-z_][A-Za-z_]*$`),
	regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+$`),
	regexp.MustCompile(`^(?:[A-Z][a-z]{2,}|[A-Z]{2,}[0-9]*)+$`),
}

// looksLikeCode reports whether an assignment value is source code, not a secret.
func looksLikeCode(v string) bool {
	if strings.Contains(strings.TrimRight(v, "="), "=") {
		return true // base64 only has = as trailing padding
	}
	for i, re := range codeValueRes {
		if i == 0 && len(v) >= identMaxLen {
			continue
		}
		if re.MatchString(v) {
			return true
		}
	}
	return false
}

// Redactor scrubs secrets from text headed for the model, the event log or
// the terminal. Exact values are replaced wherever they occur; the rest is
// pattern based. SkipPaths lists path globs exempt from the write/edit
// redacted-marker refusal (see Skips); they never exempt output redaction.
type Redactor struct {
	Exact     []string
	SkipPaths []string
}

type redactRule struct {
	kind string
	re   *regexp.Regexp
}

// Order matters: whole-token rules run before the generic assignment rule.
var redactRules = []redactRule{
	{"private_key", regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----(?:.*?-----END [A-Z ]*PRIVATE KEY-----|.*)`)},
	{"jwt", regexp.MustCompile(`eyJ[\w-]+\.[\w-]+\.[\w-]+`)},
	{"openai_key", regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`)},
	{"github_token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`)},
	{"aws_key_id", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"bearer", regexp.MustCompile(`Bearer [A-Za-z0-9._~+/-]{20,}=*`)},
}

// assignRe captures: 1 key+separator (kept), 2 value (replaced).
var assignRe = regexp.MustCompile(`(?i)([A-Za-z0-9_.-]*(?:secret|token|passw(?:or)?d|api[_-]?key|private[_-]?key|credential)[A-Za-z0-9_.-]*["']?[ \t]*(?::=|[:=])[ \t]*["']?)([A-Za-z0-9_\-+/=.~!@#$%^&*]{16,})`)

func mark(kind string) string { return RedactedMarker + kind + "]" }

// Redact replaces every secret found in s with [REDACTED:<kind>].
func (r *Redactor) Redact(s string) string {
	if s == "" {
		return s
	}
	for _, v := range r.Exact {
		if v != "" {
			s = strings.ReplaceAll(s, v, mark("exact"))
		}
	}
	for _, rule := range redactRules {
		s = rule.re.ReplaceAllLiteralString(s, mark(rule.kind))
	}
	return redactAssignments(s)
}

// redactAssignments replaces high-entropy values of secret-named keys.
func redactAssignments(s string) string {
	idx := assignRe.FindAllStringSubmatchIndex(s, -1)
	if idx == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range idx {
		vs, ve := m[4], m[5]
		v := s[vs:ve]
		if strings.Contains(v, RedactedMarker) || shannon(v) < assignMinEntropy || looksLikeCode(v) {
			continue
		}
		b.WriteString(s[last:vs])
		b.WriteString(mark("assignment"))
		last = ve
	}
	b.WriteString(s[last:])
	return b.String()
}

// Filter redacts a tool result's output; it is the Executor.Filter hook.
func (r *Redactor) Filter(res Result) Result {
	res.Output = r.Redact(res.Output)
	return res
}

// Skips reports whether the slash-separated root-relative path matches
// SkipPaths.
func (r *Redactor) Skips(rel string) bool { return matchAny(r.SkipPaths, rel) }

// shannon is the Shannon entropy, in bits per byte, of v's byte histogram.
func shannon(v string) float64 {
	if v == "" {
		return 0
	}
	var hist [256]int
	for i := 0; i < len(v); i++ {
		hist[v[i]]++
	}
	n := float64(len(v))
	h := 0.0
	for _, c := range hist {
		if c > 0 {
			p := float64(c) / n
			h -= p * math.Log2(p)
		}
	}
	return h
}

func matchAny(patterns []string, rel string) bool {
	for _, pat := range patterns {
		if ok, _ := doublestar.Match(pat, rel); ok {
			return true
		}
	}
	return false
}
