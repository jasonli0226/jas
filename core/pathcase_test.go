package core

import (
	"runtime"
	"testing"
)

func TestSamePath_CaseRules(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		if got := samePathOn(goos, "/A/b", "/a/B"); got != foldsCase(goos) {
			t.Errorf("samePathOn(%s, /A/b, /a/B) = %v, want %v", goos, got, foldsCase(goos))
		}
		if !samePathOn(goos, "/a", "/a") {
			t.Errorf("samePathOn(%s, /a, /a) = false", goos)
		}
	}
	if samePath("/A/b", "/a/B") != foldsCase(runtime.GOOS) {
		t.Fatalf("samePath does not follow %s", runtime.GOOS)
	}
}
