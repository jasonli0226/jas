package core

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jasonli0226/jas/providers"
)

// Read dedup (#217): a read whose output equals the latest earlier read of
// the same file and range, while that result is still intact in the
// session's messages, gets a one-line note instead of the content. Nothing
// is remembered: compaction and elision change the history, and so what
// can match.

// readUnchangedPrefix starts every dedup note.
const readUnchangedPrefix = "unchanged since your read of "

// readKey is what makes two reads the same read.
type readKey struct {
	path          string // resolved
	offset, limit int    // normalized: offset >= 1, limit >= 0
}

// readKeyOf decodes and resolves a read call's arguments; ok is false when
// they do not decode or the path does not resolve.
func (p ExecPolicy) readKeyOf(input json.RawMessage) (k readKey, spelled string, ok bool) {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &a); err != nil {
		return readKey{}, "", false
	}
	full, err := p.ResolveRead(a.Path)
	if err != nil {
		return readKey{}, "", false
	}
	return readKey{path: full, offset: max(a.Offset, 1), limit: max(a.Limit, 0)}, a.Path, true
}

func (k readKey) same(o readKey) bool {
	return k.offset == o.offset && k.limit == o.limit && samePath(k.path, o.path)
}

// dedupRead returns the note for call and true when res repeats the latest
// earlier read of the same key in history byte for byte. That earlier
// result decides alone: a note, an elided stub or an error there means the
// content is sent, so a repeat after a note always gets the file.
func (p ExecPolicy) dedupRead(history []providers.Message, call providers.ContentBlock, res Result) (Result, bool) {
	if call.Name != "read" || res.IsError {
		return res, false
	}
	key, spelled, ok := p.readKeyOf(call.Input)
	if !ok {
		return res, false
	}
	uses := map[string]providers.ContentBlock{}
	at := map[string]int{} // tool_use id -> index of its assistant message
	for i, m := range history {
		for _, b := range m.Content {
			if b.Type == providers.BlockToolUse {
				uses[b.ToolUseID], at[b.ToolUseID] = b, i
			}
		}
	}
	for i := len(history) - 1; i >= 0; i-- {
		content := history[i].Content
		for j := len(content) - 1; j >= 0; j-- {
			b := content[j]
			if b.Type != providers.BlockToolResult || b.ToolUseID == call.ToolUseID {
				continue
			}
			u, found := uses[b.ToolUseID]
			if !found || u.Name != "read" {
				continue
			}
			k, _, ok := p.readKeyOf(u.Input)
			if !ok || !key.same(k) {
				continue
			}
			if b.IsError || b.Text != res.Output ||
				strings.HasPrefix(b.Text, readUnchangedPrefix) || strings.HasPrefix(b.Text, elidedPrefix) {
				return res, false
			}
			return Result{Output: readNote(spelled, turnsAfter(history, at[b.ToolUseID]))}, true
		}
	}
	return res, false
}

// turnsAfter counts the assistant messages in history after index i.
func turnsAfter(history []providers.Message, i int) int {
	n := 0
	for _, m := range history[i+1:] {
		if m.Role == providers.RoleAssistant {
			n++
		}
	}
	return n
}

func readNote(path string, turns int) string {
	unit := "turns"
	if turns == 1 {
		unit = "turn"
	}
	return fmt.Sprintf("%s%s %d %s ago (same range)", readUnchangedPrefix, path, turns, unit)
}
