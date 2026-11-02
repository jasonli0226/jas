package farm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonli0226/jas/internal/assert"
)

func readPlan(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "plans", name))
	assert.NoErr(t, err)
	return b
}

func TestCanonicalHash_IgnoresApprovedBlockAndCRLF(t *testing.T) {
	base := string(readPlan(t, "valid.yaml"))
	want := CanonicalHash([]byte(base))
	approved := base + "approved:\n  sha256: abc\n  by: me\n  at: 2026-11-02T23:30:00+09:00\n"
	assert.Eq(t, CanonicalHash([]byte(approved)), want)
	assert.Eq(t, CanonicalHash([]byte(strings.ReplaceAll(approved, "\n", "\r\n"))), want)
	assert.Eq(t, CanonicalHash([]byte(base+"\n\n  ")), want)
	if CanonicalHash([]byte("# note\n"+base)) == want {
		t.Fatal("added comment must change the hash")
	}
}

func TestApprove_RoundTrip(t *testing.T) {
	at := time.Date(2026, 11, 2, 23, 30, 0, 0, time.UTC)
	for _, name := range []string{"valid.yaml", "approved.yaml"} {
		out := Approve(readPlan(t, name), "owner", at)
		p, err := ParsePlan(out)
		assert.NoErr(t, err)
		if p.Approved == nil {
			t.Fatal("no approval")
		}
		assert.Eq(t, p.Approved.SHA256, CanonicalHash(out))
		assert.Eq(t, p.Approved.By, "owner")
		assert.Eq(t, strings.Count(string(out), "approved:"), 1)
		// Re-approving is stable.
		assert.Eq(t, CanonicalHash(Approve(out, "x", at)), CanonicalHash(out))
	}
}

func TestParsePlan_Refusal(t *testing.T) {
	p, err := ParsePlan(readPlan(t, "refuse.yaml"))
	assert.NoErr(t, err)
	assert.Eq(t, p.Refuse.Reason, "small_change")
	assert.Eq(t, p.Refuse.Suggest, `jas run "fix typo"`)
	_, err = ParsePlan(readPlan(t, "refuse_bad.yaml"))
	if err == nil {
		t.Fatal("unknown reason must error")
	}
}

func TestParsePlan_Valid(t *testing.T) {
	p, err := ParsePlan(readPlan(t, "valid.yaml"))
	assert.NoErr(t, err)
	assert.Eq(t, len(p.Parts), 3)
	assert.Eq(t, p.Parts[2].After[0], "core")
	assert.Eq(t, p.Interfaces[0].Owner, "core")
	assert.Eq(t, p.Shared[0], "go.mod")
}
