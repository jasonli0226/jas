// Package memory is the profile-scoped memory store.
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	maxKeyLen  = 200 // longer keys are shortened and hashed
	keyPrefix  = 180 // bytes of the encoded path kept in a hashed key
	hashChars  = 16  // hex chars of SHA-256 kept in a hashed key
	hashMarker = '~'
	hexUpper   = "0123456789ABCDEF"
)

func isPlain(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_'
}

// Key encodes a project root as a single directory name. Every byte outside
// A-Z a-z 0-9 . _ becomes %XX (uppercase hex), then %2F is turned into "-",
// so the encoding is injective. Keys over 200 bytes are cut to 180 bytes plus
// "~" and 16 hex chars of the full path's SHA-256.
func Key(root string) string {
	var b strings.Builder
	for i := 0; i < len(root); i++ {
		c := root[i]
		switch {
		case isPlain(c):
			b.WriteByte(c)
		case c == '/':
			b.WriteByte('-')
		default:
			b.WriteByte('%')
			b.WriteByte(hexUpper[c>>4])
			b.WriteByte(hexUpper[c&15])
		}
	}
	key := b.String()
	if len(key) <= maxKeyLen {
		return key
	}
	sum := sha256.Sum256([]byte(root))
	return key[:keyPrefix] + string(hashMarker) + hex.EncodeToString(sum[:])[:hashChars]
}

// DecodeKey reverses Key. It reports false for a hashed key (the path is not
// recoverable) or a malformed one.
func DecodeKey(key string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case isPlain(c):
			b.WriteByte(c)
		case c == '-':
			b.WriteByte('/')
		case c == '%' && i+2 < len(key):
			hi, lo := unhex(key[i+1]), unhex(key[i+2])
			if hi < 0 || lo < 0 {
				return "", false
			}
			b.WriteByte(byte(hi<<4 | lo))
			i += 2
		default:
			return "", false
		}
	}
	return b.String(), true
}

// unhex decodes one uppercase hex digit, or returns -1.
func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
