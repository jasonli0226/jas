package testutil

import "testing"

// TempHome points JAS_HOME at a fresh temp dir for the test and returns it.
// HOME (USERPROFILE on Windows) moves to a second, empty temp dir so user
// skills and other dotfiles on the machine running the tests stay out.
func TempHome(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("JAS_HOME", dir)
	user := t.TempDir()
	t.Setenv("HOME", user)
	t.Setenv("USERPROFILE", user)
	return dir
}
