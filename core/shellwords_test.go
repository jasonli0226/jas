package core

import (
	"reflect"
	"testing"
)

func TestShellWords(t *testing.T) {
	tests := []struct {
		in   string
		want []string
		ok   bool
	}{
		{"go test ./...", []string{"go", "test", "./..."}, true},
		{`git commit -m "a b"`, []string{"git", "commit", "-m", "a b"}, true},
		{`echo 'a "b" \n'`, []string{"echo", `a "b" \n`}, true},
		{`echo a\ b`, []string{"echo", "a b"}, true},
		{`echo "a\"b"`, []string{"echo", `a"b`}, true},
		{`echo "" x`, []string{"echo", "", "x"}, true},
		{"  a \t b  ", []string{"a", "b"}, true},
		{"", nil, true},
		{"   ", nil, true},
		{`echo "a\b\$c\\d"`, []string{"echo", `a\b$c\d`}, true},
		{"go\rtest x", []string{"go\rtest", "x"}, true},
		{"echo 'x", nil, false},
		{`echo "x`, nil, false},
		{`echo x\`, nil, false},
	}
	for _, tc := range tests {
		got, ok := ShellWords(tc.in)
		if ok != tc.ok || (ok && !equalWords(got, tc.want)) {
			t.Errorf("ShellWords(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func equalWords(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	return reflect.DeepEqual(a, b)
}
