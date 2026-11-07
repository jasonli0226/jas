package farm

import (
	"strings"
	"testing"
)

func TestProgress_ClipsToTerminalWidth(t *testing.T) {
	var b strings.Builder
	p := NewProgress(&b, nil)
	p.Width = func() int { return 20 }
	p.print("[frontmatter-tests] → shell go test -fuzz FuzzParseFrontmatter")
	p.print("short")
	got := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if len(got) != 2 || len([]rune(got[0])) != 19 || got[1] != "short" {
		t.Fatalf("lines %q", got)
	}
	if !strings.HasSuffix(got[0], "…") {
		t.Fatalf("clipped line lacks an ellipsis: %q", got[0])
	}
}

func TestProgress_NoWidthNoClip(t *testing.T) {
	var b strings.Builder
	p := NewProgress(&b, nil)
	long := strings.Repeat("x", 300)
	p.print(long)
	if strings.TrimSuffix(b.String(), "\n") != long {
		t.Fatal("line clipped without a terminal width")
	}
}

func TestProgressLine_Generated(t *testing.T) {
	got := ProgressLine(EvGeneratedRestored, map[string]any{"part": "a", "paths": []any{"root.lock"}})
	if got != "[a] undid generated root.lock" {
		t.Fatalf("line %q", got)
	}
}

func TestProgressLine_Regenerated(t *testing.T) {
	for want, m := range map[string]map[string]any{
		"[integrate] regenerated root.lock":                {"path": "root.lock", "exit_code": 0, "changed": true},
		"[integrate] regenerate root.lock failed (exit 3)": {"path": "root.lock", "exit_code": 3, "changed": false},
		"": {"path": "root.lock", "exit_code": 0, "changed": false},
	} {
		if got := ProgressLine(EvGeneratedRegenerated, m); got != want {
			t.Errorf("line %q, want %q", got, want)
		}
	}
}
