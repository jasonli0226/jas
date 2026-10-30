package core

import (
	"runtime"
	"testing"
)

func TestSamePath_CaseRules(t *testing.T) {
	foldsCase := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	if got := samePath("/A/b", "/a/B"); got != foldsCase {
		t.Fatalf("samePath(/A/b, /a/B) = %v on %s, want %v", got, runtime.GOOS, foldsCase)
	}
	if !samePath("/a", "/a") {
		t.Fatal("samePath(/a, /a) = false")
	}
}
