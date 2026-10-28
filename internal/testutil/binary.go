package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// JasBinary builds ./cmd/jas once per test process and returns its path.
func JasBinary(t testing.TB) string {
	t.Helper()
	binOnce.Do(func() {
		_, file, _, _ := runtime.Caller(0)
		root := filepath.Join(filepath.Dir(file), "..", "..")
		dir, err := os.MkdirTemp("", "jas-bin-")
		if err != nil {
			binErr = err
			return
		}
		binPath = filepath.Join(dir, "jas")
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/jas")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			binErr = fmt.Errorf("%w: %s", err, out)
		}
	})
	if binErr != nil {
		t.Fatalf("build jas: %v", binErr)
	}
	return binPath
}
