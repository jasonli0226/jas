package memory_test

import (
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/memory"
)

func TestKey_Examples(t *testing.T) {
	cases := map[string]string{
		"/home/u/src/jas": "-home-u-src-jas",
		"/home/u/my-repo": "-home-u-my%2Drepo",
		"/home/u/my/repo": "-home-u-my-repo",
		"/a/b c%":         "-a-b%20c%25",
	}
	for path, want := range cases {
		assert.Eq(t, memory.Key(path), want)
	}
}

func TestKey_RoundTrip(t *testing.T) {
	for _, p := range []string{"/home/u/src/jas", "/home/u/my-repo", "/home/u/my/repo", "/a/b c%", "/x/y_z.v1"} {
		got, ok := memory.DecodeKey(memory.Key(p))
		assert.Eq(t, ok, true)
		assert.Eq(t, got, p)
	}
}

func TestKey_Injective(t *testing.T) {
	pairs := [][2]string{{"/a/b-c", "/a/b/c"}, {"/a-/b", "/a/-b"}}
	for _, p := range pairs {
		if memory.Key(p[0]) == memory.Key(p[1]) {
			t.Fatalf("%q and %q share key %q", p[0], p[1], memory.Key(p[0]))
		}
	}
}

func TestKey_LongPathHashed(t *testing.T) {
	a := "/" + strings.Repeat("a", 299)
	k := memory.Key(a)
	assert.Eq(t, len(k), 180+1+16)
	assert.Contains(t, k, "~")
	assert.Eq(t, memory.Key(a), k)
	b := a[:250] + strings.Repeat("b", 50)
	if memory.Key(b) == k {
		t.Fatal("long paths sharing a prefix collided")
	}
	_, ok := memory.DecodeKey(k)
	assert.Eq(t, ok, false)
}

func TestDecodeKey_Malformed(t *testing.T) {
	for _, k := range []string{"-a%2", "-a%zz", "-a%2f", "-a b", "-a~b"} {
		if _, ok := memory.DecodeKey(k); ok {
			t.Fatalf("DecodeKey(%q) ok, want false", k)
		}
	}
}
