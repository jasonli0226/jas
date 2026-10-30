package core_test

import (
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/tools"
)

// TestGated_MatchesApprover pins the gating table that jas run used before
// Gated was extracted from Approver.
func TestGated_MatchesApprover(t *testing.T) {
	want := map[core.Level]map[string]bool{
		core.Never:       {"read": false, "ls": false, "grep": false, "write": false, "edit": false, "shell": false},
		core.ShellOnly:   {"read": false, "ls": false, "grep": false, "write": false, "edit": false, "shell": true},
		core.AllMutating: {"read": false, "ls": false, "grep": false, "write": true, "edit": true, "shell": true},
	}
	all := append(tools.ReadOnly(), tools.Mutating()...)
	for level, byTool := range want {
		for _, tl := range all {
			exp, ok := byTool[tl.Name()]
			if !ok {
				t.Fatalf("no expectation for tool %q", tl.Name())
			}
			assert.Eq(t, core.Gated(level, tl), exp)
		}
	}
}
