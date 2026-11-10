package main

import (
	"os"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/settings"
)

// /settings --in-profile writes the chat's active profile's config.json and
// the effective value follows; it needs no project trust.
func TestChatBackend_SettingsInProfile(t *testing.T) {
	e, _, _ := profileChatEnv(t) // untrusted project, global approve=never
	b := &chatBackend{e: e}
	_, err := b.SwitchProfile(quietSession(t, e), "ds", nil)
	assert.NoErr(t, err)

	assert.NoErr(t, b.SetSetting("profile", "approve", "shell"))
	raw, err := os.ReadFile(settings.ProfileConfigPath(e.home, "ds"))
	assert.NoErr(t, err)
	assert.Contains(t, string(raw), `"approve": "shell"`)
	if _, err := os.Stat(settings.ProfileConfigPath(e.home, "default")); !os.IsNotExist(err) {
		t.Fatalf("write reached the default profile: %v", err)
	}
	assert.Eq(t, e.cfg.Approve, "shell")
	line, err := b.GetSetting("approve")
	assert.NoErr(t, err)
	assert.Eq(t, line.Value, "shell")
	assert.Eq(t, line.Source, string(settings.ProfileSource("ds")))

	assert.NoErr(t, b.SetSetting("profile", "max_turns", "7")) // a deferred key lands too
	assert.Eq(t, e.cfg.MaxTurns, 7)

	assert.NoErr(t, b.UnsetSetting("profile", "approve"))
	assert.Eq(t, e.cfg.Approve, "never")
	raw, err = os.ReadFile(settings.ProfileConfigPath(e.home, "ds"))
	assert.NoErr(t, err)
	assert.NotContains(t, string(raw), "approve")

	// A /reload keeps the written layer.
	_, _, err = e.reloadContext(nil)
	assert.NoErr(t, err)
	assert.NoErr(t, b.reconfigure())
	assert.Eq(t, e.cfg.MaxTurns, 7)
}

func TestChatBackend_SettingsInProfileRefusesForbiddenKeys(t *testing.T) {
	e, _, _ := profileChatEnv(t)
	b := &chatBackend{e: e}
	for _, key := range []string{"base_url", "provider", "search_base_url", "profile", "providers.vllm.base_url"} {
		err := b.SetSetting("profile", key, "http://x/v1")
		if err == nil {
			t.Fatalf("%s written to a profile", key)
		}
		assert.Contains(t, err.Error(), "cannot be set in a profile")
	}
	if _, err := os.Stat(settings.ProfileConfigPath(e.home, "default")); !os.IsNotExist(err) {
		t.Fatalf("refused write created the profile file: %v", err)
	}
}
