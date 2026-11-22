package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jasonli0226/jas/internal/assert"
)

// askWith answers the farm question with input's lines, each typed once
// the prompt is on screen (a line typed while no question waits is
// ignored), then ends the input.
func askWith(input string, choices []string) (string, bool, string) {
	const prompt = "retry, drop or abort? "
	pr, pw := io.Pipe()
	out := &lockedBuf{}
	type res struct {
		ans string
		ok  bool
	}
	done := make(chan res, 1)
	go func() {
		ans, ok := farmAsk(context.Background(), newFarmTTY(pr, out))(prompt, choices)
		done <- res{ans, ok}
	}()
	deadline := time.After(5 * time.Second)
	n := 0
	for _, line := range strings.SplitAfter(input, "\n") {
		if line == "" {
			continue
		}
		n++
		for strings.Count(out.String(), prompt) < n {
			select {
			case r := <-done:
				return r.ans, r.ok, out.String()
			case <-deadline:
				panic("farm question never asked again: " + out.String())
			case <-time.After(2 * time.Millisecond):
			}
		}
		_, _ = io.WriteString(pw, line)
	}
	pw.Close()
	select {
	case r := <-done:
		return r.ans, r.ok, out.String()
	case <-deadline:
		panic("farm question never answered: " + out.String())
	}
}

var finalChoices = []string{"merge", "keep", "discard"}

func TestFarmAsk_ReasksOnUnknownAnswer(t *testing.T) {
	ans, ok, out := askWith("end\nabort\n", []string{"retry", "drop", "abort"})
	assert.Eq(t, ans, "abort")
	assert.Eq(t, ok, true)
	assert.Eq(t, strings.Count(out, "retry, drop or abort? "), 2)
	assert.Eq(t, strings.Contains(out, "answer one of: retry, drop, abort"), true)
}

func TestFarmAsk_AcceptsUniquePrefixAndWord(t *testing.T) {
	ans, _, _ := askWith("A\n", []string{"retry", "drop", "abort"})
	assert.Eq(t, ans, "abort")
	ans, _, _ = askWith("keep\n", finalChoices)
	assert.Eq(t, ans, "keep")
	ans, _, _ = askWith("d\n", finalChoices)
	assert.Eq(t, ans, "discard")
}

func TestFarmAsk_TextStartingWithAChoiceIsAskedAgain(t *testing.T) {
	ans, ok, out := askWith("don't\ndunno\nk\n", []string{"m", "k", "d"})
	assert.Eq(t, ans, "k")
	assert.Eq(t, ok, true)
	assert.Eq(t, strings.Count(out, "answer one of: m, k, d"), 2)
}

func TestFarmAsk_EOFIsNoAnswer(t *testing.T) {
	_, ok, _ := askWith("bogus\n", []string{"retry", "drop", "abort"})
	assert.Eq(t, ok, false)
}

func TestFarmAsk_FreeTextWithoutChoices(t *testing.T) {
	ans, ok, _ := askWith("  merge anyway \n", nil)
	assert.Eq(t, ans, "merge anyway")
	assert.Eq(t, ok, true)
}

func TestFarmAsk_EmptyLinePassesThrough(t *testing.T) {
	ans, ok, _ := askWith("\n", []string{"m", "k", "d"})
	assert.Eq(t, ans, "")
	assert.Eq(t, ok, true)
}
