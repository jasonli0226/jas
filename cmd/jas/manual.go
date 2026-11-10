package main

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

// manualSkills holds jas's own skills. syncManualSkills installs the tree
// into the user cache dir (<UserCacheDir>/jas/manual), mirroring the embed
// layout: manual/jas/SKILL.md becomes <cache>/jas/manual/jas/SKILL.md.
// Real files are required: discovery reads SKILL.md from disk and the
// catalog hands the model that path to read.
//
//go:embed manual
var manualSkills embed.FS

// installManualSkills writes every embedded skill file under dstRoot, a
// missing or stale copy included, so a new jas brings its own docs. The
// files are jas's, version-pinned by the binary: a differing file is
// overwritten, not merged. Skills a user drops beside them are untouched.
func installManualSkills(dstRoot string) error {
	src, err := fs.Sub(manualSkills, "manual")
	if err != nil {
		return err
	}
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(dstRoot, filepath.FromSlash(p))
		if cur, err := os.ReadFile(dst); err == nil && bytes.Equal(cur, data) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
}

// manualDir is where the built-in manual skills are installed and read from,
// or "" when the platform has no user cache dir.
func manualDir() string {
	cache, err := os.UserCacheDir()
	if err != nil || cache == "" {
		return ""
	}
	return filepath.Join(cache, "jas", "manual")
}

// syncManualSkills installs the embedded manual skills into manualDir. It is
// best effort: prepare warns on error and the session runs without them. No
// cache dir skips silently.
func syncManualSkills() error {
	dir := manualDir()
	if dir == "" {
		return nil
	}
	return installManualSkills(dir)
}
