package core

import (
	"errors"
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
