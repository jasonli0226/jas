package core

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/jasonli0226/jas/providers"
)

// Budget defaults (design §3 "Tool execution policy").
const (
	DefaultElideAt         = 0.6
	DefaultKeepToolResults = 6

	bytesPerToken  = 4
	elidedPrefix   = "[elided: "
	maxStubArg     = 80
	elideHeadroom  = 0.1 // fraction of Window that must accumulate between passes
	unknownToolTag = "?"

	// skillFileName marks a read as loading a skill (agentskills.io).
	skillFileName   = "SKILL.md"
	skillRereadHint = "skill instructions: read the file again before following the skill"
)

// ElideStats describes one elision pass.
type ElideStats struct {
	Count        int // tool results replaced
	TokensBefore int
	TokensAfter  int
}

// Budget tracks prompt size against a context window and elides old tool
// results in one chunk when the window fills up. It is not safe for
// concurrent use.
type Budget struct {
	Window  int
	ElideAt float64 // fraction of Window that triggers elision
	Keep    int     // most recent tool results always kept verbatim

	obsTokens int // prompt tokens reported for the first obsMsgs messages
	obsMsgs   int
	lastAfter int // tokens after the last elision pass; 0 before any
}

// Observe records provider-reported prompt tokens for the first messagesSent
// messages. A report of zero tokens (no usage) is ignored.
func (b *Budget) Observe(promptTokens int, messagesSent int) {
	if promptTokens <= 0 {
		return
	}
	b.obsTokens, b.obsMsgs = promptTokens, messagesSent
}

// Rebase forgets the last observation and the elision hysteresis, so Count
// re-estimates from bytes after history is replaced.
func (b *Budget) Rebase() {
	b.obsTokens, b.obsMsgs, b.lastAfter = 0, 0, 0
}

// Count estimates the prompt tokens msgs would use: the last observation plus
// len(bytes)/4 for messages after it (all estimate before any observation).
func (b *Budget) Count(msgs []providers.Message) int {
	n := b.obsMsgs
	if n > len(msgs) {
		n = len(msgs)
	}
	est := 0
	for _, m := range msgs[n:] {
		est += msgBytes(m)
	}
	return b.obsTokens + est/bytesPerToken
}

// WouldExceed reports whether msgs no longer fit in the window.
func (b *Budget) WouldExceed(msgs []providers.Message) bool {
	return b.Count(msgs) > b.Window
}

// MaybeElide returns msgs unchanged (and nil) while Count is below
// ElideAt*Window or nothing is eligible. Otherwise it returns a new slice in
// which every tool_result except the last Keep is replaced by a one-line stub.
// Only tool_result Text changes, so the prefix before the first newly elided
// result stays byte-identical.
func (b *Budget) MaybeElide(msgs []providers.Message) ([]providers.Message, *ElideStats) {
	before := b.Count(msgs)
	// Hysteresis: after a pass, wait for elideHeadroom of the window to
	// accumulate so the cached prefix changes once per pass, not every turn.
	trigger := b.ElideAt * float64(b.Window)
	if b.lastAfter > 0 {
		trigger = max(trigger, float64(b.lastAfter)+elideHeadroom*float64(b.Window))
	}
	if float64(before) < trigger {
		return msgs, nil
	}
	total := 0
	for _, m := range msgs {
		for _, blk := range m.Content {
			if blk.Type == providers.BlockToolResult {
				total++
			}
		}
	}
	limit := total - b.Keep // results with index < limit are eligible
	if limit <= 0 {
		return msgs, nil
	}
	uses := toolUses(msgs)
	out := make([]providers.Message, len(msgs))
	copy(out, msgs)
	idx, count, savedObserved, savedAll := 0, 0, 0, 0
	for i, m := range msgs {
		var content []providers.ContentBlock // lazily cloned
		for j, blk := range m.Content {
			if blk.Type != providers.BlockToolResult {
				continue
			}
			k := idx
			idx++
			if k >= limit || strings.HasPrefix(blk.Text, elidedPrefix) {
				continue
			}
			if content == nil {
				content = append([]providers.ContentBlock(nil), m.Content...)
			}
			stub := elideStub(uses[blk.ToolUseID], blk.Text)
			saved := len(blk.Text) - len(stub)
			savedAll += saved
			if i < b.obsMsgs {
				savedObserved += saved
			}
			content[j].Text = stub
			count++
		}
		if content != nil {
			out[i].Content = content
		}
	}
	if count == 0 {
		return msgs, nil
	}
	// Keep the observation consistent with the shortened history.
	b.obsTokens = max(0, b.obsTokens-savedObserved/bytesPerToken)
	b.lastAfter = max(0, before-savedAll/bytesPerToken)
	return out, &ElideStats{Count: count, TokensBefore: before, TokensAfter: max(0, before-savedAll/bytesPerToken)}
}

func msgBytes(m providers.Message) int {
	n := 0
	for _, b := range m.Content {
		n += len(b.Text) + len(b.Name) + len(b.Input)
	}
	return n
}

func toolUses(msgs []providers.Message) map[string]providers.ContentBlock {
	uses := map[string]providers.ContentBlock{}
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == providers.BlockToolUse {
				uses[b.ToolUseID] = b
			}
		}
	}
	return uses
}

// elideStub builds "[elided: <tool> <first-arg> <N> lines]".
func elideStub(use providers.ContentBlock, text string) string {
	tool := use.Name
	if tool == "" {
		tool = unknownToolTag
	}
	var args map[string]any
	_ = json.Unmarshal(use.Input, &args)
	arg := ""
	for _, key := range []string{"path", "command", "pattern"} {
		if s, ok := args[key].(string); ok && s != "" {
			arg = clipRunes(oneLine(s), maxStubArg)
			break
		}
	}
	lines := strings.Count(text, "\n")
	if text != "" && !strings.HasSuffix(text, "\n") {
		lines++
	}
	if arg == "" {
		return fmt.Sprintf("%s%s %d lines]", elidedPrefix, tool, lines)
	}
	if p, _ := args["path"].(string); tool == "read" && filepath.Base(p) == skillFileName {
		// The model tends to carry on from its memory of a skill; tell it
		// the instructions are gone so it reads them again.
		return fmt.Sprintf("%s%s %s %d lines; %s]", elidedPrefix, tool, arg, lines, skillRereadHint)
	}
	return fmt.Sprintf("%s%s %s %d lines]", elidedPrefix, tool, arg, lines)
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// oneLine turns control characters into spaces and collapses whitespace.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}
