package testutil

import "testing"

// TempHome points JAS_HOME at a fresh temp dir for the test and returns it.
func TempHome(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("JAS_HOME", dir)
	return dir
}
