package core

import (
	"runtime"
	"strings"
)

// foldsCase reports whether goos defaults to a case-insensitive filesystem.
func foldsCase(goos string) bool { return goos == "windows" || goos == "darwin" }

// samePath reports whether a and b name the same directory. Windows and macOS
// default to case-insensitive filesystems, so case is ignored there.
func samePath(a, b string) bool { return samePathOn(runtime.GOOS, a, b) }

// samePathOn is samePath with the OS named, so every rule is tested on any OS.
func samePathOn(goos, a, b string) bool {
	a, b = canonPath(a), canonPath(b)
	if foldsCase(goos) {
		return strings.EqualFold(a, b)
	}
	return a == b
}
