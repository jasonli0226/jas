package farm

import "testing"

func TestMatchChoice(t *testing.T) {
	final := []string{ChoiceMerge, ChoiceKeep, ChoiceDiscard}
	failure := []string{choiceRetry, choiceDrop, choiceAbort}
	cases := []struct {
		ans     string
		choices []string
		want    string
		ok      bool
	}{
		{"d", final, ChoiceDiscard, true},
		{"discard", final, ChoiceDiscard, true},
		{"  DISCARD ", final, ChoiceDiscard, true},
		{"m", final, ChoiceMerge, true},
		{"keep", final, ChoiceKeep, true},
		{"dr", failure, choiceDrop, true},
		{"A", failure, choiceAbort, true},
		// Text that merely starts with a choice's letter names nothing.
		{"don't", final, "", false},
		{"dunno", final, "", false},
		{"delete", final, "", false},
		{"discarded", final, "", false},
		{"keep it", final, "", false},
		{"", final, "", false},
		{"maybe", []string{"m", "k", "d"}, "", false},
	}
	for _, c := range cases {
		got, ok := MatchChoice(c.ans, c.choices)
		if got != c.want || ok != c.ok {
			t.Errorf("MatchChoice(%q, %v) = %q, %v; want %q, %v", c.ans, c.choices, got, ok, c.want, c.ok)
		}
	}
}
