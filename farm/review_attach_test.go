package farm

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAttachServer_ReviewEndedDropsLeftoverSteers(t *testing.T) {
	s := newAttachServer(nil)
	s.steers = []attachSteer{{part: reviewPart}, {part: "a"}}
	s.FarmEvent(EvReviewEnded, map[string]any{"round": 1, "status": "done"})
	if len(s.steers) != 1 || s.steers[0].part != "a" {
		t.Fatalf("steers %+v", s.steers)
	}
}

func TestPartLogs_ReviewRoundsInOrder(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"round2.jsonl", "round1.jsonl"} {
		appendTo(t, filepath.Join(dir, reviewPart, n), "{}\n")
	}
	var got []string
	for _, l := range partLogs(dir) {
		got = append(got, l.part+"/"+filepath.Base(l.path))
	}
	if strings.Join(got, ",") != "review/round1.jsonl,review/round2.jsonl" {
		t.Fatalf("logs %v", got)
	}
}
