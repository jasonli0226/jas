// Package settings holds jas configuration: the model context-window catalog
// and (later) config file loading.
package settings

//go:generate go run ../cmd/gencatalog -out catalog.json

import (
	_ "embed"
	"encoding/json"
	"maps"
	"slices"
	"strings"
)

// DefaultContextWindow is used when a model is not in the catalog.
const DefaultContextWindow = 128000

//go:embed catalog.json
var catalogJSON []byte

// catalog maps model id to context length. Tests may replace it.
var catalog = loadCatalog()

func loadCatalog() map[string]int {
	m := map[string]int{}
	if err := json.Unmarshal(catalogJSON, &m); err != nil {
		panic("settings: corrupt embedded catalog.json: " + err.Error())
	}
	return m
}

// ContextWindow returns the context window for model: an override, else an
// exact catalog hit, else a hit after stripping a "provider/" prefix, else
// DefaultContextWindow with known=false.
func ContextWindow(model string, overrides map[string]int) (window int, known bool) {
	if w, ok := overrides[model]; ok && w > 0 {
		return w, true
	}
	if w, ok := catalog[model]; ok {
		return w, true
	}
	if _, rest, ok := strings.Cut(model, "/"); ok {
		if w, ok := catalog[rest]; ok {
			return w, true
		}
	}
	return DefaultContextWindow, false
}

// CatalogModels returns every model id in the catalog, sorted.
func CatalogModels() []string {
	return slices.Sorted(maps.Keys(catalog))
}
