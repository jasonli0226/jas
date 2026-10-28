package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
		if e.IsDir() {
			n += "/"
		}
		names = append(names, n)
	}
	sort.Strings(names)
	return core.Result{Output: Truncate([]byte(strings.Join(names, "\n")), p.OutputLimit())}, nil
}
