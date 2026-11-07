package farm

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

const npmRegen = "npm install --package-lock-only --no-audit --no-fund"

func TestDetectGenerated(t *testing.T) {
	for name, tc := range map[string]struct {
		files []string
		want  []Generated
	}{
		"npm lockfile":      {[]string{"package.json", "package-lock.json"}, []Generated{{"package-lock.json", npmRegen}}},
		"yarn berry":        {[]string{"package.json", "yarn.lock", ".yarnrc.yml"}, []Generated{{"yarn.lock", "yarn install --mode=update-lockfile"}}},
		"yarn classic":      {[]string{"package.json", "yarn.lock"}, []Generated{{"yarn.lock", "yarn install --ignore-scripts --non-interactive"}}},
		"pnpm":              {[]string{"package.json", "pnpm-lock.yaml"}, []Generated{{"pnpm-lock.yaml", "pnpm install --lockfile-only"}}},
		"cargo":             {[]string{"Cargo.toml", "Cargo.lock"}, []Generated{{"Cargo.lock", "cargo update --workspace"}}},
		"uv":                {[]string{"pyproject.toml", "uv.lock"}, []Generated{{"uv.lock", "uv lock"}}},
		"package.json only": {[]string{"package.json"}, []Generated{{"package-lock.json", npmRegen}}},
		"nothing":           {[]string{"README.md"}, nil},
		"nested lockfile":   {[]string{"client/package.json", "client/package-lock.json"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			w, _ := newRepo(t)
			for _, f := range tc.files {
				writeFile(t, filepath.Join(w.Main, f), "x\n")
			}
			mustGit(t, w.Git, w.Main, "add", "-A")
			mustGit(t, w.Git, w.Main, "commit", "-q", "-m", "files")
			base := mustGit(t, w.Git, w.Main, "rev-parse", "HEAD")
			got, err := DetectGenerated(context.Background(), w.Git, w.Main, base)
			assert.NoErr(t, err)
			assert.Eq(t, len(got), len(tc.want))
			for i := range tc.want {
				assert.Eq(t, got[i], tc.want[i])
			}
		})
	}
}

// A lockfile only in the working tree (untracked or ignored) is not at base.
func TestDetectGenerated_ReadsBaseNotWorkingTree(t *testing.T) {
	w, base := newRepo(t)
	writeFile(t, filepath.Join(w.Main, "Cargo.lock"), "x\n")
	got, err := DetectGenerated(context.Background(), w.Git, w.Main, base)
	assert.NoErr(t, err)
	assert.Eq(t, len(got), 0)
}

func TestResolveGenerated_SetWinsOverDetect(t *testing.T) {
	w, _ := newRepo(t)
	writeFile(t, filepath.Join(w.Main, "Cargo.lock"), "x\n")
	mustGit(t, w.Git, w.Main, "add", "-A")
	mustGit(t, w.Git, w.Main, "commit", "-q", "-m", "lock")
	base := mustGit(t, w.Git, w.Main, "rev-parse", "HEAD")
	ctx := context.Background()

	got, err := ResolveGenerated(ctx, Config{generatedSet: true}, w.Git, w.Main, base)
	assert.NoErr(t, err)
	assert.Eq(t, len(got), 0)

	got, err = ResolveGenerated(ctx, Config{}, w.Git, w.Main, base)
	assert.NoErr(t, err)
	assert.Eq(t, len(got), 1)
	assert.Eq(t, generatedPaths(got)[0], "Cargo.lock")
}
