package dream

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// HashTree maps each regular file under dir (slash-separated relative path)
// to its sha256 hex. A missing dir yields an empty map.
func HashTree(dir string) (map[string]string, error) {
	out := map[string]string{}
	root, err := resolveRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dream: hash %s: %w", dir, err)
	}
	dir = root
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		sum, err := hashFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = sum
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("dream: hash %s: %w", dir, err)
	}
	return out, nil
}

// resolveRoot follows symlinks on the root itself (the store may be a link
// to a folder elsewhere); links inside the tree are never followed.
func resolveRoot(p string) (string, error) {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	return r, nil
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CopyTree copies the regular files and folders under src into dst. Symlinks
// inside src are skipped, never followed; src itself may be a symlink.
func CopyTree(src, dst string) error {
	root, err := resolveRoot(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // a missing source has nothing to copy
	}
	if err != nil {
		return fmt.Errorf("dream: copy %s: %w", src, err)
	}
	src = root
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			return copyFile(p, target)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("dream: copy %s: %w", src, err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// ChangedPaths returns the sorted paths whose hash differs between a and b
// or that exist on one side only.
func ChangedPaths(a, b map[string]string) []string {
	var out []string
	for p, h := range a {
		if bh, ok := b[p]; !ok || bh != h {
			out = append(out, p)
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
