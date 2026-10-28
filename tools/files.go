package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jasonli0226/jas/core"
)

// ReadOnly returns the read-only tools: read, ls, grep.
func ReadOnly() []core.Tool {
	return []core.Tool{Read{}, Ls{}, Grep{}}
}

func errResult(format string, a ...any) (core.Result, error) {
	return core.Result{Output: fmt.Sprintf(format, a...), IsError: true}, nil
}

// Read returns the contents of a file.
type Read struct{}

func (Read) Name() string { return "read" }
func (Read) Description() string {
	return "Read a text file from the project, optionally from a 1-based line offset for a limited number of lines."
}
func (Read) Kind() core.Kind { return core.ReadOnly }
func (Read) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"path":{"type":"string","description":"File path, relative to the project root."},` +
		`"offset":{"type":"integer","description":"1-based line to start at."},` +
		`"limit":{"type":"integer","description":"Maximum number of lines to return."}},` +
		`"required":["path"]}`)
}

func (Read) Call(_ context.Context, p core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err)
	}
	full, err := p.Resolve(a.Path)
	if err != nil {
		return errResult("%v", err)
	}
	info, err := os.Stat(full)
	if err != nil {
		return errResult("read %s: %v", a.Path, err)
	}
	if !info.Mode().IsRegular() {
		return errResult("read %s: not a regular file", a.Path)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return errResult("read %s: %v", a.Path, err)
	}
	if a.Offset > 1 || a.Limit > 0 {
		data = sliceLines(data, a.Offset, a.Limit)
	}
	return core.Result{Output: Truncate(data, p.OutputLimit())}, nil
}

// sliceLines keeps lines [offset, offset+limit) (1-based); limit <= 0 means to the end.
func sliceLines(data []byte, offset, limit int) []byte {
	if offset < 1 {
		offset = 1
	}
	lines := strings.SplitAfter(string(data), "\n")
	if offset > len(lines) {
		return nil
	}
	lines = lines[offset-1:]
	if limit > 0 && limit < len(lines) {
		lines = lines[:limit]
	}
	return []byte(strings.Join(lines, ""))
}

// Ls lists a directory.
type Ls struct{}

func (Ls) Name() string { return "ls" }
func (Ls) Description() string {
	return "List the entries of a directory in the project, with directories suffixed by a slash."
}
func (Ls) Kind() core.Kind { return core.ReadOnly }
func (Ls) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"path":{"type":"string","description":"Directory path, relative to the project root; defaults to the root."}},` +
		`"required":[]}`)
}

func (Ls) Call(_ context.Context, p core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	var a struct {
		Path string `json:"path"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return errResult("invalid arguments: %v", err)
		}
	}
	if a.Path == "" {
		a.Path = "."
	}
	full, err := p.Resolve(a.Path)
	if err != nil {
		return errResult("%v", err)
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return errResult("ls %s: %v", a.Path, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if p.Denied(filepath.Join(full, n)) { // also follows a symlink entry to its target
			continue
		}
		if e.IsDir() {
			n += "/"
		}
		names = append(names, n)
	}
	sort.Strings(names)
	return core.Result{Output: Truncate([]byte(strings.Join(names, "\n")), p.OutputLimit())}, nil
}

const (
	newFileMode fs.FileMode = 0o644
	newDirMode  fs.FileMode = 0o755
)

// renameFile is the rename used by atomicWrite; tests swap it to inject failures.
var renameFile = os.Rename

// Mutating returns the mutating tools: write, edit, shell.
func Mutating() []core.Tool {
	return []core.Tool{Write{}, Edit{}, Shell{}}
}

// atomicWrite writes data to path via a temp file in the same directory,
// fsyncs it, renames it over path, then fsyncs the directory. On any failure
// the original file is untouched and the temp file is removed.
func atomicWrite(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".jas-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return cleanup(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(err)
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := renameFile(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

const redactedRegionMsg = "region contains redacted content; re-read the file and edit around the redacted value"

// refuseRedacted refuses a mutation whose text carries a redaction marker
// (the model would otherwise persist "[REDACTED:...]" over the real value),
// unless the file matches redact_skip_paths.
func refuseRedacted(p core.ExecPolicy, full string, texts ...string) (core.Result, bool) {
	if p.RedactSkipped(full) {
		return core.Result{}, false
	}
	for _, t := range texts {
		if strings.Contains(t, core.RedactedMarker) {
			return core.Result{Output: redactedRegionMsg, IsError: true}, true
		}
	}
	return core.Result{}, false
}

// Write creates or overwrites a file atomically.
type Write struct{}

func (Write) Name() string { return "write" }
func (Write) Description() string {
	return "Create or overwrite a file in the project with the given content. Parent directories are created. The write is atomic."
}
func (Write) Kind() core.Kind { return core.Mutating }
func (Write) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"path":{"type":"string","description":"File path, relative to the project root."},` +
		`"content":{"type":"string","description":"Full new content of the file."}},` +
		`"required":["path","content"]}`)
}

