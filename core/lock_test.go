package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestLockPath_SecondHolderFails(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", ".lock")
	release, err := LockPath(p)
	assert.NoErr(t, err)
	_, err = LockPath(p)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	assert.Eq(t, err.Error(), "locked by another jas process")
	assert.NoErr(t, release())
	release2, err := LockPath(p)
	assert.NoErr(t, err)
	assert.NoErr(t, release2())
}

func TestLockExisting_MissingFileNotCreated(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".lock")
	_, err := LockExisting(p)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want ErrNotExist", err)
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock file created: %v", err)
	}
}

func TestLockExisting_SecondHolderFails(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".lock")
	release, err := LockPath(p)
	assert.NoErr(t, err)
	if _, err := LockExisting(p); !errors.Is(err, ErrLocked) {
		t.Fatalf("err = %v, want ErrLocked", err)
	}
	assert.NoErr(t, release())
	release2, err := LockExisting(p)
	assert.NoErr(t, err)
	assert.NoErr(t, release2())
}
