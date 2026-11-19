#!/usr/bin/env bash
# link-claude-code.sh sets up this skill for Claude Code in the current clone.
# jas and other agents read .agents/skills directly and need none of this.
# .claude/ is gitignored, so each clone runs it once (make claude-setup):
#
# - links .claude/skills/pr-review-loop to this skill, because Claude Code
#   reads skills only from .claude/skills, not .agents/skills;
# - adds the merge-gate hook (check-pr-reviewed.sh) to
#   .claude/settings.local.json, keeping the file's other settings.
#
# Running it again changes nothing.
set -euo pipefail

readonly SKILL=pr-review-loop
readonly HOOK='"$CLAUDE_PROJECT_DIR"/.agents/skills/pr-review-loop/scripts/check-pr-reviewed.sh'
readonly HOOK_TIMEOUT=30

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

dest=.claude/skills/$SKILL
target=../../.agents/skills/$SKILL
# Anything but our own link (an old checkout's copy, say) is left alone:
# ln would link inside a directory, and that may hold the owner's work.
if [[ -e $dest || -L $dest ]] && ! [[ -L $dest && $(readlink "$dest") == "$target" ]]; then
	echo "link-claude-code: $dest exists and is not a link to $target; move it away and rerun" >&2
	exit 1
fi
mkdir -p .claude/skills
ln -sfn "$target" "$dest"
# Git Bash copies instead of linking unless symlinks are enabled, and so do
# filesystems without symlinks (some docker mounts); a copy would go stale on
# the next pull.
if ! [[ -L $dest ]]; then
	rm -rf "$dest"
	echo "link-claude-code: your ln made a copy, not a link; on Windows enable symlinks (Developer Mode, and MSYS=winsymlinks:nativestrict), elsewhere run from a filesystem that supports symlinks, and rerun" >&2
	exit 1
fi

settings=.claude/settings.local.json
[[ -f $settings ]] || echo '{}' >"$settings"
tmp=$(mktemp)
jq --arg cmd "$HOOK" --argjson timeout "$HOOK_TIMEOUT" '
	.hooks.PreToolUse //= []
	| if any(.hooks.PreToolUse[]; any(.hooks[]?; .command == $cmd)) then .
	  else .hooks.PreToolUse += [{matcher: "Bash", hooks: [{type: "command", command: $cmd, timeout: $timeout}]}]
	  end' "$settings" >"$tmp"
mv "$tmp" "$settings"

echo "Claude Code: skill linked at .claude/skills/$SKILL, merge hook in $settings"
