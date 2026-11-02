package settings

import (
	"fmt"
	"strconv"
	"strings"
)

// Presets maps a provider to its base URL.
var Presets = map[string]string{
	ProviderOpenRouter: "https://openrouter.ai/api/v1",
	ProviderOllama:     "http://localhost:11434/v1",
}

// Run is the typed view of Effective that `jas run` consumes.
type Run struct {
	Provider        string // never empty: "custom" when unset
	Model, BaseURL  string
	Approve         string
	MaxTurns        int
	MaxToolOutput   int
	ElideAt         float64
	KeepToolResults int
	RedactSkipPaths []string
	CompactReserve  int
	CompactKeep     int
	CompactModel    string // "" = the session's model
	MaxOutputTokens int    // sent as max_tokens on every model request
}

// Build validates e and returns typed settings. Missing model or base_url
// yields an error naming the setting and how to set it.
func (e Effective) Build() (Run, error) {
	if err := e.Validate(); err != nil {
		return Run{}, err
	}
	r := Run{
		Provider: e["provider"].Raw, Model: e["model"].Raw, BaseURL: e["base_url"].Raw,
		Approve: e["approve"].Raw, CompactModel: e["compact_model"].Raw,
	}
	if r.Provider == "" {
		r.Provider = ProviderCustom
	}
	if r.BaseURL == "" {
		r.BaseURL = Presets[r.Provider]
	}
	if r.Model == "" {
		return r, fmt.Errorf("missing model: run `jas config set model z-ai/glm-5.3`, set JAS_MODEL, or pass --model")
	}
	if r.BaseURL == "" {
		return r, fmt.Errorf("missing base_url: run `jas config set base_url https://host/v1`, set JAS_BASE_URL, pass --base-url, or choose a provider with `jas config set provider openrouter`")
	}
	r.MaxTurns, _ = strconv.Atoi(e["max_turns"].Raw)
	r.MaxToolOutput, _ = strconv.Atoi(e["max_tool_output"].Raw)
	r.KeepToolResults, _ = strconv.Atoi(e["keep_tool_results"].Raw)
	r.ElideAt, _ = strconv.ParseFloat(e["elide_at"].Raw, 64)
	r.CompactReserve, _ = strconv.Atoi(e["compact_reserve_tokens"].Raw)
	r.CompactKeep, _ = strconv.Atoi(e["compact_keep_tokens"].Raw)
	r.MaxOutputTokens, _ = strconv.Atoi(e["max_output_tokens"].Raw)
	for _, p := range strings.Split(e["redact_skip_paths"].Raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			r.RedactSkipPaths = append(r.RedactSkipPaths, p)
		}
	}
	return r, nil
}
