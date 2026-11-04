package tools

import "github.com/jasonli0226/jas/core"

// Web returns the optional network tools enabled by web_tools=true:
// web_fetch and web_search. searchBaseURL is an optional SearXNG root;
// when empty, web_search falls back to DuckDuckGo lite.
func Web(searchBaseURL string) []core.Tool {
	return []core.Tool{
		WebFetch{},
		WebSearch{BaseURL: searchBaseURL},
	}
}

// SessionTools is the toolset for chat and jas run: read/mutating tools,
// plus web tools when web is true.
func SessionTools(web bool, searchBaseURL string) []core.Tool {
	out := append(ReadOnly(), Mutating()...)
	if web {
		out = append(out, Web(searchBaseURL)...)
	}
	return out
}