func (Write) Call(_ context.Context, p core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	var a struct {
		Path    string  `json:"path"`
		Content *string `json:"content"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err)
	}
	if a.Content == nil {
		return errResult("invalid arguments: content is required")
	}
	full, err := p.Resolve(a.Path)
	if err != nil {
		return errResult("%v", err)
	}
	if res, refused := refuseRedacted(p, full, *a.Content); refused {
		return res, nil
	}
	mode := newFileMode
	if info, err := os.Stat(full); err == nil {
		if !info.Mode().IsRegular() {
			return errResult("write %s: not a regular file", a.Path)
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return errResult("write %s: %v", a.Path, err)
	}
	if err := os.MkdirAll(filepath.Dir(full), newDirMode); err != nil {
		return errResult("write %s: %v", a.Path, err)
	}
	if err := atomicWrite(full, []byte(*a.Content), mode); err != nil {
		return errResult("write %s: %v", a.Path, err)
	}
	return core.Result{Output: fmt.Sprintf("wrote %s (%d bytes)", a.Path, len(*a.Content))}, nil
}

// Edit replaces one exact occurrence of a string in a file.
type Edit struct{}

func (Edit) Name() string { return "edit" }
func (Edit) Description() string {
	return "Replace exactly one occurrence of old_string with new_string in a file. Matching is exact (including whitespace and line endings); fails if old_string is missing or matches more than once."
}
func (Edit) Kind() core.Kind { return core.Mutating }
func (Edit) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"path":{"type":"string","description":"File path, relative to the project root."},` +
		`"old_string":{"type":"string","description":"Exact text to replace; must occur exactly once."},` +
		`"new_string":{"type":"string","description":"Replacement text."}},` +
		`"required":["path","old_string","new_string"]}`)
}

func (Edit) Call(_ context.Context, p core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	var a struct {
		Path      string  `json:"path"`
		OldString string  `json:"old_string"`
		NewString *string `json:"new_string"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err)
	}
	if a.OldString == "" {
		return errResult("old_string must not be empty")
	}
	if a.NewString == nil {
		return errResult("invalid arguments: new_string is required")
	}
	full, err := p.Resolve(a.Path)
	if err != nil {
		return errResult("%v", err)
	}
	if res, refused := refuseRedacted(p, full, a.OldString, *a.NewString); refused {
		return res, nil
	}
	info, err := os.Stat(full)
	if err != nil {
		return errResult("edit %s: %v", a.Path, err)
	}
	if !info.Mode().IsRegular() {
		return errResult("edit %s: not a regular file", a.Path)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return errResult("edit %s: %v", a.Path, err)
	}
	content := string(data)
	switch n := strings.Count(content, a.OldString); n {
	case 0:
		return errResult("old_string not found in %s", a.Path)
	case 1:
	default:
		return errResult("old_string matches %d times in %s; include more surrounding context", n, a.Path)
	}
	updated := strings.Replace(content, a.OldString, *a.NewString, 1)
	if err := atomicWrite(full, []byte(updated), info.Mode().Perm()); err != nil {
		return errResult("edit %s: %v", a.Path, err)
	}
	return core.Result{Output: "edited " + a.Path}, nil
}
