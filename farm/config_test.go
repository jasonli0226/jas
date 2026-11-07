package farm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	assert.NoErr(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NoErr(t, os.WriteFile(path, []byte(body), 0o644))
}

func TestLoadConfig_Defaults(t *testing.T) {
	cfg, _, err := LoadConfig(t.TempDir(), t.TempDir(), true)
	assert.NoErr(t, err)
	assert.Eq(t, cfg.Split.MaxWorkers, 3)
	assert.Eq(t, cfg.Split.MinParts, 2)
	assert.Eq(t, cfg.Split.DisjointFiles, true)
	assert.Eq(t, cfg.Budget.PerWorkerTokens, 1500000)
	assert.Eq(t, cfg.Budget.FarmTokens, 5000000)
	assert.Eq(t, cfg.Budget.MaxOutputTokens, 16000)
	assert.Eq(t, cfg.Budget.IntegratorReserve, 0.2)
	assert.Eq(t, cfg.Budget.PerWorkerTimeout.String(), "30m0s")
	assert.Eq(t, cfg.Budget.FarmTimeout.String(), "2h0m0s")
	assert.Eq(t, cfg.OnFailure, "drop")
	assert.Eq(t, cfg.Board.MaxRounds, 3)
	assert.Eq(t, cfg.Board.PostChars, 500)
	assert.Eq(t, cfg.Board.DeliverChars, 2000)
}

func TestLoadConfig_ProjectOverUser(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, "farm.yaml"), "split:\n  max_workers: 2\n  min_parts: 3\n")
	write(t, filepath.Join(root, ".jas", "farm.yaml"), "split:\n  max_workers: 4\n")
	cfg, _, err := LoadConfig(root, home, true)
	assert.NoErr(t, err)
	assert.Eq(t, cfg.Split.MaxWorkers, 4)
	assert.Eq(t, cfg.Split.MinParts, 3)
}

func TestLoadConfig_UntrustedProjectIgnored(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, ".jas", "farm.yaml"), "check: make evil\n")
	cfg, notices, err := LoadConfig(root, home, false)
	assert.NoErr(t, err)
	assert.Eq(t, cfg.Check, "")
	assert.Eq(t, len(notices), 1)
	assert.Contains(t, notices[0], ".jas/farm.yaml")
}

func TestLoadConfig_UnknownVarIsError(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, ".jas", "farm.yaml"),
		"roles:\n  migrator:\n    writes: [\"${tset_globs}\"]\n")
	_, _, err := LoadConfig(root, home, true)
	if err == nil {
		t.Fatal("want error")
	}
	assert.Contains(t, err.Error(), "tset_globs")
}

func TestLoadConfig_KnownVarOK(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, ".jas", "farm.yaml"),
		"vars:\n  migrations_dir: db/migrations\nroles:\n  migrator:\n    writes: [\"${migrations_dir}/**\", \"${test_globs}\"]\n")
	cfg, _, err := LoadConfig(root, home, true)
	assert.NoErr(t, err)
	assert.Eq(t, cfg.Vars["migrations_dir"].Value[0], "db/migrations")
	assert.Eq(t, cfg.Vars["migrations_dir"].Source, "project")
	assert.Eq(t, cfg.Vars["test_globs"].Source, "unset")
}

func TestLoadConfig_CheckRequired(t *testing.T) {
	cfg, _, err := LoadConfig(t.TempDir(), t.TempDir(), true)
	assert.NoErr(t, err)
	assert.Eq(t, cfg.Check, "")
	assert.Eq(t, cfg.Vars["check"].Source, "unset")
}

func TestLoadConfig_CheckSources(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module x\n")
	cfg, _, err := LoadConfig(root, home, true)
	assert.NoErr(t, err)
	assert.Eq(t, cfg.Check, "go build ./... && go test ./...")
	assert.Eq(t, cfg.Vars["check"].Source, "detected")

	write(t, filepath.Join(home, "farm.yaml"), "check: make user\n")
	cfg, _, _ = LoadConfig(root, home, true)
	assert.Eq(t, cfg.Check, "make user")
	assert.Eq(t, cfg.Vars["check"].Source, "user")

	write(t, filepath.Join(root, ".jas", "farm.yaml"), "vars:\n  check: make proj\n")
	cfg, _, _ = LoadConfig(root, home, true)
	assert.Eq(t, cfg.Check, "make proj")
	assert.Eq(t, cfg.Vars["check"].Source, "project")
}

