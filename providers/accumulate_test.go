package providers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonli0226/jas/internal/assert"
)

// parseChunk converts one OpenAI chat-completions chunk into StreamEvents.
func parseChunk(t testing.TB, payload []byte) []StreamEvent {
	t.Helper()
	var c struct {
		Choices []struct {
			Delta struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			Details          struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	assert.NoErr(t, json.Unmarshal(payload, &c))
	var evs []StreamEvent
	for _, ch := range c.Choices {
		if ch.Delta.Content != "" {
			evs = append(evs, StreamEvent{Text: ch.Delta.Content})
		}
		for _, tc := range ch.Delta.ToolCalls {
			evs = append(evs, StreamEvent{ToolCallDelta: &ToolCallDelta{
				Index: tc.Index, ID: tc.ID, Name: tc.Function.Name, ArgsFragment: tc.Function.Arguments,
			}})
		}
		if ch.FinishReason != "" {
			evs = append(evs, StreamEvent{FinishReason: ch.FinishReason})
		}
	}
	if c.Usage != nil {
		evs = append(evs, StreamEvent{Usage: &Usage{
			PromptTokens: c.Usage.PromptTokens, CompletionTokens: c.Usage.CompletionTokens,
			CachedTokens: c.Usage.Details.CachedTokens,
		}})
	}
	return evs
}

func fixtureEvents(t testing.TB, name string) []StreamEvent {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	assert.NoErr(t, err)
	defer f.Close()
	out, errc := ReadSSE(context.Background(), f, 5*time.Second)
	var evs []StreamEvent
	for p := range out {
		evs = append(evs, parseChunk(t, p)...)
	}
	assert.NoErr(t, <-errc)
	return evs
}

func accumulate(evs []StreamEvent) (Message, []ContentBlock, error) {
	var a Accumulator
	for _, ev := range evs {
		a.Add(ev)
	}
	return a.Done()
}

func accumulateFixture(t testing.TB, name string) (Message, []ContentBlock, error) {
	t.Helper()
	return accumulate(fixtureEvents(t, name))
}

func TestAccumulator_SplitArgs(t *testing.T) {
	msg, calls, err := accumulateFixture(t, "split_args.sse")
	assert.NoErr(t, err)
	assert.Eq(t, len(calls), 1)
	assert.Eq(t, calls[0].Name, "read")
	assert.Eq(t, calls[0].ToolUseID, "call_abc")
	assert.JSONEq(t, string(calls[0].Input), `{"path":"main.go"}`)
	assert.Eq(t, len(msg.Content), 1) // tool_use only, no empty text block
	assert.Eq(t, msg.Role, RoleAssistant)
}

func TestAccumulator_WholeCallOneChunk(t *testing.T) {
	_, calls, err := accumulateFixture(t, "whole_call_one_chunk.sse")
	assert.NoErr(t, err)
	assert.Eq(t, len(calls), 1)
	assert.Eq(t, calls[0].Name, "shell")
	assert.JSONEq(t, string(calls[0].Input), `{"command":"ls -la"}`)
}

func TestAccumulator_TwoParallelCalls(t *testing.T) {
	msg, calls, err := accumulateFixture(t, "two_parallel_calls.sse")
	assert.NoErr(t, err)
	assert.Eq(t, len(calls), 2)
	assert.Eq(t, len(msg.Content), 2)
	assert.Eq(t, calls[0].Name, "read")
	assert.JSONEq(t, string(calls[0].Input), `{"path":"a.go"}`)
	assert.Eq(t, calls[1].Name, "grep")
	assert.JSONEq(t, string(calls[1].Input), `{"pattern":"TODO"}`)
}

func TestAccumulator_TextOnly(t *testing.T) {
	msg, calls, err := accumulateFixture(t, "text_only.sse")
	assert.NoErr(t, err)
	assert.Eq(t, len(calls), 0)
	assert.Eq(t, len(msg.Content), 1)
	assert.Eq(t, msg.Content[0].Type, BlockText)
	assert.Eq(t, msg.Content[0].Text, "Hello, world.")
}

func TestAccumulator_MissingNameErrors(t *testing.T) {
	var a Accumulator
	a.Add(StreamEvent{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "x", ArgsFragment: "{}"}})
	_, _, err := a.Done()
	if err == nil {
		t.Fatal("expected error for call with no name")
	}
}

func TestAccumulator_EmptyArgsBecomeEmptyObject(t *testing.T) {
	var a Accumulator
	a.Add(StreamEvent{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "x", Name: "ls"}})
	_, calls, err := a.Done()
	assert.NoErr(t, err)
	assert.JSONEq(t, string(calls[0].Input), `{}`)
}

var fuzzFixtures = []string{
	"split_args.sse", "two_parallel_calls.sse", "text_only.sse", "whole_call_one_chunk.sse",
}

// rechunk joins each index's arg fragments and re-splits them at the given
// cut offsets, emitting the resulting fragments in order (ID/name stay on the
// first fragment of each index).
func rechunk(evs []StreamEvent, cuts []byte) []StreamEvent {
	type agg struct {
		id, name, args string
	}
	order := []int{}
	byIdx := map[int]*agg{}
	var out []StreamEvent
	for _, ev := range evs {
		d := ev.ToolCallDelta
		if d == nil {
			out = append(out, ev)
			continue
		}
		g := byIdx[d.Index]
		if g == nil {
			g = &agg{}
			byIdx[d.Index] = g
			order = append(order, d.Index)
		}
		if g.id == "" {
			g.id = d.ID
		}
		if g.name == "" {
			g.name = d.Name
		}
		g.args += d.ArgsFragment
	}
	for _, i := range order {
		g := byIdx[i]
		start := 0
		first := true
		emit := func(frag string) {
			d := &ToolCallDelta{Index: i, ArgsFragment: frag}
			if first {
				d.ID, d.Name = g.id, g.name
				first = false
			}
			out = append(out, StreamEvent{ToolCallDelta: d})
		}
		for _, c := range cuts {
			if len(g.args) == 0 {
				break
			}
			end := int(c) % (len(g.args) + 1)
			if end <= start {
				continue
			}
			emit(g.args[start:end])
			start = end
		}
		emit(g.args[start:])
	}
	return out
}

func FuzzAccumulator_Rechunk(f *testing.F) {
	for i := range fuzzFixtures {
		f.Add(uint8(i), []byte{3, 7, 1, 20})
	}
	f.Fuzz(func(t *testing.T, which uint8, cuts []byte) {
		evs := fixtureEvents(t, fuzzFixtures[int(which)%len(fuzzFixtures)])
		wantMsg, wantCalls, wantErr := accumulate(evs)
		gotMsg, gotCalls, gotErr := accumulate(rechunk(evs, cuts))
		assert.NoErr(t, wantErr)
		assert.NoErr(t, gotErr)
		assert.Eq(t, len(gotCalls), len(wantCalls))
		for i := range wantCalls {
			assert.Eq(t, gotCalls[i].ToolUseID, wantCalls[i].ToolUseID)
			assert.Eq(t, gotCalls[i].Name, wantCalls[i].Name)
			assert.Eq(t, string(gotCalls[i].Input), string(wantCalls[i].Input))
		}
		assert.Eq(t, len(gotMsg.Content), len(wantMsg.Content))
	})
}
