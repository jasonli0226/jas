package tui

import (
	"strings"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/providers"
)

const historyCap = 100 // most submitted inputs Up/Down remembers

// history is the ring of submitted inputs that Up and Down walk, oldest
// first. Browsing never changes the entries: an edited recall only becomes
// one when it is submitted.
type history struct {
	items []string
	pos   int    // entry being shown; len(items) when not browsing
	draft string // what was being typed when browsing began
}

// add records a submitted input, collapsing a repeat of the newest entry,
// and ends browsing.
func (h *history) add(s string) {
	if s != "" && (len(h.items) == 0 || h.items[len(h.items)-1] != s) {
		h.items = append(h.items, s)
		if over := len(h.items) - historyCap; over > 0 {
			h.items = h.items[over:]
		}
	}
	h.pos, h.draft = len(h.items), ""
}

// prev steps to the next older entry; cur is kept as the draft when browsing
// begins. ok is false at the oldest entry.
func (h *history) prev(cur string) (string, bool) {
	if h.pos == 0 {
		return "", false
	}
	if h.pos == len(h.items) {
		h.draft = cur
	}
	h.pos--
	return h.items[h.pos], true
}

// next steps to the next newer entry, then back to the draft. ok is false
// when not browsing.
func (h *history) next() (string, bool) {
	if h.pos >= len(h.items) {
		return "", false
	}
	h.pos++
	if h.pos == len(h.items) {
		return h.draft, true
	}
	return h.items[h.pos], true
}

// seed adds the user's typed messages from a resumed session, skipping tool
// results and the compaction summary.
func (h *history) seed(msgs []providers.Message) {
	for _, msg := range msgs {
		if _, ok := core.IsSummary(msg); ok || msg.Role != providers.RoleUser {
			continue
		}
		var parts []string
		for _, b := range msg.Content {
			if b.Type == providers.BlockText {
				parts = append(parts, b.Text)
			}
		}
		h.add(strings.Join(parts, "\n"))
	}
}