func TestLoadConfig_MaxWorkersWarns(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, ".jas", "farm.yaml"), "split:\n  max_workers: 5\n")
	_, notices, err := LoadConfig(root, home, true)
	assert.NoErr(t, err)
	assert.Eq(t, len(notices), 1)
	assert.Contains(t, notices[0], ">4")
}

func TestLoadConfig_BadInput(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, ".jas", "farm.yaml"), "on_failure: explode\n")
	_, _, err := LoadConfig(root, home, true)
	if err == nil {
		t.Fatal("want error for on_failure")
	}
	write(t, filepath.Join(root, ".jas", "farm.yaml"), "budget:\n  farm_timeout: soon\n")
	_, _, err = LoadConfig(root, home, true)
	if err == nil {
		t.Fatal("want error for duration")
	}
	write(t, filepath.Join(root, ".jas", "farm.yaml"), "bogus: 1\n")
	_, _, err = LoadConfig(root, home, true)
	if err == nil {
		t.Fatal("want error for unknown key")
	}
}

func TestLoadConfig_PartCheckDefaultsToCheck(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, "farm.yaml"), "check: make test\n")
	cfg, _, err := LoadConfig(root, home, true)
	assert.NoErr(t, err)
	assert.Eq(t, cfg.PartCheck, "make test")
}

func TestLoadConfig_PartCheckOverrideAndOff(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, "farm.yaml"), "check: make test\npart_check: make unit\n")
	cfg, _, err := LoadConfig(root, home, true)
	assert.NoErr(t, err)
	assert.Eq(t, cfg.PartCheck, "make unit")
	write(t, filepath.Join(root, ".jas", "farm.yaml"), "part_check: \"\"\n")
	cfg, _, err = LoadConfig(root, home, true)
	assert.NoErr(t, err)
	assert.Eq(t, cfg.PartCheck, "")
}

func TestLoadConfig_GeneratedProjectReplacesUser(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, "farm.yaml"), "generated:\n  - {path: a.lock, regenerate: a}\n  - {path: b.lock, regenerate: b}\n")
	write(t, filepath.Join(root, ".jas", "farm.yaml"), "generated:\n  - {path: c.lock, regenerate: c}\n")
	cfg, _, err := LoadConfig(root, home, true)
	assert.NoErr(t, err)
	assert.Eq(t, cfg.generatedSet, true)
	assert.Eq(t, len(cfg.Generated), 1)
	assert.Eq(t, cfg.Generated[0], Generated{Path: "c.lock", Regenerate: "c"})
}

func TestLoadConfig_GeneratedOffInUserLayer(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, "farm.yaml"), "generated: []\n")
	cfg, _, err := LoadConfig(root, home, true)
	assert.NoErr(t, err)
	assert.Eq(t, cfg.generatedSet, true)
	assert.Eq(t, len(cfg.Generated), 0)
}

func TestLoadConfig_GeneratedUnset(t *testing.T) {
	cfg, _, err := LoadConfig(t.TempDir(), t.TempDir(), true)
	assert.NoErr(t, err)
	assert.Eq(t, cfg.generatedSet, false)
}

func TestLoadConfig_GeneratedValidation(t *testing.T) {
	for name, body := range map[string]string{
		"empty path":       "generated:\n  - {path: \"\", regenerate: x}\n",
		"absolute path":    "generated:\n  - {path: /abs.lock, regenerate: x}\n",
		"dotdot path":      "generated:\n  - {path: ../x.lock, regenerate: x}\n",
		"duplicate path":   "generated:\n  - {path: a.lock, regenerate: x}\n  - {path: a.lock, regenerate: y}\n",
		"empty regenerate": "generated:\n  - {path: a.lock, regenerate: \"\"}\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, ".jas", "farm.yaml"), body)
			_, _, err := LoadConfig(root, t.TempDir(), true)
			if err == nil || !strings.Contains(err.Error(), "generated") {
				t.Fatalf("err = %v, want one naming generated", err)
			}
		})
	}
}
