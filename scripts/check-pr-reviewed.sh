#!/usr/bin/env bash
# check-pr-reviewed.sh is a Claude Code PreToolUse hook for Bash. It blocks
# `gh pr merge` unless scripts/review-pr.sh posted a PASS review for the
# PR's current head commit. It reads the hook payload on stdin; exit 2
# blocks the call and shows stderr to the agent.
set -euo pipefail

readonly MARKER=jas-review
# Only gh in command position counts (line start, or after ; & | or (), so
# text that merely mentions the command, like a heredoc, passes.
readonly MERGE_RE='(^|[;&|(])[[:space:]]*gh[[:space:]]+pr[[:space:]]+merge([[:space:]]|$)'

cmd=$(jq -r '.tool_input.command // ""')
merge_line=$(grep -m1 -E "$MERGE_RE" <<<"$cmd" || true)
[[ -n $merge_line ]] || exit 0

block() {
	echo "merge blocked: $1" >&2
	echo "Run scripts/review-pr.sh <pr> and address its findings first." >&2
	exit 2
}

# The PR is the first bare number after "merge"; with none, gh merges the
# current branch's PR.
pr=$(grep -oE 'gh[[:space:]]+pr[[:space:]]+merge([[:space:]]+[^[:space:]]+)*' <<<"$merge_line" |
	tr -s '[:space:]' '\n' | grep -m1 -xE '#?[0-9]+' | tr -d '#' || true)
view=(gh pr view)
[[ -n $pr ]] && view+=("$pr")

pr_json=$("${view[@]}" --json number,headRefOid,comments 2>/dev/null) || block "could not look up the PR"
me=$(gh api user -q .login 2>/dev/null) || block "could not look up the GitHub user"
head=$(jq -r .headRefOid <<<"$pr_json")
number=$(jq -r .number <<<"$pr_json")

# Only the reviewer's own comments count, so nobody else can post a PASS.
verdict=$(jq -r --arg me "$me" --arg marker "$MARKER sha=$head" '
	[.comments[] | select(.author.login == $me) | .body
	 | capture("<!-- " + $marker + " verdict=(?<v>PASS|CHANGES) -->")? | .v]
	| last // ""' <<<"$pr_json")

case $verdict in
PASS) exit 0 ;;
CHANGES) block "the jas review of PR #$number at ${head:0:7} asked for changes" ;;
*) block "PR #$number has no jas review of its head commit ${head:0:7}" ;;
esac
