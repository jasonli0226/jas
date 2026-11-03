package skills

import "testing"

// FuzzParseFrontmatter pins robustness: the parser must never panic, and
// when it succeeds the result must be usable as a map of strings.
func FuzzParseFrontmatter(f *testing.F) {
	for _, s := range []string{
		"---\nname: x\ndescription: d\n---\n",
		"---\ndescription: >-\n  a\n\n  b\n---\n",
		"---\ndescription: \"a\\\"b\n  c\"\n---\n",
		"---\nk: 'it''s'\nm:\n\t x: y\n---\n",
		"---\nmetadata:\n  a: b\nname: x\n---\n",
		"---\nk:\n- item\n- item\n---\n",
		"---\nk: |\n  block\n  text\n---\n",
		"---\n... body\n",
		"\xef\xbb\xbf---\r\nk: v\r\n---\r\n",
		"---\nk: 'unterminated\n---\n",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		fm, err := parseFrontmatter(b)
		if err != nil {
			return
		}
		// Sanity invariants on any accepted file.
		for k, v := range fm {
			if k == "" {
				t.Fatalf("empty key in %q", b)
			}
			if !validKey(k) {
				t.Fatalf("bad key %q in %q", k, b)
			}
			_ = v
		}
	})
}

func validKey(k string) bool {
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}