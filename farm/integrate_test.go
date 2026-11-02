package farm

import (
	"slices"
	"testing"
)

func TestLeafParts_SkipsDoneDependenciesKeepsPlanOrder(t *testing.T) {
	plan := Plan{Parts: []Part{
		coder("c", "c/**"), coder("a", "a/**"), coder("b", "b/**", "a"), coder("d", "d/**", "c"),
	}}
	// d was dropped, so c has no done dependent and is a leaf itself.
	got := leafParts(plan, []string{"a", "b", "c"})
	if want := []string{"c", "b"}; !slices.Equal(got, want) {
		t.Fatalf("leaves %v, want %v", got, want)
	}
}

func TestLeafParts_DependencyIsNotALeaf(t *testing.T) {
	// x depends on a, so x is a leaf and a is not (x's branch contains it).
	plan := Plan{Parts: []Part{coder("x", "x/**", "a"), coder("b", "b/**"), coder("a", "a/**")}}
	got := leafParts(plan, []string{"x", "b", "a"})
	if want := []string{"x", "b"}; !slices.Equal(got, want) {
		t.Fatalf("leaves %v, want %v", got, want)
	}
}
