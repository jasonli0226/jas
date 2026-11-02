package core

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/jasonli0226/jas/providers"
)

// CompactConfig tunes context compaction.
type CompactConfig struct {
	Reserve int    // compact when Count > threshold(window); compact_reserve_tokens
	Keep    int    // tokens kept verbatim; compact_keep_tokens
	Model   string // summary model; "" = session model
}

const (
	DefaultCompactReserve = 16384
	DefaultCompactKeep    = 20000

	summaryToolClip = 2000
	summaryPrefix   = "[jas] The earlier conversation was compacted. Summary:\n\n"
	clippedSuffix   = "…[clipped]"

	summarySystem = "You summarize a coding-agent conversation so it can continue in a fresh context. Do not call tools. Reply with the summary only."
	summaryFormat = `Write a structured summary with these sections:
## Goal
## Constraints & Preferences
## Progress
## Key Decisions
## Next Steps
## Critical Context
Keep exact file paths, commands, error messages and identifiers. Be concise.`
)

var (
	readBlockRe     = regexp.MustCompile(`(?s)\n*<read-files>\n(.*?)\n</read-files>`)
	modifiedBlockRe = regexp.MustCompile(`(?s)\n*<modified-files>\n(.*?)\n</modified-files>`)
)

// compactThreshold is the prompt size above which the session compacts.
func compactThreshold(window, reserve int) int {
	return max(window-reserve, window/2)
}

// compactKeep caps the kept tail at half the room the trigger leaves after
// overhead (system prompt and tool schemas), so a compaction always brings
// the prompt well under the threshold, even on small windows.
func compactKeep(window, reserve, keep, overhead int) int {
	return min(keep, max(0, compactThreshold(window, reserve)-overhead)/2)
}

// cutIndex returns the index of the first message kept verbatim, or 0 when
// there is nothing worth compacting. The cut never lands on a tool-results
// message, so a tool call stays with its results.
func cutIndex(msgs []providers.Message, keepTokens int) int {
	cut, total := -1, 0
	for i := len(msgs) - 1; i >= 0; i-- {
		total += msgBytes(msgs[i])
		if total/bytesPerToken >= keepTokens {
			cut = i
			break
		}
	}
	if cut < 0 {
		return 0
	}
	for cut > 0 && isToolResults(msgs[cut]) {
		cut--
	}
	floor := 0
	if len(msgs) > 0 {
		if _, ok := IsSummary(msgs[0]); ok {
			floor = 1
		}
	}
	if cut <= floor {
		return 0
	}
	return cut
}

// isToolResults reports whether m is a user message of only tool_result blocks.
func isToolResults(m providers.Message) bool {
	if m.Role != providers.RoleUser || len(m.Content) == 0 {
		return false
	}
	for _, b := range m.Content {
		if b.Type != providers.BlockToolResult {
			return false
		}
	}
	return true
}

// IsSummary reports whether m is a compaction summary and returns its text.
func IsSummary(m providers.Message) (string, bool) {
	if m.Role != providers.RoleUser || len(m.Content) != 1 || m.Content[0].Type != providers.BlockText {
		return "", false
	}
	return strings.CutPrefix(m.Content[0].Text, summaryPrefix)
}

func summaryMessage(summary string) providers.Message {
	return providers.Message{Role: providers.RoleUser, Content: []providers.ContentBlock{
		{Type: providers.BlockText, Text: summaryPrefix + summary},
	}}
}

// summaryRequest asks the model to summarize msgs, updating prev if non-empty.
func summaryRequest(model string, msgs []providers.Message, prev, focus string) providers.Request {
	var sb strings.Builder
	sb.WriteString("<conversation>\n" + transcript(msgs) + "\n</conversation>\n\n")
	if prev != "" {
		sb.WriteString("Previous summary to update:\n<previous-summary>\n" + prev + "\n</previous-summary>\n\n")
	}
	sb.WriteString(summaryFormat)
	if focus != "" {
		sb.WriteString("\nPay special attention to: " + focus)
	}
	return providers.Request{
		Model:  model,
		System: summarySystem,
		Messages: []providers.Message{{Role: providers.RoleUser, Content: []providers.ContentBlock{
			{Type: providers.BlockText, Text: sb.String()},
		}}},
	}
}

// transcript renders msgs as one line per block.
func transcript(msgs []providers.Message) string {
	var lines []string
	for _, m := range msgs {
		for _, b := range m.Content {
			switch b.Type {
			case providers.BlockText:
				lines = append(lines, "["+string(m.Role)+"]: "+b.Text)
			case providers.BlockToolUse:
				lines = append(lines, "[tool call] "+b.Name+"("+compactJSON(b.Input)+")")
			case providers.BlockToolResult:
				text := clipRunes(b.Text, summaryToolClip)
				if len(text) < len(b.Text) {
					text += clippedSuffix
				}
				lines = append(lines, "[tool result]: "+text)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// compactJSON minifies raw; invalid JSON (malformed model args) is kept as is.
func compactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

// fileLists merges the file lists carried in prev with the paths touched by
// tool calls in msgs. Both are sorted and de-duplicated; modified wins.
func fileLists(prev string, msgs []providers.Message) (read, modified []string) {
	read, modified = parseFileBlock(readBlockRe, prev), parseFileBlock(modifiedBlockRe, prev)
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type != providers.BlockToolUse {
				continue
			}
			var args struct{ Path string }
			if json.Unmarshal(b.Input, &args) != nil || args.Path == "" {
				continue
			}
			switch b.Name {
			case "read", "ls", "grep":
				read = append(read, args.Path)
			case "write", "edit":
				modified = append(modified, args.Path)
			}
		}
	}
	slices.Sort(read)
	slices.Sort(modified)
	read, modified = slices.Compact(read), slices.Compact(modified)
	read = slices.DeleteFunc(read, func(p string) bool { return slices.Contains(modified, p) })
	return read, modified
}

func parseFileBlock(re *regexp.Regexp, s string) []string {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	return strings.Split(m[1], "\n")
}

// withFileLists replaces any file-list blocks in summary with the given lists.
func withFileLists(summary string, read, modified []string) string {
	summary = readBlockRe.ReplaceAllString(summary, "")
	summary = modifiedBlockRe.ReplaceAllString(summary, "")
	summary = strings.TrimRight(summary, "\n")
	if len(read) > 0 {
		summary += "\n\n<read-files>\n" + strings.Join(read, "\n") + "\n</read-files>"
	}
	if len(modified) > 0 {
		summary += "\n<modified-files>\n" + strings.Join(modified, "\n") + "\n</modified-files>"
	}
	return summary
}
