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

// checkPlainTree refuses a tree holding anything but regular files and
// folders (a link, device, socket...), which a copy would drop.
func checkPlainTree(dir string) error {
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a plain file or folder (%s); only those can be copied", p, d.Type())
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("dream: check %s: %w", dir, err)
	}
	return nil
}

// copyPlainTree copies src (a real folder, not a link) to dst, which must
// not exist, keeping permission bits. Any entry that is not a regular file
// or folder is an error, so nothing is silently dropped.
func copyPlainTree(src, dst string) error {
	if err := checkPlainTree(src); err != nil {
		return err
	}
	type dirMode struct {
		path string
		mode fs.FileMode
	}
	var dirs []dirMode
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		switch {
		case d.IsDir():
			// Writable while filling; the real mode is set at the end.
			if err := os.Mkdir(target, mode|0o700); err != nil {
				return err
			}
			dirs = append(dirs, dirMode{target, mode})
			return nil
		case d.Type().IsRegular():
			return copyFileMode(p, target, mode)
		}
		return fmt.Errorf("%s is not a plain file or folder (%s)", p, d.Type())
	})
	for i := len(dirs) - 1; err == nil && i >= 0; i-- {
		err = os.Chmod(dirs[i].path, dirs[i].mode)
	}
	if err != nil {
		return fmt.Errorf("dream: copy %s: %w", src, err)
	}
	return nil
}

func copyFileMode(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode) // OpenFile's mode is masked by the umask
}
