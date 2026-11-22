package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/farm"
)

func TestPrintFarmStatus_FinalLine(t *testing.T) {
	for _, c := range []struct {
		name string
		m    farm.Manifest
		want string
	}{
		{"running", farm.Manifest{TotalTokens: 42}, "final: none yet  total tokens 42\n"},
		{"ended", farm.Manifest{FinalChoice: "keep", Reason: "ok", TotalTokens: 7}, "final: keep (ok) exit 0  total tokens 7\n"},
		{"ended without choice", farm.Manifest{Reason: "interrupted", ExitCode: 130}, "final: - (interrupted) exit 130  total tokens 0\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var b bytes.Buffer
			printFarmStatus(&b, c.m)
			if !strings.HasSuffix(b.String(), c.want) {
				t.Fatalf("got %q, want suffix %q", b.String(), c.want)
			}
		})
	}
}
