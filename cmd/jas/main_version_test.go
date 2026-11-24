package main

import (
	"runtime/debug"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestBuildVersion(t *testing.T) {
	stamped := func(v string) *debug.BuildInfo { return &debug.BuildInfo{Main: debug.Module{Version: v}} }
	for _, tc := range []struct {
		name, v string
		info    *debug.BuildInfo
		want    string
	}{
		{"ldflags wins", "v1.3.0", stamped("v1.2.1-0.20261124154813-40e14e5a2f8c"), "v1.3.0"},
		{"go install tag", "dev", stamped("v1.2.0"), "v1.2.0"},
		{"checkout pseudo-version", "dev", stamped("v1.2.1-0.20261124154813-40e14e5a2f8c+dirty"), "v1.2.1-0.20261124154813-40e14e5a2f8c+dirty"},
		{"devel stays dev", "dev", stamped("(devel)"), "dev"},
		{"empty stays dev", "dev", stamped(""), "dev"},
		{"no build info", "dev", nil, "dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Eq(t, buildVersion(tc.v, tc.info), tc.want)
		})
	}
}
