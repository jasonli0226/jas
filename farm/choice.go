package farm

import "strings"

// MatchChoice returns the one choice ans names, ignoring case and outer
// space: the choice itself, or a prefix of exactly one choice ("dr" names
// "drop", "d" names "discard"). Text that only starts with a choice names
// nothing, so "don't" is not "d" and "discarded" is not "discard": an answer
// can never pick a destructive choice by accident.
func MatchChoice(ans string, choices []string) (string, bool) {
	ans = strings.ToLower(strings.TrimSpace(ans))
	if ans == "" {
		return "", false
	}
	var found []string
	for _, c := range choices {
		if ans == c {
			return c, true
		}
		if strings.HasPrefix(c, ans) {
			found = append(found, c)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return "", false
}
