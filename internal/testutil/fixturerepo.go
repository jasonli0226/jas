package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Fixture secret values written by NewFixtureRepo.
const (
	FixtureOpenAIKey  = "sk-fixture-0123456789abcdefghij"
	FixtureDBPassword = "hunter2-very-long-secret-value"
	FixtureConfigKey  = "Xk9fQ2vLmN8pR4sT6uW1yZ3aB5cD7eGh"

	fixtureEnvFile    = "OPENAI_API_KEY=" + FixtureOpenAIKey + "\nDB_PASSWORD=" + FixtureDBPassword + "\n"
	fixtureConfigYAML = "name: demo\napi_key: " + FixtureConfigKey + "\nregion: us-east-1\n"
	fixtureMainGo     = "package main\n\nfunc main() {}\n"
	fixtureFilePerm   = 0o600
)

// NewFixtureRepo creates a git repo in a temp dir holding main.go, a .env with
// secrets and a config.yaml with a high-entropy api_key, and returns its path.
func NewFixtureRepo(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{"main.go": fixtureMainGo, ".env": fixtureEnvFile, "config.yaml": fixtureConfigYAML}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), fixtureFilePerm); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return dir
}
