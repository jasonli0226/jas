package skills

import (
	"strings"
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

func TestParseFrontmatter_UnindentedListIgnored(t *testing.T) {
	fm, err := parseFrontmatter([]byte("---\nname: x\nallowed-tools:\n- Read\n- Grep\ndescription: d\n---\n"))
	assert.NoErr(t, err)
	assert.Eq(t, fm["name"], "x")
	assert.Eq(t, fm["description"], "d")
}

// TestParseFrontmatter_Golden pins the parser's current behaviour on the
// quirks SKILL.md files actually rely on. If one of these cases changes
// because of a bug fix, update the expectation and say why.
func TestParseFrontmatter_Golden(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want map[string]string
		err  string // empty means success
	}{
		{
			name: "empty frontmatter",
			src:  "---\n---\n# body\n",
			want: map[string]string{},
		},
		{
			name: "empty frontmatter with blank lines",
			src:  "---\n\n\n---\n",
			want: map[string]string{},
		},
		{
			name: "non-frontmatter file",
			src:  "# just markdown\n\nno fences at all\n",
			err:  "no frontmatter",
		},
		{
			name: "frontmatter not on first line",
			src:  "# title\n---\nname: x\n---\n",
			err:  "no frontmatter",
		},
		{
			name: "dotted ellipsis closes",
			src:  "---\nname: x\n...\n",
			want: map[string]string{"name": "x"},
		},
		{
			name: "indented continuation folded into plain value",
			src:  "---\nname: x # trailing\n  continuation\ndescription: d\n---\n",
			want: map[string]string{"name": "x continuation", "description": "d"},
		},
		{
			name: "full-line comment inside plain continuation",
			src:  "---\ndescription: a\n  # comment\n  b\n...\n",
			want: map[string]string{"description": "a b"},
		},
		{
			name: "trailing comment needs preceding space",
			src:  "---\nname: v#not a comment\n---\n",
			want: map[string]string{"name": "v#not a comment"},
		},
		{
			name: "trailing comment after plain value",
			src:  "---\nname: v # comment\n---\n",
			want: map[string]string{"name": "v"},
		},
		{
			name: "plain value continued on indented lines",
			src:  "---\nname: x\ndescription:\n  Use when probing\n  the parser\n---\n",
			want: map[string]string{"name": "x", "description": "Use when probing the parser"},
		},
		{
			name: "nested map folded into a string (quirk, #51)",
			src:  "---\nname: x\nmetadata:\n  author: me\n  license: MIT\nversion: 2\n---\n",
			want: map[string]string{"name": "x", "metadata": "author: me license: MIT", "version": "2"},
		},
		{
			name: "nested map folded, then sibling key",
			src:  "---\nmetadata:\n  a: b\nname: x\n---\n",
			want: map[string]string{"metadata": "a: b", "name": "x"},
		},
		{
			name: "quoted double with escapes",
			src:  "---\nname: x\ndescription: \"line one\\nline two\"\n---\n",
			want: map[string]string{"name": "x", "description": "line one\nline two"},
		},
		{
			name: "quoted single with doubled quote",
			src:  "---\nname: x\nlicense: 'don''t'\n---\n",
			want: map[string]string{"name": "x", "license": "don't"},
		},
		{
			name: "quoted value spanning lines",
			src:  "---\nname: x\ndescription: \"first part\n  second part\"\n---\n",
			want: map[string]string{"name": "x", "description": "first part second part"},
		},
		{
			name: "block literal keeps newlines",
			src:  "---\nname: x\nnote: |\n  line1\n  line2\n---\n",
			want: map[string]string{"name": "x", "note": "line1\nline2"},
		},
		{
			name: "block folded joins lines",
			src:  "---\nname: x\nnote: >\n  line1\n  line2\n---\n",
			want: map[string]string{"name": "x", "note": "line1 line2"},
		},
		{
			name: "folded blank line becomes newline",
			src:  "---\nname: x\nnote: >\n  para one\n\n  para two\n---\n",
			want: map[string]string{"name": "x", "note": "para one\npara two"},
		},
		{
			name: "bare key with no value",
			src:  "---\nname:\ndescription: d\n---\n",
			want: map[string]string{"name": "", "description": "d"},
		},
		{
			name: "key chars beyond letters",
			src:  "---\nallowed-tools_2: x\n---\n",
			want: map[string]string{"allowed-tools_2": "x"},
		},
		{
			name: "closing fence with trailing spaces",
			src:  "---\nname: x\n---   \n",
			want: map[string]string{"name": "x"},
		},
		{
			name: "unindented list folded into a string (quirk)",
			src:  "---\nname: x\nallowed-tools:\n- Read\n- Grep\ndescription: d\n---\n",
			want: map[string]string{"name": "x", "allowed-tools": "- Read - Grep", "description": "d"},
		},
		{
			name: "plain value folding indented lines",
			src:  "---\ndescription: first\n  second\n  third\n---\n",
			want: map[string]string{"description": "first second third"},
		},
		{
			name: "top-level junk errors",
			src:  "---\njust text\n---\n",
			err:  "expected key: value",
		},
		{
			name: "indented first key line errors",
			src:  "---\n  name: x\n---\n",
			err:  "expected key: value",
		},
		{
			name: "unclosed double quote",
			src:  "---\ndescription: \"oops\n---\n",
			err:  "unterminated quoted value",
		},
		{
			name: "opening fence with text after it",
			src:  "--- text\nname: x\n---\n",
			err:  "no frontmatter",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFrontmatter([]byte(tc.src))
			if tc.err != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got %v", tc.err, got)
				}
				if !contains(err.Error(), tc.err) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("key %q: got %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
