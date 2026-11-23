#!/usr/bin/env bash
# link-claude-code_test.sh runs link-claude-code.sh in throwaway repos: it
# links the skill, removes the old merge hook but keeps other settings and
# hooks, changes nothing on a second run, refuses a destination that is not its symlink, and fails
# cleanly when ln copies instead of linking.
#
# Usage: bash .agents/skills/pr-review-loop/scripts/link-claude-code_test.sh
set -uo pipefail

skill_dir=$(cd "$(dirname "$0")/.." && pwd)
fails=0
fail() {
	echo "FAIL: $1"
	fails=$((fails + 1))
}

# new_repo makes a git repo holding a copy of this skill and prints its path.
new_repo() {
	local repo
	repo=$(mktemp -d)
	git -C "$repo" init -q
	mkdir -p "$repo/.agents/skills"
	cp -R "$skill_dir" "$repo/.agents/skills/pr-review-loop"
	printf '%s\n' "$repo"
}

link() { (cd "$1" && bash .agents/skills/pr-review-loop/scripts/link-claude-code.sh >/dev/null 2>&1); }
# link_with_copying_ln runs the script with an ln that copies, as Git Bash
# does without symlinks enabled, and prints its stderr.
link_with_copying_ln() {
	local bin
	bin=$(mktemp -d)
	cat >"$bin/ln" <<'LN'
#!/usr/bin/env bash
# Fake ln -sfn target dest: copy target (relative to dest's directory).
target=${@: -2:1} dest=${@: -1}
cp -R "$(dirname "$dest")/$target" "$dest"
LN
	chmod +x "$bin/ln"
	(cd "$1" && PATH="$bin:$PATH" bash .agents/skills/pr-review-loop/scripts/link-claude-code.sh 2>&1 >/dev/null)
	local code=$?
	rm -rf "$bin"
	return $code
}
old_hook='"$CLAUDE_PROJECT_DIR"/.agents/skills/pr-review-loop/scripts/check-pr-reviewed.sh'

# Links the skill and leaves settings without the old hook as they are; a
# second run changes nothing.
repo=$(new_repo)
mkdir -p "$repo/.claude"
echo '{"model":"opus"}' >"$repo/.claude/settings.local.json"
link "$repo" || fail "first run exited non-zero"
link "$repo" || fail "second run exited non-zero"
dest=$repo/.claude/skills/pr-review-loop
[[ -L $dest && $(readlink "$dest") == ../../.agents/skills/pr-review-loop ]] || fail "skill is not linked to the .agents copy"
[[ -f $dest/SKILL.md ]] || fail "SKILL.md is not reachable through the link"
[[ $(jq -c . "$repo/.claude/settings.local.json") == '{"model":"opus"}' ]] || fail "settings changed: $(cat "$repo/.claude/settings.local.json")"
rm -rf "$repo"

# No settings file: none is created.
repo=$(new_repo)
link "$repo" || fail "run without settings exited non-zero"
[[ ! -e $repo/.claude/settings.local.json ]] || fail "a settings file was created"
rm -rf "$repo"

# The old merge hook is removed; other settings and hooks stay.
repo=$(new_repo)
mkdir -p "$repo/.claude"
jq -n --arg old "$old_hook" '{model: "opus", hooks: {PreToolUse: [
	{matcher: "Bash", hooks: [{type: "command", command: $old, timeout: 30}]},
	{matcher: "Bash", hooks: [{type: "command", command: "mine.sh"}]}]}}' >"$repo/.claude/settings.local.json"
link "$repo" || fail "run with the old hook exited non-zero"
got=$(jq -c '[.hooks.PreToolUse[].hooks[].command]' "$repo/.claude/settings.local.json")
[[ $got == '["mine.sh"]' ]] || fail "want only mine.sh left, got $got"
[[ $(jq -r .model "$repo/.claude/settings.local.json") == opus ]] || fail "existing settings were dropped"
rm -rf "$repo"

# The old hook alone: the empty hooks object goes too.
repo=$(new_repo)
mkdir -p "$repo/.claude"
jq -n --arg old "$old_hook" '{hooks: {PreToolUse: [
	{matcher: "Bash", hooks: [{type: "command", command: $old, timeout: 30}]}]}}' >"$repo/.claude/settings.local.json"
link "$repo" || fail "run with only the old hook exited non-zero"
[[ $(jq -c . "$repo/.claude/settings.local.json") == '{}' ]] || fail "want {}, got $(cat "$repo/.claude/settings.local.json")"
rm -rf "$repo"

# A real directory where the link belongs (an old checkout's copy) is
# refused, not linked into.
repo=$(new_repo)
mkdir -p "$repo/.claude/skills/pr-review-loop"
echo stale >"$repo/.claude/skills/pr-review-loop/SKILL.md"
if link "$repo"; then
	fail "a real directory at the destination was accepted"
fi
[[ ! -e $repo/.claude/skills/pr-review-loop/pr-review-loop ]] || fail "linked inside the existing directory"
[[ $(cat "$repo/.claude/skills/pr-review-loop/SKILL.md") == stale ]] || fail "the existing directory was changed"
rm -rf "$repo"

# An ln that copies is reported, and the copy is removed.
repo=$(new_repo)
if out=$(link_with_copying_ln "$repo"); then
	fail "a copy instead of a link was accepted"
fi
[[ $out == *"made a copy"* ]] || fail "no copy message, got: $out"
[[ ! -e $repo/.claude/skills/pr-review-loop ]] || fail "the copy was left behind"
rm -rf "$repo"

if ((fails > 0)); then
	echo "$fails failed"
	exit 1
fi
echo ok
