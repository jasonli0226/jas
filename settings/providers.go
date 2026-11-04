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

// openRouterVendors is the set of vendor segments in the embedded OpenRouter
// catalog ("qwen" from "qwen/qwen3-coder"; a leading "~" alias is dropped).
var openRouterVendors = func() map[string]bool {
	set := map[string]bool{}
	for id := range catalog {
		if v, _, ok := strings.Cut(strings.TrimPrefix(id, "~"), "/"); ok {
			set[v] = true
		}
	}
	return set
}()

// VendorShadowWarning explains a model ref whose first segment is both a
// configured provider and an OpenRouter vendor while the default provider is
// OpenRouter: the ref goes to that provider, not OpenRouter, so a later
// providers.<name> entry would otherwise reroute it silently. "" when the
// ref is unambiguous.
func (f File) VendorShadowWarning(ref, defaultProvider string) string {
	if defaultProvider != ProviderOpenRouter {
		return ""
	}
	p, m, ok := SplitModelRef(ref, f.KnownProvider)
	if !ok || p == ProviderOpenRouter || !openRouterVendors[p] {
		return ""
	}
	return fmt.Sprintf("model %q goes to providers.%s (model %q), not OpenRouter, because %q is also an OpenRouter vendor; use %q to stay on OpenRouter, or rename the provider", ref, p, m, p, ProviderOpenRouter+"/"+ref)
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
