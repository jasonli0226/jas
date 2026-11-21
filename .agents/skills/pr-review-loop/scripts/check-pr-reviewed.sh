#!/usr/bin/env bash
# check-pr-reviewed.sh is a Claude Code PreToolUse hook for Bash. It blocks
# `gh pr merge` unless review-pr.sh posted a PASS review for the
# PR's current head commit, or the PR changes docs only (see docs_only). It
# reads the hook payload on stdin; exit 2 blocks the call and shows stderr to
# the agent.
#
# --match-only prints the PR target ("" for the current branch's PR) and
# exits 0 when the command merges, or exits 1 when it does not; it makes no
# GitHub calls (check-pr-reviewed_test.sh uses it). --docs-only reads paths,
# one per line, and exits 0 when they are docs only, else 1.
set -euo pipefail

readonly MARKER=jas-review
readonly REVIEW_SCRIPT=.agents/skills/pr-review-loop/scripts/review-pr.sh
# gh counts only in command position: at line start or after ; & | or (,
# behind any of these prefixes (command, exec, sudo, nohup, time, env, VAR=x)
# and with an optional directory. Text that merely mentions the command,
# like a heredoc or an echo, passes. It is a net for an agent forgetting the
# review, not a sandbox: a merge hidden in bash -c "..." or backticks is not
# seen (check-pr-reviewed_test.sh pins this).
readonly PREFIX_RE='((command|exec|sudo|nohup|time|env|[A-Za-z_][A-Za-z0-9_]*=[^[:space:]]*)[[:space:]]+)*'
readonly MERGE_RE="(^|[;&|(])[[:space:]]*${PREFIX_RE}([^[:space:];&|()]*/)?gh[[:space:]]+pr[[:space:]]+merge([[:space:]]|\$)"
# gh pr merge flags that take a separate value.
readonly VALUE_FLAGS=' -b --body -F --body-file -t --subject -A --author-email --match-head-commit -R --repo '

# docs_only reads changed paths, one per line, and succeeds when there is at
# least one and every one is a doc: a .md file that is not agent
# instructions (SKILL.md, AGENTS.md, CLAUDE.md, anything under .agents/),
# which change how agents behave and so get reviewed.
docs_only() {
	local p n=0
	while IFS= read -r p; do
		[[ -n $p ]] || continue
		n=$((n + 1))
		case $p in
		.agents/* | SKILL.md | */SKILL.md | AGENTS.md | */AGENTS.md | CLAUDE.md | */CLAUDE.md) return 1 ;;
		*.md) ;;
		*) return 1 ;;
		esac
	done
	((n > 0))
}

if [[ ${1:-} == --docs-only ]]; then
	docs_only
	exit
fi

match_only=false
[[ ${1:-} == --match-only ]] && match_only=true

cmd=$(jq -r '.tool_input.command // ""')
merge_line=$(grep -m1 -E "$MERGE_RE" <<<"$cmd" || true)
if [[ -z $merge_line ]]; then
	$match_only && exit 1
	exit 0
fi

# merge_target prints the first argument after "merge" that is not a flag or
# a flag's value: a PR number, URL or branch. Empty means the current branch.
merge_target() {
	local args skip=false tok toks
	args=$(sed -E 's/.*gh[[:space:]]+pr[[:space:]]+merge//; s/[;&|)].*//' <<<"$merge_line")
	# xargs splits like the shell does, so a quoted flag value stays one
	# word; on unbalanced quotes fall back to splitting on blanks.
	mapfile -t toks < <(xargs -n1 printf '%s\n' <<<"$args" 2>/dev/null || tr -s '[:space:]' '\n' <<<"$args")
	for tok in "${toks[@]}"; do
		if $skip; then
			skip=false
		elif [[ $tok == -* ]]; then
			[[ $tok != *=* && $VALUE_FLAGS == *" $tok "* ]] && skip=true
		else
			tr -d "'\"#" <<<"$tok"
			return 0
		fi
	done
	return 0
}
target=$(merge_target)

if $match_only; then
	printf '%s\n' "$target"
	exit 0
fi

block() {
	echo "merge blocked: $1" >&2
	echo "Run bash $REVIEW_SCRIPT <pr> and address its findings first." >&2
	exit 2
}

view=(gh pr view)
[[ -n $target ]] && view+=("$target")
pr_json=$("${view[@]}" --json number,headRefOid,comments,files,changedFiles 2>/dev/null) || block "could not look up the PR"
# A docs-only PR needs no review. gh lists at most 100 files, so a longer
# list than it returned is never taken as docs-only.
if jq -e '(.files | length) == .changedFiles' <<<"$pr_json" >/dev/null; then
	jq -r '.files[].path' <<<"$pr_json" | docs_only && exit 0
fi
me=$(gh api user -q .login 2>/dev/null) || block "could not look up the GitHub user"
head=$(jq -r .headRefOid <<<"$pr_json")
number=$(jq -r .number <<<"$pr_json")

# Only the reviewer's own comments count, so nobody else can post a PASS.
# In each comment the last marker counts: review-pr.sh writes it last, and
# text before it (test output, the review) may quote one.
verdict=$(jq -r --arg me "$me" --arg marker "$MARKER sha=$head" '
	[.comments[] | select(.author.login == $me) | .body
	 | [scan("<!-- " + $marker + " verdict=(PASS|CHANGES) -->")] | last | .[0]? // empty]
	| last // ""' <<<"$pr_json")

case $verdict in
PASS) exit 0 ;;
CHANGES) block "the jas review of PR #$number at ${head:0:7} asked for changes" ;;
*) block "PR #$number has no jas review of its head commit ${head:0:7}" ;;
esac
