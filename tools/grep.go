package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jasonli0226/jas/core"
)

const (
	grepMaxMatches  = 200
	grepMaxFileSize = 1 << 20
	grepBinarySniff = 8 << 10
)

// Grep searches file contents with a regular expression.
type Grep struct{}

func (Grep) Name() string { return "grep" }
func (Grep) Description() string {
	return "Search project files for a regular expression and return matching lines as path:line:text."
}
func (Grep) Kind() core.Kind { return core.ReadOnly }
func (Grep) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"pattern":{"type":"string","description":"Regular expression (Go RE2 syntax)."},` +
		`"path":{"type":"string","description":"File or directory to search, relative to the project root; defaults to the root."},` +
		`"glob":{"type":"string","description":"Only search files whose name matches this glob, e.g. *.go."}},` +
		`"required":["pattern"]}`)
}

func (Grep) Call(ctx context.Context, p core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	var a struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Glob    string `json:"glob"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err)
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return errResult("invalid regex: %v", err)
	}
	if a.Glob != "" {
		if _, err := filepath.Match(a.Glob, ""); err != nil {
			return errResult("invalid glob: %v", err)
		}
	}
	if a.Path == "" {
		a.Path = "."
	}
	start, err := p.Resolve(a.Path)
	if err != nil {
		return errResult("%v", err)
	}
	if _, err := os.Stat(start); err != nil {
		return errResult("grep %s: %v", a.Path, err)
	}
	root, err := filepath.EvalSymlinks(p.Root)
	if err != nil {
		return errResult("project root: %v", err)
	}

	denied := p.DenyMatcher()
	var lines []string
	total := 0
	walkErr := filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			return nil // unreadable entry: skip
		}
		if denied(path) { // silently skip every denied entry, not just the start
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() { // also skips symlinks, which could escape the root
			return nil
		}
		if a.Glob != "" && !globMatches(a.Glob, root, path) {
			return nil
		}
		grepFile(path, re, func(n int, text string) {
			total++
			if len(lines) < grepMaxMatches {
				rel, rerr := filepath.Rel(root, path)
				if rerr != nil {
					rel = path
				}
				lines = append(lines, fmt.Sprintf("%s:%d:%s", filepath.ToSlash(rel), n, text))
			}
		})
		return nil
	})
	if walkErr != nil {
		return errResult("grep: %v", walkErr)
	}
	if total == 0 {
		return core.Result{Output: "no matches"}, nil
	}
	out := strings.Join(lines, "\n")
	if total > grepMaxMatches {
		out += fmt.Sprintf("\n[%d of %d matches]", grepMaxMatches, total)
	}
	return core.Result{Output: Truncate([]byte(out), p.OutputLimit())}, nil
}

// globMatches matches the glob against the base name, or the root-relative
// path when the glob contains a slash.
func globMatches(glob, root, path string) bool {
	target := filepath.Base(path)
	if strings.Contains(glob, "/") {
		if rel, err := filepath.Rel(root, path); err == nil {
			target = filepath.ToSlash(rel)
		}
	}
	ok, _ := filepath.Match(glob, target)
	return ok
}

// grepFile calls emit for every matching line; binary and oversized files are skipped.
func grepFile(path string, re *regexp.Regexp, emit func(line int, text string)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || info.Size() > grepMaxFileSize {
		return
	}
	r := bufio.NewReaderSize(f, 64<<10)
	head, _ := r.Peek(grepBinarySniff)
	if bytes.IndexByte(head, 0) >= 0 {
		return
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return
	}
	// Collapse PEM private-key blocks first: body lines have no BEGIN header
	// of their own, so per-line redaction could never recognise them.
	sc := bufio.NewScanner(strings.NewReader(core.RedactPrivateKeys(string(data))))
	sc.Buffer(make([]byte, 0, 64<<10), grepMaxFileSize)
	for n := 1; sc.Scan(); n++ {
		if re.Match(sc.Bytes()) {
			emit(n, sc.Text())
		}
	}
}
