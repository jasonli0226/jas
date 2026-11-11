package memory

import (
	"strings"
	"testing"
)

const specNote = "---\n" +
	"name: squash-merge-after-create\n" +
	"description: squash-merge each PR to main right after opening it; no CI wait\n" +
	"type: feedback\n" +
	"---\n\n" +
	"Squash-merge every PR to main as soon as it is created, without waiting for\n" +
	"CI or asking. Related: [[pr-titles]].\n\n" +
	"**Why:** the owner reviews before asking for the PR, and CI runs on main.\n" +
	"**How to apply:** after `gh pr create`, run `gh pr merge --squash`.\n"

func TestValidateNote(t *testing.T) {
	const typeMsg = "type must be one of user, feedback, project, reference"
	tests := []struct {
		name    string
		file    string
		content string
		wantErr string // empty means no error
	}{
		{"spec example", "squash-merge-after-create.md", specNote, ""},
		{"no frontmatter", "x.md", "just text\n", "memory note needs frontmatter"},
		{"missing type", "x.md", "---\nname: x\ndescription: d\n---\nbody\n", typeMsg},
		{"bad type", "x.md", "---\nname: x\ndescription: d\ntype: idea\n---\n", typeMsg},
		{"not kebab-case", "Foo_Bar.md", "---\nname: Foo_Bar\ndescription: d\ntype: user\n---\n", "kebab-case"},
		{"name mismatch", "y.md", "---\nname: x\ndescription: d\ntype: user\n---\n", "must match the file name"},
		{"multiline description", "x.md", "---\nname: x\ndescription: \"a\\nb\"\ntype: user\n---\n", "description"},
		{"long description", "x.md", "---\nname: x\ndescription: " + strings.Repeat("a", 201) + "\ntype: user\n---\n", "description"},
		{"extra key", "x.md", "---\nname: x\ndescription: d\ntype: user\nstatus: retired\n---\n", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateNote(tc.file, tc.content)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
			if !strings.HasPrefix(err.Error(), "memory note needs frontmatter: ") {
				t.Fatalf("error %q lacks the standard prefix", err)
			}
		})
	}
}
