package main

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
)

func TestAskLine_ReturnsOnCancel(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan string, 1)
	go func() { res <- askLine(ctx, bufio.NewReader(pr), io.Discard, "p ") }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case got := <-res:
		assert.Eq(t, got, "")
	case <-time.After(2 * time.Second):
		t.Fatal("askLine did not return after cancel")
	}
}

func TestCLI_TrustProjectIgnoresCorruptStore(t *testing.T) {
	proj := t.TempDir()
	writeAgents(t, proj, "UNIQUE-RULE-9912")
	home := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "trust.json"), []byte("{bad"), 0o600))
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	_, _, code := runJasIn(t, proj, []string{"JAS_HOME=" + home, "JAS_MODEL=m", "JAS_API_KEY=k", "JAS_BASE_URL=" + url}, "run", "--trust-project", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, string(bodies()[0]), "UNIQUE-RULE-9912")
}
