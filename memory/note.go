package memory

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const (
	frontmatterFence = "---"
	maxDescription   = 200
	noteErrPrefix    = "memory note needs frontmatter: "
)

// Types are the allowed note types.
var Types = []string{"user", "feedback", "project", "reference"}

var kebabCase = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Note is the frontmatter of a memory note. Unknown keys are ignored.
type Note struct {
	Name, Description, Type string
}

// ParseNote reads the leading --- ... --- YAML block of content.
func ParseNote(content string) (Note, error) {
	block, ok := frontmatterBlock(content)
	if !ok {
		return Note{}, errors.New(noteErrPrefix + "the note must start with a --- block closed by ---")
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(block), &m); err != nil {
		return Note{}, fmt.Errorf("%sinvalid YAML: %w", noteErrPrefix, err)
	}
	var n Note
	for key, dst := range map[string]*string{"name": &n.Name, "description": &n.Description, "type": &n.Type} {
		if v, present := m[key]; present {
			s, isStr := v.(string)
			if !isStr {
				return Note{}, fmt.Errorf("%s%s must be a string", noteErrPrefix, key)
			}
			*dst = s
		}
	}
	return n, nil
}

// frontmatterBlock returns the text between the opening and closing fence.
func frontmatterBlock(content string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != frontmatterFence {
		return "", false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == frontmatterFence {
			return strings.Join(lines[1:i], "\n"), true
		}
	}
	return "", false
}

// ValidateNote checks content against the note format. fileName is the base
// name (x.md).
func ValidateNote(fileName, content string) error {
	n, err := ParseNote(content)
	if err != nil {
		return err
	}
	switch {
	case !kebabCase.MatchString(n.Name):
		return fmt.Errorf("%sname must be kebab-case (lowercase letters, digits, hyphens)", noteErrPrefix)
	case n.Name != strings.TrimSuffix(fileName, ".md"):
		return fmt.Errorf("%sname %q must match the file name %q without .md", noteErrPrefix, n.Name, fileName)
	case n.Description == "" || strings.ContainsAny(n.Description, "\r\n"):
		return fmt.Errorf("%sdescription must be a single non-empty line", noteErrPrefix)
	case utf8.RuneCountInString(n.Description) > maxDescription:
		return fmt.Errorf("%sdescription must be at most %d characters", noteErrPrefix, maxDescription)
	case !slices.Contains(Types, n.Type):
		return fmt.Errorf("%stype must be one of %s", noteErrPrefix, strings.Join(Types, ", "))
	}
	return nil
}
