package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/jasonli0226/jas/core"
)

const trustFile = "trust.json"

// contextFileMeta is one session_started.context_files entry.
type contextFileMeta struct {
	Path   string `json:"path"`
	Loaded bool   `json:"loaded"`
	SHA256 string `json:"sha256"`
}

// loadProjectContext finds the project context file, applies the trust gate
// and returns the system prompt plus the session_started entries. The file
// content is redacted and capped before it reaches the prompt.
func loadProjectContext(ctx context.Context, root, home string, interactive, flagTrust bool,
	in *bufio.Reader, out io.Writer, redact func(string) string) (string, []contextFileMeta, error) {
	path, content, ok := core.FindContextFile(root)
	if !ok {
		return core.BuildSystem(core.BasePrompt, ""), []contextFileMeta{}, nil
	}
	name := filepath.Base(path)
	store := core.TrustStore{Path: filepath.Join(home, trustFile)}
	load, err := core.DecideContextFile(name, root, content, store, interactive, flagTrust,
		func(p string) string { return askLine(ctx, in, out, p) })
	if err != nil {
		return "", nil, err
	}
	meta := []contextFileMeta{{Path: path, Loaded: load, SHA256: core.ContentHash(content)}}
	if !load {
		return core.BuildSystem(core.BasePrompt, ""), meta, nil
	}
	text := core.CapContext(redact(string(content)))
	return core.BuildSystem(core.BasePrompt, fmt.Sprintf("Project instructions (%s):\n%s", name, text)), meta, nil
}

// askLine writes prompt to out and reads one line from in. It returns "" (a
// refusal) when ctx is cancelled first; the reader goroutine is abandoned,
// which is fine because a cancelled run ends the process.
func askLine(ctx context.Context, in *bufio.Reader, out io.Writer, prompt string) string {
	ch := make(chan string, 1)
	go func() {
		_, _ = io.WriteString(out, prompt)
		line, _ := in.ReadString('\n')
		ch <- line
	}()
	select {
	case <-ctx.Done():
		return ""
	case line := <-ch:
		return line
	}
}
