//go:build windows

package tools

// syncDir is a no-op on windows: a directory cannot be flushed through a
// read-only handle (FlushFileBuffers fails with access denied), and NTFS
// journals the rename's metadata itself.
func syncDir(dir string) error { return nil }
