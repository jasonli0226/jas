package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
)

// A write through the executor into an ancestor's skills reaches the gate
// marked with the owning profile; one into the own skills is must-ask only.
func TestExec_SharedSkillsWriteNamesOwner(t *testing.T) {
	f := newInheritFixture(t)
	for _, tc := range []struct{ dir, owner string }{
		{f.parent, "p"},
		{f.own, ""},
	} {
		var asked bool
		var owner string
		gate := gateFunc(func(tl Tool) (Decision, error) {
			asked, owner = MustAsk(tl), MustAskShared(tl)
			return Granted, nil
		})
		e := newExec(&fakeLog{}, gate, &fakeTool{name: "write", kind: Mutating})
		e.Policy = f.policy(t)
		in, _ := json.Marshal(map[string]string{"path": filepath.Join(tc.dir, "x", "SKILL.md")})
		c := call("1", "write")
		c.Input = in
		_, err := e.Run(context.Background(), []providers.ContentBlock{c})
		assert.NoErr(t, err)
		assert.Eq(t, asked, true)
		assert.Eq(t, owner, tc.owner)
	}
}
