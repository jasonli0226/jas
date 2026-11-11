package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	MaxIndexLines   = 200
	MaxIndexBytes   = 25 << 10
	MinProjectLines = 50
	MinProjectBytes = 6 << 10

	indexFile = "MEMORY.md"
)

// Dirs are the two memory folders: the profile store and this project's folder.
type Dirs struct{ Profile, Project string }

// DirsFor places the stores under profileDir. mainRoot must be absolute.
func DirsFor(profileDir, mainRoot string) (Dirs, error) {
	if !filepath.IsAbs(mainRoot) {
		return Dirs{}, fmt.Errorf("memory: project root %q is not an absolute path", mainRoot)
	}
	profile := filepath.Join(profileDir, "memory")
	return Dirs{Profile: profile, Project: filepath.Join(profile, "projects", Key(mainRoot))}, nil
}

// Index is one loaded MEMORY.md. Lines and Bytes count the file before the
// cap; Text is what the prompt gets, truncation line included.
type Index struct {
	Path, Text string
	Lines      int
	Bytes      int
	Truncated  bool
	SHA256     string
	Exists     bool
}

// LoadIndexes reads both indexes under one shared cap: the profile index
// first, the project index keeping at least MinProject* of the budget.
// Missing files are not errors. hideProject skips the project index.
func LoadIndexes(d Dirs, hideProject bool) (profile, project Index, err error) {
	profile, pLines, err := readIndex(filepath.Join(d.Profile, indexFile))
	if err != nil {
		return Index{}, Index{}, err
	}
	project = Index{Path: filepath.Join(d.Project, indexFile)}
	var qLines []string
	if !hideProject {
		if project, qLines, err = readIndex(project.Path); err != nil {
			return Index{}, Index{}, err
		}
	}
	qNeedLines, qNeedBytes := len(qLines), byteLen(qLines)
	profLines, profBytes := MaxIndexLines-min(qNeedLines, MinProjectLines), MaxIndexBytes-min(qNeedBytes, MinProjectBytes)
	kept := keep(pLines, profLines, profBytes)
	applyCap(&profile, pLines, kept)
	if profile.Exists {
		usedBytes := byteLen(pLines[:kept])
		kept = keep(qLines, MaxIndexLines-kept, MaxIndexBytes-usedBytes)
	} else {
		kept = keep(qLines, MaxIndexLines, MaxIndexBytes)
	}
	applyCap(&project, qLines, kept)
	return profile, project, nil
}

func readIndex(path string) (Index, []string, error) {
	idx := Index{Path: path}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return idx, nil, nil
	}
	if err != nil {
		return Index{}, nil, fmt.Errorf("memory: read index: %w", err)
	}
	sum := sha256.Sum256(raw)
	idx.Exists, idx.Bytes, idx.SHA256 = true, len(raw), hex.EncodeToString(sum[:])
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	var ls []string
	if text != "" {
		ls = strings.Split(text, "\n")
	}
	idx.Lines = len(ls)
	return idx, ls, nil
}

func byteLen(ls []string) int {
	n := 0
	for _, l := range ls {
		n += len(l) + 1
	}
	return n
}

// keep returns how many leading lines fit both budgets.
func keep(ls []string, maxLines, maxBytes int) int {
	n, used := 0, 0
	for _, l := range ls {
		if n >= maxLines || used+len(l)+1 > maxBytes {
			break
		}
		n++
		used += len(l) + 1
	}
	return n
}

func applyCap(idx *Index, ls []string, kept int) {
	if !idx.Exists {
		return
	}
	var b strings.Builder
	for _, l := range ls[:kept] {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if kept < len(ls) {
		idx.Truncated = true
		fmt.Fprintf(&b, "(memory index truncated at %d lines; prune it)\n", kept)
	}
	idx.Text = b.String()
}
