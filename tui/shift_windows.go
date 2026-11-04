//go:build windows

package tui

import "golang.org/x/sys/windows"

// VK_SHIFT: either Shift key currently down.
const vkShift = 0x10

var (
	modUser32            = windows.NewLazySystemDLL("user32.dll")
	procGetAsyncKeyState = modUser32.NewProc("GetAsyncKeyState")
)

// shiftHeld reports whether a Shift key is currently down on this host.
// Windows Terminal (until Kitty keyboard protocol) and classic ConPTY
// collapse Shift+Enter to plain Enter; polling GetAsyncKeyState matches
// what pi does so Shift+Enter can still insert a newline.
func shiftHeld() bool {
	r, _, _ := procGetAsyncKeyState.Call(uintptr(vkShift))
	// High bit set means the key is currently pressed.
	return r&0x8000 != 0
}
