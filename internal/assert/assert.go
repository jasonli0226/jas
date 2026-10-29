// Package assert holds tiny test assertion helpers (the project avoids testify).
package assert

import (
	"encoding/json"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// NoErr fails the test immediately if err is non-nil.
func NoErr(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Eq fails the test if got != want.
func Eq[T comparable](t testing.TB, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// JSONEq fails the test if the two JSON documents are not semantically equal.
func JSONEq(t testing.TB, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("got is not valid JSON (%v): %q", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want is not valid JSON (%v): %q", err, want)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("JSON differs:\n got: %s\nwant: %s", got, want)
	}
}

// Contains fails the test if s does not contain sub.
func Contains(t testing.TB, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("%q does not contain %q", s, sub)
	}
}

// NotContains fails the test if s contains sub.
func NotContains(t testing.TB, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Fatalf("%q unexpectedly contains %q", s, sub)
	}
}

// Perm fails the test if the permission bits differ. Windows has no unix
// permission bits (Go reports 0666 or 0444), so the check is skipped there.
func Perm(t testing.TB, got, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	if got.Perm() != want {
		t.Fatalf("got %v, want %v", got.Perm(), want)
	}
}
