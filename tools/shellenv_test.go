package tools

import (
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestFilterEnv_CaseFolded(t *testing.T) {
	environ := []string{`Path=C:\Windows`, "systemroot=C:\\Windows", "SECRET=x", "=C:=C:\\"}
	got := filterEnv(environ, []string{"PATH", "SystemRoot"}, true)
	assert.Eq(t, len(got), 2)
	assert.Eq(t, got[0], `Path=C:\Windows`)
	assert.Eq(t, got[1], `systemroot=C:\Windows`)
}

func TestFilterEnv_CaseSensitive(t *testing.T) {
	got := filterEnv([]string{"Path=/x", "PATH=/y"}, []string{"PATH"}, false)
	assert.Eq(t, len(got), 1)
	assert.Eq(t, got[0], "PATH=/y")
}

func TestFilterEnv_NeverNil(t *testing.T) {
	if filterEnv(nil, nil, false) == nil {
		t.Fatal("filterEnv must return a non-nil slice")
	}
}

func TestNormalizeCmd_Unix(t *testing.T) {
	got := normalizeCmd(`cat $HOME/.ssh/id_rsa ${HOME}/x \.e\nv`, "/home/u", false)
	assert.Eq(t, got, `cat /home/u/.ssh/id_rsa /home/u/x .env`)
}

func TestNormalizeCmd_WindowsKeepsBackslashPaths(t *testing.T) {
	got := normalizeCmd(`type %USERPROFILE%\.ssh\id_rsa ^.e^nv`, `C:\Users\u`, true)
	assert.Eq(t, got, `type C:\Users\u\.ssh\id_rsa .env`)
}

func TestNormalizeCmd_WindowsEnvNameCaseInsensitive(t *testing.T) {
	got := normalizeCmd(`type %userprofile%\x`, `C:\Users\u`, true)
	assert.Eq(t, got, `type C:\Users\u\x`)
}

func TestShellEnv_SetsKeyValueEntries(t *testing.T) {
	t.Setenv("TMPDIR", "/outer")
	env := ShellEnv([]string{"TMPDIR=/wt/.jas/tmp", "GIT_CONFIG_NOSYSTEM=1"})
	var tmp []string
	nosys := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMPDIR=") {
			tmp = append(tmp, kv)
		}
		if kv == "GIT_CONFIG_NOSYSTEM=1" {
			nosys = true
		}
	}
	assert.Eq(t, len(tmp), 1)
	assert.Eq(t, tmp[0], "TMPDIR=/wt/.jas/tmp")
	assert.Eq(t, nosys, true)
}
