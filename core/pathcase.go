package core

import (
	"runtime"
	"strings"
)

// samePath reports whether a and b name the same directory. Windows and macOS
// default to case-insensitive filesystems, so case is ignored there.
func samePath(a, b string) bool {
	a, b = canonPath(a), canonPath(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
