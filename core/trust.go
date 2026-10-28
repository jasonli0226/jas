package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	trustFileMode = 0o600
	trustDirMode  = 0o700
)

// TrustStore remembers which project context files the user has approved.
// The file maps an absolute, symlink-resolved project root to the hex sha256
// of the approved content, so any edit asks again. core does not know where
// the file lives; the caller passes the path.
type TrustStore struct{ Path string }

// ContentHash is the hex sha256 recorded for context content.
func ContentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// trustKey normalises root to an absolute, symlink-resolved path.
func trustKey(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r, nil
	}
	return abs, nil
}

func (s TrustStore) load() (map[string]string, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read trust store: %w", err)
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse trust store %s: %w", s.Path, err)
	}
	return m, nil
}

// Check reports whether content is the approved content for root.
func (s TrustStore) Check(root string, content []byte) (bool, error) {
	key, err := trustKey(root)
	if err != nil {
		return false, err
	}
	m, err := s.load()
	if err != nil {
		return false, err
	}
	return m[key] == ContentHash(content), nil
}

// Trust records content as approved for root (file mode 0600, atomic).
func (s TrustStore) Trust(root string, content []byte) error {
	key, err := trustKey(root)
	if err != nil {
		return err
	}
	m, err := s.load()
	if err != nil {
		return err
	}
	m[key] = ContentHash(content)
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, trustDirMode); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(s.Path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(trustFileMode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}
