#!/usr/bin/env bash
# link-claude-code_test.sh runs link-claude-code.sh in throwaway repos: it
# links the skill, merges the hook into existing settings, changes nothing
# on a second run, and refuses a destination that is not its symlink.
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
hooks() { jq '[.hooks.PreToolUse[].hooks[]] | length' "$1/.claude/settings.local.json"; }

# Links the skill and keeps other settings; a second run changes nothing.
repo=$(new_repo)
mkdir -p "$repo/.claude"
echo '{"model":"opus"}' >"$repo/.claude/settings.local.json"
link "$repo" || fail "first run exited non-zero"
link "$repo" || fail "second run exited non-zero"
dest=$repo/.claude/skills/pr-review-loop
[[ -L $dest && $(readlink "$dest") == ../../.agents/skills/pr-review-loop ]] || fail "skill is not linked to the .agents copy"
[[ -f $dest/SKILL.md ]] || fail "SKILL.md is not reachable through the link"
[[ $(hooks "$repo") == 1 ]] || fail "want 1 hook after two runs, got $(hooks "$repo")"
[[ $(jq -r .model "$repo/.claude/settings.local.json") == opus ]] || fail "existing settings were dropped"
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

if ((fails > 0)); then
	echo "$fails failed"
	exit 1
fi
echo ok
