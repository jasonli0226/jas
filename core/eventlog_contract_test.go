package core

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

// The event-log contract (docs/event-log.md) and its v1 fixture must cover
// every event type jas writes. Changing what the fixture holds or how it
// replays is a contract change.

const (
	eventLogDoc     = "../docs/event-log.md"
	eventLogFixture = "testdata/eventlog/v1.jsonl"
)

// eventTypes returns the value of every Ev* constant in events.go.
func eventTypes(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("events.go")
	assert.NoErr(t, err)
	var out []string
	for _, m := range regexp.MustCompile(`(?m)^\tEv\w+\s*=\s*"(\w+)"`).FindAllSubmatch(src, -1) {
		out = append(out, string(m[1]))
	}
	if len(out) == 0 {
		t.Fatal("no Ev constants found in events.go")
	}
	slices.Sort(out)
	return out
}

func TestEventLogDoc_SectionPerEventType(t *testing.T) {
	doc, err := os.ReadFile(eventLogDoc)
	assert.NoErr(t, err)
	_, section, ok := strings.Cut(string(doc), "\n## Event types\n")
	if !ok {
		t.Fatal("docs/event-log.md has no \"## Event types\" section")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	var documented []string
	for _, m := range regexp.MustCompile("(?m)^\\*\\*`(\\w+)`\\*\\*").FindAllStringSubmatch(section, -1) {
		documented = append(documented, m[1])
	}
	slices.Sort(documented)
	assert.Eq(t, strings.Join(documented, " "), strings.Join(eventTypes(t), " "))
}

func TestEventLogFixture_HoldsEveryEventType(t *testing.T) {
	f, err := os.Open(eventLogFixture)
	assert.NoErr(t, err)
	defer f.Close()
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for n := int64(1); sc.Scan(); n++ {
		var h struct {
			V        int     `json:"v"`
			ID       string  `json:"id"`
			ParentID *string `json:"parent_id"`
			Seq      int64   `json:"seq"`
			Type     string  `json:"type"`
		}
		assert.NoErr(t, json.Unmarshal(sc.Bytes(), &h))
		assert.Eq(t, h.V, eventVersion)
		assert.Eq(t, h.Seq, n)
		if (h.ParentID == nil) != (n == 1) {
			t.Fatalf("line %d: parent_id must be null only on the first event", n)
		}
		seen[h.Type] = true
	}
	assert.NoErr(t, sc.Err())
	for _, typ := range eventTypes(t) {
		if !seen[typ] {
			t.Errorf("fixture has no %s event", typ)
		}
	}
}

func TestEventLogFixture_Replays(t *testing.T) {
	path, err := filepath.Abs(eventLogFixture)
	assert.NoErr(t, err)
	st, err := Replay(path)
	assert.NoErr(t, err)
	assert.Eq(t, len(st.Notices), 0)
	assert.Eq(t, st.Model, "deepseek/deepseek-chat") // model_changed, then session_resumed
	assert.Eq(t, st.Title, "create a.txt")           // session_titled beats session_started.title
	assert.Eq(t, st.Profile, "work")
	assert.Eq(t, st.Root, "/work/demo")
	if len(st.Messages) == 0 || !strings.Contains(textOf(st.Messages[0]), "Created a.txt; tests pass.") {
		t.Fatalf("history does not start with the compaction summary: %+v", st.Messages)
	}
	assert.Eq(t, len(st.Interrupted), 0)
	assert.Eq(t, len(st.PendingShell), 0) // the ! command was carried by the next user_message
}

// session_started and session_resumed may carry an optional memory object
// (docs/event-log.md); the fixture shows both.
func TestEventLogFixture_MemoryField(t *testing.T) {
	f, err := os.Open(eventLogFixture)
	assert.NoErr(t, err)
	defer f.Close()
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e struct {
			Type   string                     `json:"type"`
			Memory map[string]json.RawMessage `json:"memory"`
		}
		assert.NoErr(t, json.Unmarshal(sc.Bytes(), &e))
		if e.Type != EvSessionStarted && e.Type != EvSessionResumed {
			continue
		}
		seen[e.Type] = true
		keys := make([]string, 0, len(e.Memory))
		for k := range e.Memory {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		assert.Eq(t, strings.Join(keys, " "), "index_bytes profile_dir project_dir truncated")
	}
	assert.NoErr(t, sc.Err())
	assert.Eq(t, len(seen), 2)
}

// session_started may carry an optional dream object (docs/event-log.md);
// the fixture shows it on the one session that has it.
func TestEventLogFixture_DreamField(t *testing.T) {
	f, err := os.Open(eventLogFixture)
	assert.NoErr(t, err)
	defer f.Close()
	found := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e struct {
			Type  string `json:"type"`
			Dream *struct {
				ID             string `json:"id"`
				WindowSessions int    `json:"window_sessions"`
			} `json:"dream"`
		}
		assert.NoErr(t, json.Unmarshal(sc.Bytes(), &e))
		if e.Type != EvSessionStarted || e.Dream == nil {
			continue
		}
		found++
		assert.Eq(t, e.Dream.ID != "", true)
		assert.Eq(t, e.Dream.WindowSessions, 3)
	}
	assert.NoErr(t, sc.Err())
	assert.Eq(t, found, 1)
}
