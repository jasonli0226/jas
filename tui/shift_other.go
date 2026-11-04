//go:build !windows

package tui

// shiftHeld is always false off Windows. Kitty / modifyOtherKeys already
// report Shift+Enter as a distinct key where the terminal supports them;
// WSL and SSH cannot see the Windows host's GetAsyncKeyState.
func shiftHeld() bool { return false }
