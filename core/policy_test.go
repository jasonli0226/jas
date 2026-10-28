package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func realpath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	assert.NoErr(t, err)
	return r
}

func TestFindRoot_GitAndPlain(t *testing.T) {
	repo := realpath(t, t.TempDir())
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v %s", err, out)
	}
	sub := filepath.Join(repo, "a", "b")
	assert.NoErr(t, os.MkdirAll(sub, 0o755))
	assert.Eq(t, realpath(t, FindRoot(sub)), repo)

	plain := realpath(t, t.TempDir())
	assert.Eq(t, FindRoot(plain), plain)
}

func TestResolve_RefusesDotDot(t *testing.T) {
	root := realpath(t, t.TempDir())
	_, err := ExecPolicy{Root: root}.Resolve("../outside")
	if err == nil {
		t.Fatal("expected error")
	}
	assert.Contains(t, err.Error(), "path outside project root")
}

func TestResolve_RefusesSymlinkOut(t *testing.T) {
	root := realpath(t, t.TempDir())
	target := filepath.Join(realpath(t, t.TempDir()), "x")
	assert.NoErr(t, os.WriteFile(target, []byte("x"), 0o644))
	assert.NoErr(t, os.Symlink(target, filepath.Join(root, "link")))
	_, err := ExecPolicy{Root: root}.Resolve("link")
	if err == nil {
		t.Fatal("expected error")
	}
	assert.Contains(t, err.Error(), "outside project root")
}

func TestResolve_AllowsNewFileInRoot(t *testing.T) {
	root := realpath(t, t.TempDir())
	got, err := ExecPolicy{Root: root}.Resolve("new/dir/f.go")
	assert.NoErr(t, err)
	assert.Eq(t, got, filepath.Join(root, "new/dir/f.go"))
}

func TestResolve_AllowsRootItself(t *testing.T) {
	root := realpath(t, t.TempDir())
	got, err := ExecPolicy{Root: root}.Resolve(".")
	assert.NoErr(t, err)
	assert.Eq(t, got, root)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	assert.NoErr(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NoErr(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestDeny_FloorPatterns(t *testing.T) {
	root := realpath(t, t.TempDir())
	p := ExecPolicy{Root: root}
	for _, denied := range []string{".env", "sub/.env", ".env.local", "sub/.env.prod", "secrets/a", "secrets/x/y", "key.pem", "a/b/key.pem", "id_rsa", "d/id_rsa.pub", "c.p12", ".git-credentials", ".ENV", "KEY.PEM"} {
		_, err := p.Resolve(denied)
		if err == nil {
			t.Errorf("%s: want denied", denied)
			continue
		}
		assert.Contains(t, err.Error(), "path denied by policy: ")
	}
	for _, ok := range []string{"envoy.go", "secrets.go", "sub/environment.txt", "README.md", "pem.go"} {
		if _, err := p.Resolve(ok); err != nil {
			t.Errorf("%s: want allowed, got %v", ok, err)
		}
	}
}

func TestDeny_ExtraPatternsAddToFloor(t *testing.T) {
	root := realpath(t, t.TempDir())
	p := ExecPolicy{Root: root, Deny: []string{"private/**"}}
	if _, err := p.Resolve("private/x"); err == nil {
		t.Fatal("extra pattern not applied")
	}
	if _, err := p.Resolve(".env"); err == nil {
		t.Fatal("floor must stay")
	}
}

func TestDeny_SymlinkToEnv(t *testing.T) {
	root := realpath(t, t.TempDir())
	writeFile(t, filepath.Join(root, ".env"), "X=1")
	assert.NoErr(t, os.Symlink(".env", filepath.Join(root, "config")))
	_, err := ExecPolicy{Root: root}.Resolve("config")
	if err == nil {
		t.Fatal("symlink to .env must be denied")
	}
	assert.Contains(t, err.Error(), "path denied by policy")
	assert.Eq(t, ExecPolicy{Root: root}.Denied(filepath.Join(root, "config")), true)
}

func TestDeny_HomePatternsWhenRootIsHome(t *testing.T) {
	home := realpath(t, t.TempDir())
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, ".ssh", "id_ed25519"), "k")
	writeFile(t, filepath.Join(home, ".aws", "credentials"), "k")
	writeFile(t, filepath.Join(home, "notes.txt"), "k")
	p := ExecPolicy{Root: home}
	for _, d := range []string{".ssh/id_ed25519", ".aws/credentials"} {
		if _, err := p.Resolve(d); err == nil {
			t.Errorf("%s: want denied", d)
		}
	}
	if _, err := p.Resolve("notes.txt"); err != nil {
		t.Fatal(err)
	}
}
