package core

import (
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

const fixtureSecret = "hunter2-very-long-secret-value"

func TestRedact_Positives(t *testing.T) {
	files, err := filepath.Glob("testdata/redact/positives/*.txt")
	assert.NoErr(t, err)
	if len(files) == 0 {
		t.Fatal("no positive fixtures")
	}
	r := &Redactor{}
	for _, f := range files {
		in, err := os.ReadFile(f)
		assert.NoErr(t, err)
		want, err := os.ReadFile(strings.TrimSuffix(f, ".txt") + ".expect")
		assert.NoErr(t, err)
		assert.Eq(t, r.Redact(string(in)), string(want))
	}
}

func TestShannon_Pinned(t *testing.T) {
	if got := shannon(fixtureSecret); math.Abs(got-3.761) > 0.001 {
		t.Fatalf("shannon = %v, want 3.761", got)
	}
	assert.Eq(t, shannon("aaaa"), 0.0)
}

func TestRedact_NegativesUntouched(t *testing.T) {
	files, err := filepath.Glob("testdata/redact/negatives/*")
	assert.NoErr(t, err)
	if len(files) < 3 {
		t.Fatalf("want negatives corpus, got %v", files)
	}
	r := &Redactor{}
	for _, f := range files {
		in, err := os.ReadFile(f)
		assert.NoErr(t, err)
		if got := r.Redact(string(in)); got != string(in) {
			t.Errorf("%s changed", f)
		}
	}
}

func TestRedact_ExactValueAnywhere(t *testing.T) {
	r := &Redactor{Exact: []string{"sk-or-v1-abc123", ""}}
	got := r.Redact("GET https://host/path?key=sk-or-v1-abc123&x=1 and nothing else")
	assert.Eq(t, got, "GET https://host/path?key=[REDACTED:exact]&x=1 and nothing else")
	assert.Eq(t, r.Redact("plain"), "plain")
}

func TestRedact_UnterminatedPrivateKey(t *testing.T) {
	got := (&Redactor{}).Redact("x\n-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBg\n[truncated")
	assert.Eq(t, got, "x\n[REDACTED:private_key]")
}

func TestRedact_NoDoubleRedaction(t *testing.T) {
	got := (&Redactor{}).Redact("token=sk-proj-AbCdEfGhIjKlMnOpQrStUvWx12345")
	assert.Eq(t, got, "token=[REDACTED:openai_key]")
}

func TestRedactor_FilterAndSkip(t *testing.T) {
	r := &Redactor{Exact: []string{"s3cr3tvalue"}, SkipPaths: []string{"testdata/**"}}
	res := r.Filter(Result{Output: "a s3cr3tvalue b", IsError: true})
	assert.Eq(t, res.Output, "a [REDACTED:exact] b")
	assert.Eq(t, res.IsError, true)
	assert.Eq(t, r.Skips("testdata/x/y.txt"), true)
	assert.Eq(t, r.Skips("src/y.txt"), false)
}

func TestRedact_RealRepoFPSample(t *testing.T) {
	root := filepath.Join(runtime.GOROOT(), "src")
	if _, err := os.Stat(root); err != nil {
		t.Skip("GOROOT/src unavailable")
	}
	r := &Redactor{}
	total, changed := 0, 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		total++
		if out := r.Redact(string(b)); out != string(b) {
			changed++
			t.Logf("changed: %s kinds=%v", p, kindsIn(out))
		}
		return nil
	})
	t.Logf("%d of %d files changed", changed, total)
	if total == 0 || float64(changed) > 0.001*float64(total) {
		t.Fatalf("false-positive rate too high: %d of %d", changed, total)
	}
}

func kindsIn(s string) []string {
	var ks []string
	for _, part := range strings.Split(s, RedactedMarker)[1:] {
		k, _, _ := strings.Cut(part, "]")
		ks = append(ks, k)
	}
	return ks
}
