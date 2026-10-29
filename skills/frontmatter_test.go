package skills

import (
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestParseFrontmatter_PlainAndQuoted(t *testing.T) {
	fm, err := parseFrontmatter([]byte("---\nname: pdf-tools\ndescription: \"Fill \\\"PDF\\\" forms\"\nlicense: 'it''s MIT'\n---\n# Body\n"))
	assert.NoErr(t, err)
	assert.Eq(t, fm["name"], "pdf-tools")
	assert.Eq(t, fm["description"], `Fill "PDF" forms`)
	assert.Eq(t, fm["license"], "it's MIT")
}

func TestParseFrontmatter_UnquotedColonKept(t *testing.T) {
	fm, err := parseFrontmatter([]byte("---\nname: x\ndescription: Use when: the user asks about PDFs\n---\n"))
	assert.NoErr(t, err)
	assert.Eq(t, fm["description"], "Use when: the user asks about PDFs")
}

func TestParseFrontmatter_BlockScalars(t *testing.T) {
	src := "---\nname: x\ndescription: >-\n  Folded line one\n  and two.\nnotes: |\n  keep\n  lines\n---\n"
	fm, err := parseFrontmatter([]byte(src))
	assert.NoErr(t, err)
	assert.Eq(t, fm["description"], "Folded line one and two.")
	assert.Eq(t, fm["notes"], "keep\nlines")
}

func TestParseFrontmatter_PlainContinuationAndComment(t *testing.T) {
	src := "---\n# a comment\nname: x # trailing\ndescription: starts here\n  and continues\nmetadata:\n  author: me\n---\n"
	fm, err := parseFrontmatter([]byte(src))
	assert.NoErr(t, err)
	assert.Eq(t, fm["name"], "x")
	assert.Eq(t, fm["description"], "starts here and continues")
}

func TestParseFrontmatter_CRLFAndBOM(t *testing.T) {
	fm, err := parseFrontmatter([]byte("\xef\xbb\xbf---\r\nname: x\r\ndescription: d\r\n---\r\nbody"))
	assert.NoErr(t, err)
	assert.Eq(t, fm["name"], "x")
	assert.Eq(t, fm["description"], "d")
}

func TestParseFrontmatter_Errors(t *testing.T) {
	for name, src := range map[string]string{
		"no opening fence": "name: x\n",
		"no closing fence": "---\nname: x\n",
		"top-level list":   "---\n- a\n---\n",
		"unclosed quote":   "---\ndescription: \"oops\n---\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseFrontmatter([]byte(src)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func FuzzParseFrontmatter(f *testing.F) {
	for _, s := range []string{
		"---\nname: x\ndescription: d\n---\n",
		"---\ndescription: >-\n  a\n\n  b\n---\n",
		"---\ndescription: \"a\\\"b\n  c\"\n---\n",
		"---\nk: 'it''s'\nm:\n\t x: y\n---\n",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = parseFrontmatter(b) // must not panic
	})
}
