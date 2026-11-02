package settings

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// SplitModelRef splits a model reference at the first "/". The part before
// it is a provider name only when it names a configured provider; a ref like
// "z-ai/glm-5.3" (OpenRouter ids carry slashes themselves) has no provider
// the caller knows, so both parts come back with ok=false.
func SplitModelRef(ref string, known func(string) bool) (provider, model string, ok bool) {
	name, rest, found := strings.Cut(ref, "/")
	if !found || !known(name) || rest == "" || name == providerReserved {
		return "", ref, false
	}
	return name, rest, true
}

// KnownProvider reports whether name is addressable: a preset, a stored
// providers.<name> entry, or the legacy custom/unset default.
func (f File) KnownProvider(name string) bool {
	if name == ProviderOpenRouter || name == ProviderOllama || name == ProviderCustom {
		return true
	}
	_, ok := f.Providers[name]
	return ok
}

// ProviderURL is the base URL for a named provider: its stored
// providers.<name>.base_url, else the preset, else "" (fall back to the
// flat base_url, then fail loudly).
func (f File) ProviderURL(name string) string {
	if p, ok := f.Providers[name]; ok && p.BaseURL != "" {
		return p.BaseURL
	}
	return Presets[name]
}

// ProviderNames lists every addressable provider name sorted: the presets,
// the legacy custom, and every stored entry.
func (f File) ProviderNames() []string {
	set := map[string]bool{ProviderOpenRouter: true, ProviderOllama: true, ProviderCustom: true}
	for n := range f.Providers {
		set[n] = true
	}
	return slices.Sorted(maps.Keys(set))
}

// ResolveModel turns a model reference into (provider, baseURL, model id).
// A qualified ref whose provider is configured overrides the flat provider
// and base_url; anything else is a bare model on the default provider
// (cfgProvider/cfgBaseURL, as resolved from the flat keys). The ref's
// provider wins over the flat base_url, which loses to the stored
// providers.<name>.base_url and the preset.
func (f File) ResolveModel(ref, cfgProvider, cfgBaseURL string) (provider, baseURL, model string, err error) {
	provider, model = cfgProvider, ref
	if p, m, ok := SplitModelRef(ref, f.KnownProvider); ok {
		provider, model = p, m
	}
	if provider == "" {
		provider = ProviderCustom
	}
	baseURL = f.ProviderURL(provider)
	if provider == cfgProvider {
		// The flat base_url (explicit or preset fallback); Build reports a
		// missing one with the friendlier message.
		if cfgBaseURL != "" {
			baseURL = cfgBaseURL
		}
	} else if baseURL == "" {
		return "", "", "", fmt.Errorf("no base_url for provider %q: run `jas config set providers.%s.base_url <url>`", provider, provider)
	}
	return provider, baseURL, model, nil
}
