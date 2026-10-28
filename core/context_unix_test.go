//go:build unix

package core_test

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func TestFindContextFile_FIFOSkipped(t *testing.T) {
	root := t.TempDir()
	assert.NoErr(t, syscall.Mkfifo(filepath.Join(root, "AGENTS.md"), 0o644))
	done := make(chan bool, 1)
	go func() { _, _, ok := core.FindContextFile(root); done <- ok }()
	select {
	case ok := <-done:
		assert.Eq(t, ok, false)
	case <-time.After(3 * time.Second):
		t.Fatal("FindContextFile hung on a FIFO")
	}
}
