package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestTruncate_NoNewlines(t *testing.T) {
	out := Truncate([]byte(strings.Repeat("a", 100*1024)), 32768)
	marker := "\n[truncated: 1 more lines]"
	if len(out) > 32768+len(marker) {
		t.Fatalf("too long: %d", len(out))
	}
	assert.Contains(t, out, "[truncated: 1 more lines]")
}

func TestTruncate_CutsAtNewline(t *testing.T) {
	out := Truncate([]byte("aaa\nbbb\nccc\nddd"), 9)
	assert.Eq(t, out, "aaa\nbbb\n[truncated: 2 more lines]")
}

func TestTruncate_InvalidUTF8(t *testing.T) {
	out := Truncate([]byte("\xff\xfe"), 100)
	if !utf8.ValidString(out) {
		t.Fatal("invalid utf8")
	}
	assert.Contains(t, out, "�")
	b, err := json.Marshal(out)
	assert.NoErr(t, err)
	var back string
	assert.NoErr(t, json.Unmarshal(b, &back))
	assert.Eq(t, back, out)
}

func TestTruncate_UnderCapUnchanged(t *testing.T) {
	assert.Eq(t, Truncate([]byte("hello\nworld"), 100), "hello\nworld")
}
