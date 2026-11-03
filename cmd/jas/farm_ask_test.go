package main

import (
	"bufio"
	"context"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func askWith(input string, choices []string) (string, bool, string) {
	var out strings.Builder
	ask := farmAsk(context.Background(), bufio.NewReader(strings.NewReader(input)), &out)
	ans, ok := ask("retry, drop or abort? ", choices)
	return ans, ok, out.String()
}

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
	ans, _, _ = askWith("keep\n", []string{"m", "k", "d"})
	assert.Eq(t, ans, "k")
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
