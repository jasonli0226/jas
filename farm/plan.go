package farm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Interface is a structured contract the integrator checks.
type Interface struct {
	Name  string `yaml:"name"`
	Owner string `yaml:"owner"`
	Shape string `yaml:"shape"`
}

// Part is one unit of parallel work.
type Part struct {
	ID    string   `yaml:"id"`
	Role  string   `yaml:"role"`
	Goal  string   `yaml:"goal"`
	Owns  []string `yaml:"owns,omitempty"`
	After []string `yaml:"after,omitempty"`
}

// Approval records owner approval of a canonical plan hash.
type Approval struct {
	SHA256 string    `yaml:"sha256"`
	By     string    `yaml:"by"`
	At     time.Time `yaml:"at"`
}

// Refusal is a structured planner (or code) decision not to split.
type Refusal struct {
	Reason  string `yaml:"reason"`
	Detail  string `yaml:"detail"`
	Suggest string `yaml:"-"`
}

// Plan is the farm plan document.
type Plan struct {
	Task          string             `yaml:"task"`
	Base          string             `yaml:"base,omitempty"`
	Contracts     string             `yaml:"contracts,omitempty"`
	Interfaces    []Interface        `yaml:"interfaces,omitempty"`
	Parts         []Part             `yaml:"parts,omitempty"`
	Shared        []string           `yaml:"shared,omitempty"`
	WhySplit      string             `yaml:"why_split,omitempty"`
	ProposedRoles map[string]RoleDef `yaml:"proposed_roles,omitempty"`
	Approved      *Approval          `yaml:"approved,omitempty"`
	Refuse        *Refusal           `yaml:"refuse,omitempty"`
	Suggest       string             `yaml:"suggest,omitempty"`
}

// RefuseReasons are the allowed refusal reasons.
var RefuseReasons = []string{"small_change", "single_package", "overlapping_ownership", "serial_dependencies", "no_check", "unclear_task", "no_clear_contract"}

// ParsePlan decodes a plan, rejecting unknown keys and unknown refusal reasons.
func ParsePlan(b []byte) (Plan, error) {
	var p Plan
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return Plan{}, fmt.Errorf("parse plan: %w", err)
	}
	if p.Refuse != nil {
		if !validReason(p.Refuse.Reason) {
			return Plan{}, fmt.Errorf("refuse.reason %q: want one of %s", p.Refuse.Reason, strings.Join(RefuseReasons, ", "))
		}
		p.Refuse.Suggest = p.Suggest
	}
	return p, nil
}

func validReason(r string) bool {
	for _, v := range RefuseReasons {
		if v == r {
			return true
		}
	}
	return false
}

// stripApproved normalizes line endings to LF and removes the top-level
// approved: key with its indented block, working on raw lines.
func stripApproved(b []byte) string {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	var out []string
	skipping := false
	for _, line := range strings.Split(s, "\n") {
		if line == "approved:" {
			skipping = true
			continue
		}
		if skipping && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			continue
		}
		skipping = false
		out = append(out, line)
	}
	return strings.TrimRight(strings.Join(out, "\n"), " \t\r\n")
}

// CanonicalHash is the sha256 (hex) of the plan bytes without the approved
// block, with LF line endings and no trailing whitespace. No YAML re-encoding.
func CanonicalHash(b []byte) string {
	sum := sha256.Sum256([]byte(stripApproved(b)))
	return hex.EncodeToString(sum[:])
}

// Approve returns the plan bytes with a fresh approved: block appended
// (replacing any existing one) whose sha256 is the plan's CanonicalHash.
func Approve(b []byte, by string, at time.Time) []byte {
	body := stripApproved(b)
	sum := sha256.Sum256([]byte(body))
	by = strings.NewReplacer("\n", " ", "\r", " ").Replace(by)
	block := fmt.Sprintf("approved:\n  sha256: %s\n  by: %q\n  at: %s\n", hex.EncodeToString(sum[:]), by, at.Format(time.RFC3339))
	return []byte(body + "\n" + block)
}
