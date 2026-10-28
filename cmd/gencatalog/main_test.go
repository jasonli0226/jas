package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestRender_FiltersAndSorts(t *testing.T) {
	body := `{"data":[{"id":"b/zed","context_length":10},{"id":"a/zero","context_length":0},
	{"id":"a/neg","context_length":-5},{"id":"a/one","context_length":7},{"id":"","context_length":9}]}`
	got, err := render([]byte(body))
	assert.NoErr(t, err)
	assert.Eq(t, string(got), "{\n  \"a/one\": 7,\n  \"b/zed\": 10\n}\n")
	var m map[string]int
	assert.NoErr(t, json.Unmarshal(got, &m))
}

func TestRender_Errors(t *testing.T) {
	_, err := render([]byte(`{"data":[{"id":"x","context_length":0}]}`))
	if err == nil {
		t.Fatal("expected error for empty result")
	}
	_, err = render([]byte(`not json`))
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v", err)
	}
}
