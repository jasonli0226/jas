package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
		`"path":{"type":"string","description":"File path, relative to the project root, or an absolute path inside a listed skill's directory."},` +
		`"offset":{"type":"integer","description":"1-based line to start at."},` +
		`"limit":{"type":"integer","description":"Maximum number of lines to return."}},` +
		`"required":["path"]}`)
}

func (Read) Call(ctx context.Context, p core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err)
	}
	full, err := p.ResolveRead(a.Path)
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
	data, err := readFileCtx(ctx, full)
	if err != nil {
		if ok, cerr := cancelled(ctx, "read"); ok {
			return core.Result{}, cerr
		}
		return errResult("read %s: %v", a.Path, err)
	}
	// Redact key blocks over the whole file: a slice may start after BEGIN.
	data = []byte(core.RedactPrivateKeys(string(data)))
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
		`"path":{"type":"string","description":"Directory path, relative to the project root (or absolute inside a listed skill's directory); defaults to the root."}},` +
		`"required":[]}`)
}

func (Ls) Call(ctx context.Context, p core.ExecPolicy, args json.RawMessage) (core.Result, error) {
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
	full, err := p.ResolveRead(a.Path)
	if err != nil {
		return errResult("%v", err)
	}
	scope := p.ReadScope(full)
	if ok, cerr := cancelled(ctx, "ls"); ok {
		return core.Result{}, cerr
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return errResult("ls %s: %v", a.Path, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if ok, cerr := cancelled(ctx, "ls"); ok {
			return core.Result{}, cerr
		}
		n := e.Name()
		if scope.Denied(filepath.Join(full, n)) { // also follows a symlink entry to its target
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
// fsyncs it, renames it over path, then fsyncs the directory (unix only).
// On any failure, including ctx ending before the rename, the original file
// is untouched and the temp file is removed.
func atomicWrite(ctx context.Context, path string, data []byte, mode fs.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	if err := copyCtx(ctx, tmp, bytes.NewReader(data)); err != nil {
		return cleanup(err)
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := renameFile(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return syncDir(dir)
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

// writePlan is the validated, not yet applied, result of a write call.
type writePlan struct {
	path    string // as given by the model
	full    string
	mode    fs.FileMode
	content string
	change  *core.Change
}

// plan reads and validates a write call without changing anything. A non-nil
// result is the error to return to the model; a non-nil error is a cancellation.
func (Write) plan(ctx context.Context, p core.ExecPolicy, args json.RawMessage) (writePlan, *core.Result, error) {
	fail := func(format string, a ...any) (writePlan, *core.Result, error) {
		res, _ := errResult(format, a...)
		return writePlan{}, &res, nil
	}
	var a struct {
		Path    string  `json:"path"`
		Content *string `json:"content"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return fail("invalid arguments: %v", err)
	}
	if a.Content == nil {
		return fail("invalid arguments: content is required")
	}
	full, err := p.Resolve(a.Path)
	if err != nil {
		return fail("%v", err)
	}
	if res, refused := refuseRedacted(p, full, *a.Content); refused {
		return writePlan{}, &res, nil
	}
	mode := newFileMode
	ch := &core.Change{Path: a.Path, New: *a.Content}
	if info, err := os.Stat(full); err == nil {
		if !info.Mode().IsRegular() {
			return fail("write %s: not a regular file", a.Path)
		}
		mode = info.Mode().Perm()
		// The old file is read only for the diff: one over the cap, or one
		// that fails to read (ctx ending is caught below), just means no diff.
		ch = nil
		if info.Size() <= core.MaxDiffSource {
			if old, err := readFileCtx(ctx, full); err == nil {
				ch = &core.Change{Path: a.Path, Old: string(old), New: *a.Content, HideRemoved: true}
			}
		}
	} else if !os.IsNotExist(err) {
		return fail("write %s: %v", a.Path, err)
	}
	if ok, cerr := cancelled(ctx, "write"); ok {
		return writePlan{}, nil, cerr
	}
	return writePlan{path: a.Path, full: full, mode: mode, content: *a.Content, change: ch}, nil, nil
}

func (w Write) Call(ctx context.Context, p core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	pl, res, err := w.plan(ctx, p, args)
	if err != nil {
		return core.Result{}, err
	}
	if res != nil {
		return *res, nil
	}
	if err := os.MkdirAll(filepath.Dir(pl.full), newDirMode); err != nil {
		return errResult("write %s: %v", pl.path, err)
	}
	if err := atomicWrite(ctx, pl.full, []byte(pl.content), pl.mode); err != nil {
		if ok, cerr := cancelled(ctx, "write"); ok {
			return core.Result{}, cerr
		}
		return errResult("write %s: %v", pl.path, err)
	}
	return core.Result{Output: fmt.Sprintf("wrote %s (%d bytes)", pl.path, len(pl.content)), Change: pl.change}, nil
}

// Preview reports what Call would change, without writing.
func (w Write) Preview(ctx context.Context, p core.ExecPolicy, args json.RawMessage) (*core.Change, error) {
	pl, res, err := w.plan(ctx, p, args)
	if err != nil {
		return nil, err
	}
	if res != nil {
		return nil, errors.New(res.Output)
	}
	return pl.change, nil
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

// editPlan is the validated, not yet applied, result of an edit call.
type editPlan struct {
	path    string // as given by the model
	full    string
	mode    fs.FileMode
	old     string
	updated string
}

// plan reads and validates an edit call without changing anything. A non-nil
// result is the error to return to the model; a non-nil error is a cancellation.
func (Edit) plan(ctx context.Context, p core.ExecPolicy, args json.RawMessage) (editPlan, *core.Result, error) {
	fail := func(format string, a ...any) (editPlan, *core.Result, error) {
		res, _ := errResult(format, a...)
		return editPlan{}, &res, nil
	}
	var a struct {
		Path      string  `json:"path"`
		OldString string  `json:"old_string"`
		NewString *string `json:"new_string"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return fail("invalid arguments: %v", err)
	}
	if a.OldString == "" {
		return fail("old_string must not be empty")
	}
	if a.NewString == nil {
		return fail("invalid arguments: new_string is required")
	}
	full, err := p.Resolve(a.Path)
	if err != nil {
		return fail("%v", err)
	}
	if res, refused := refuseRedacted(p, full, a.OldString, *a.NewString); refused {
		return editPlan{}, &res, nil
	}
	info, err := os.Stat(full)
	if err != nil {
		return fail("edit %s: %v", a.Path, err)
	}
	if !info.Mode().IsRegular() {
		return fail("edit %s: not a regular file", a.Path)
	}
	data, err := readFileCtx(ctx, full)
	if err != nil {
		if ok, cerr := cancelled(ctx, "edit"); ok {
			return editPlan{}, nil, cerr
		}
		return fail("edit %s: %v", a.Path, err)
	}
	content := string(data)
	switch n := strings.Count(content, a.OldString); n {
	case 0:
		return fail("old_string not found in %s", a.Path)
	case 1:
	default:
		return fail("old_string matches %d times in %s; include more surrounding context", n, a.Path)
	}
	updated := strings.Replace(content, a.OldString, *a.NewString, 1)
	return editPlan{path: a.Path, full: full, mode: info.Mode().Perm(), old: content, updated: updated}, nil, nil
}

func (e Edit) Call(ctx context.Context, p core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	pl, res, err := e.plan(ctx, p, args)
	if err != nil {
		return core.Result{}, err
	}
	if res != nil {
		return *res, nil
	}
	if err := atomicWrite(ctx, pl.full, []byte(pl.updated), pl.mode); err != nil {
		if ok, cerr := cancelled(ctx, "edit"); ok {
			return core.Result{}, cerr
		}
		return errResult("edit %s: %v", pl.path, err)
	}
	return core.Result{
		Output: "edited " + pl.path,
		Change: pl.change(),
	}, nil
}

// Preview reports what Call would change, without writing.
func (e Edit) Preview(ctx context.Context, p core.ExecPolicy, args json.RawMessage) (*core.Change, error) {
	pl, res, err := e.plan(ctx, p, args)
	if err != nil {
		return nil, err
	}
	if res != nil {
		return nil, errors.New(res.Output)
	}
	return pl.change(), nil
}

// change is the edit's diff source. It is screen-only: the diff's context
// lines are file content the model never sent.
func (pl editPlan) change() *core.Change {
	return &core.Change{Path: pl.path, Old: pl.old, New: pl.updated, ScreenOnly: true}
}
