package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestInstallManualSkills(t *testing.T) {
	root := t.TempDir()

	if err := installManualSkills(root); err != nil {
		t.Fatalf("install: %v", err)
	}
	path := filepath.Join(root, "jas", "SKILL.md")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	want, err := manualSkills.ReadFile("manual/jas/SKILL.md")
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	assert.Eq(t, string(got), string(want))

	// A second run is a no-op, and a stale copy is overwritten.
	if err := installManualSkills(root); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	stale := []byte("stale")
	if err := os.WriteFile(path, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installManualSkills(root); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assert.Eq(t, string(got), string(want))

	// A file the user dropped beside the manual is untouched.
	other := filepath.Join(root, "jas", "extra.md")
	if err := os.WriteFile(other, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installManualSkills(root); err != nil {
		t.Fatalf("third install: %v", err)
	}
	got, err = os.ReadFile(other)
	if err != nil {
		t.Fatal(err)
	}
	assert.Eq(t, string(got), string(stale))
}
