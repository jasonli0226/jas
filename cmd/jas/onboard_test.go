package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
)

func jasWithStdin(t *testing.T, home, stdin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(testutil.JasBinary(t), args...)
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "JAS_HOME=" + home}
	cmd.Stdin = bytes.NewBufferString(stdin)
	var se bytes.Buffer
	cmd.Stderr = &se
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return se.String(), code
}

func TestOnboard_WritesConfigAndAuth(t *testing.T) {
	home := filepath.Join(t.TempDir(), "jas")
	_, code := jasWithStdin(t, home, "openrouter\n$OPENROUTER_API_KEY\nz-ai/glm-5.3\n")
	assert.Eq(t, code, 0)
	var cfg map[string]any
	b, err := os.ReadFile(filepath.Join(home, "config.json"))
	assert.NoErr(t, err)
	assert.NoErr(t, json.Unmarshal(b, &cfg))
	assert.Eq(t, cfg["provider"].(string), "openrouter")
	assert.Eq(t, cfg["model"].(string), "z-ai/glm-5.3")
	authPath := filepath.Join(home, "auth.json")
	info, err := os.Stat(authPath)
	assert.NoErr(t, err)
	assert.Perm(t, info.Mode(), 0o600)
	var auth map[string]map[string]string
	b, _ = os.ReadFile(authPath)
	assert.NoErr(t, json.Unmarshal(b, &auth))
	assert.Eq(t, auth["openrouter"]["env"], "OPENROUTER_API_KEY")
	assert.Eq(t, auth["openrouter"]["key"], "")
}

func TestOnboard_ExistingConfigPrintsUsage(t *testing.T) {
	home := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.json"), []byte("{}\n"), 0o600))
	errOut, code := jasWithStdin(t, home, "")
	assert.Eq(t, code, 0)
	assert.Contains(t, errOut, "usage:")
}
