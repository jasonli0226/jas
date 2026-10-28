package settings

import (
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestContextWindow_Lookup(t *testing.T) {
	old := catalog
	t.Cleanup(func() { catalog = old })
	catalog = map[string]int{"openai/gpt-x": 200000, "plain": 32000}

	w, known := ContextWindow("openai/gpt-x", map[string]int{"openai/gpt-x": 1234})
	assert.Eq(t, w, 1234)
	assert.Eq(t, known, true)

	w, known = ContextWindow("openai/gpt-x", nil)
	assert.Eq(t, w, 200000)
	assert.Eq(t, known, true)

	w, known = ContextWindow("anthropic/plain", nil) // prefix stripped
	assert.Eq(t, w, 32000)
	assert.Eq(t, known, true)

	w, known = ContextWindow("nope", nil)
	assert.Eq(t, w, 128000)
	assert.Eq(t, known, false)
}

func TestEmbeddedCatalogLoads(t *testing.T) {
	if len(catalog) == 0 {
		t.Fatal("embedded catalog is empty")
	}
}
