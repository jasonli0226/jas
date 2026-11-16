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

mkdir -p .claude/skills
ln -sfn "../../.agents/skills/$SKILL" ".claude/skills/$SKILL"

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
