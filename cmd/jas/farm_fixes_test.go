package main

import (
	"strings"
	"testing"

	"github.com/jasonli0226/jas/farm"
	"github.com/jasonli0226/jas/internal/assert"
)

func TestPrintReviewStatus_EmptyStatusRunning(t *testing.T) {
	var b strings.Builder
	printReviewStatus(&b, &farm.ReviewState{})
	assert.Contains(t, b.String(), "review: running  open 0")
}

func TestFarmUsage_ListsReview(t *testing.T) {
	assert.Contains(t, farmUsage, "--review")
}
