#!/usr/bin/env bash
# review-pr.sh runs jas's reviewer profile on a pull request and posts the
# review as a PR comment. The comment carries a marker with the reviewed
# head SHA and verdict, which check-pr-reviewed.sh reads before a
# merge.
#
# Usage: bash .agents/skills/pr-review-loop/scripts/review-pr.sh [--dry-run] <pr-number>
#   --dry-run  print the review instead of posting it
#
# Before each review the reviewer profile (this skill's reviewer/ folder) is
# installed from the base branch by the base branch's install-reviewer.sh, so
# the PR under review cannot change its own reviewer's instructions. This
# script and the merge hook are the copies in the checkout that runs them:
# run the loop from a checkout you trust, not from a PR you have not read.
#
# A PASS review's findings are all low; they are also appended to the open
# issue titled $LOW_ISSUE (created when missing), to fix in batches rather
# than in follow-up PRs.
#
# Exit codes: 0 PASS, 1 CHANGES, 2 usage error, 3 the review did not finish.
set -euo pipefail

readonly MAX_TOKENS=800000
readonly MAX_TURNS=40 # the pr-review skill budgets against this
readonly TIMEOUT=30m # GLM reviews of script-heavy PRs can take over 15 minutes
readonly PROFILE=reviewer
readonly MARKER=jas-review
readonly LOW_ISSUE="Low findings from jas reviews"
readonly HEAD_SYNC_TRIES=12 HEAD_SYNC_DELAY=5
readonly INSTALLER=.agents/skills/pr-review-loop/scripts/install-reviewer.sh

usage() {
	echo "usage: review-pr.sh [--dry-run] <pr-number>" >&2
	exit 2
}

for cmd in jas gh jq git tar; do
	command -v "$cmd" >/dev/null || { echo "review-pr: $cmd is not installed; see the pr-review-loop skill's Setup" >&2; exit 2; }
done

dry_run=false
if [[ ${1:-} == --dry-run ]]; then
	dry_run=true
	shift
fi
[[ $# -eq 1 && $1 =~ ^[0-9]+$ ]] || usage
pr=$1

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

pr_json=$(gh pr view "$pr" --json title,body,headRefName,baseRefName,state)
[[ $(jq -r .state <<<"$pr_json") == OPEN ]] || { echo "review-pr: PR #$pr is not open" >&2; exit 2; }
base_ref=$(jq -r .baseRefName <<<"$pr_json")

# The branch tip on the remote is the head; GitHub's headRefOid lags a fresh
# push, and the merge hook reads headRefOid, so wait until the two agree.
head=$(git ls-remote origin "refs/heads/$(jq -r .headRefName <<<"$pr_json")" | cut -f1)
[[ -n $head ]] || { echo "review-pr: PR #$pr has no branch on origin" >&2; exit 3; }
for ((i = 0; i < HEAD_SYNC_TRIES; i++)); do
	[[ $(gh pr view "$pr" --json headRefOid -q .headRefOid) == "$head" ]] && break
	sleep "$HEAD_SYNC_DELAY"
done
((i < HEAD_SYNC_TRIES)) || { echo "review-pr: GitHub does not show $head as PR #$pr's head yet" >&2; exit 3; }

git fetch -q origin "$base_ref" "$head"
base=$(git merge-base "origin/$base_ref" "$head") ||
	{ echo "review-pr: no merge base of origin/$base_ref and $head" >&2; exit 3; }

# The installer runs as committed on the base, never the PR's copy. A base
# from before the reviewer was kept in the repo has none; the profile
# already in the jas home is used then. A failed install stops the review:
# the profile in the jas home may be from any older base.
if installer=$(git show "origin/$base_ref:$INSTALLER" 2>/dev/null); then
	bash -c "$installer" install-reviewer "origin/$base_ref" ||
		{ echo "review-pr: could not install the reviewer from origin/$base_ref" >&2; exit 3; }
else
	echo "review-pr: origin/$base_ref has no reviewer installer; using the profile already in the jas home" >&2
fi
jas profile show "$PROFILE" >/dev/null 2>&1 || {
	echo "review-pr: no jas profile '$PROFILE' (~/.jas/profiles/$PROFILE with the pr-review skill)" >&2
	exit 2
}

# Issues the PR body links with a closing keyword or "Refs".
issue_context() {
	local body n
	body=$(jq -r '.body // ""' <<<"$pr_json")
	for n in $(grep -oiE '(close[sd]?|fix(e[sd])?|resolve[sd]?|refs?) #[0-9]+' <<<"$body" | grep -oE '[0-9]+' | sort -u); do
		gh issue view "$n" --json number,title,body \
			-q '"### Issue #\(.number): \(.title)\n\n\(.body)\n"' || true
	done
}

work=$(mktemp -d)
gh_empty=$(mktemp -d)
cleanup() {
	git worktree remove --force "$work/tree" 2>/dev/null || true
	rm -rf "$work" "$gh_empty"
}
trap cleanup EXIT
git worktree add -q --detach "$work/tree" "$head"

prompt=$(cat <<PROMPT
Review pull request #$pr before it merges. The checkout is the PR head
($head); the base is $base (diff with: git diff $base...HEAD).

### PR: $(jq -r '.title // ""' <<<"$pr_json")

$(jq -r '.body // ""' <<<"$pr_json")

$(issue_context)
PROMPT
)

# The reviewer runs in the throwaway worktree logged out of GitHub, so it
# can run tests but cannot comment, push or merge:
# - gh reads an empty config with no token in the environment;
# - the D-Bus session bus is unreachable, so neither gh nor its git
#   credential helper can read the token from the keyring;
# - git has no SSH agent, no credential helpers and a dead push URL.
# This guards against a reviewer that misuses its tools, not hostile code:
# the shell can still read files the user can read.
#
# The project is not trusted: a trusted project skill named pr-review would
# replace the profile's, letting the PR under review rewrite its own
# checklist. The reviewer reads the repo's AGENTS.md with its tools instead.
sandbox=(env -u GH_TOKEN -u GITHUB_TOKEN -u GH_ENTERPRISE_TOKEN -u GITHUB_ENTERPRISE_TOKEN
	-u SSH_AUTH_SOCK DBUS_SESSION_BUS_ADDRESS=disabled: GH_CONFIG_DIR="$gh_empty"
	GIT_SSH_COMMAND=false GIT_TERMINAL_PROMPT=0 GIT_CONFIG_COUNT=3
	GIT_CONFIG_KEY_0=remote.origin.pushurl GIT_CONFIG_VALUE_0=/push-disabled-in-review
	GIT_CONFIG_KEY_1=credential.helper GIT_CONFIG_VALUE_1=
	GIT_CONFIG_KEY_2=credential.https://github.com.helper GIT_CONFIG_VALUE_2=)

# sandbox_logged_out fails when gh or git can still find a GitHub token.
sandbox_logged_out() {
	! "${sandbox[@]}" gh auth status >/dev/null 2>&1 &&
		! printf 'protocol=https\nhost=github.com\n\n' |
		"${sandbox[@]}" git credential fill 2>/dev/null | grep -q '^password='
}
if ! sandbox_logged_out; then
	echo "review-pr: a GitHub token is still reachable inside the review sandbox; not running the reviewer" >&2
	exit 3
fi
set +e
(cd "$work/tree" && "${sandbox[@]}" \
	jas run --profile "$PROFILE" \
	--max-tokens "$MAX_TOKENS" --max-turns "$MAX_TURNS" --wrap-up --timeout "$TIMEOUT" --name "review PR #$pr" \
	"$prompt") >"$work/out" 2>"$work/err"
code=$?
set -e

review=$(awk '/^## Findings/{buf=""; keep=1} keep{buf=buf $0 "\n"} END{printf "%s", buf}' "$work/out")
verdict=$(grep -oE '^VERDICT: (PASS|CHANGES)$' <<<"$review" | tail -1 | cut -d' ' -f2 || true)
if [[ $code -ne 0 || -z $verdict ]]; then
	echo "review-pr: the review did not finish (jas exit $code, verdict '${verdict:-none}')" >&2
	tail -20 "$work/err" >&2
	exit 3
fi

comment="## jas review: $verdict

Reviewed \`${head:0:7}\` with \`jas run --profile $PROFILE\`.

$review
<!-- $MARKER sha=$head verdict=$verdict -->"

# findings is the review's Findings section without its heading, or "" when
# it lists none.
findings() {
	local f
	f=$(awk '/^## Findings/{keep=1; next} /^## /{keep=0} keep' <<<"$review" | sed '/^[[:space:]]*$/d')
	[[ $f == None. ]] || printf '%s\n' "$f"
}

# log_low appends a PASS review's findings to the low-findings issue.
log_low() {
	local low n
	low=$(findings)
	[[ -n $low ]] || return 0
	n=$(gh issue list --state open --search "in:title \"$LOW_ISSUE\"" --json number,title \
		-q "[.[] | select(.title == \"$LOW_ISSUE\")][0].number // empty") || return 1
	if [[ -z $n ]]; then
		n=$(gh issue create --title "$LOW_ISSUE" \
			--body "Low findings from jas PR reviews, appended by review-pr.sh; fix them in batches." | grep -oE '[0-9]+$') || return 1
	fi
	gh issue comment "$n" --body "From the jas review of #$pr (\`${head:0:7}\`):

$low" >/dev/null || return 1
	echo "review-pr: low findings logged in #$n" >&2
}

if $dry_run; then
	printf '%s\n' "$comment"
else
	gh pr comment "$pr" --body "$comment" >/dev/null
	printf '%s\n' "$comment"
	# The PASS is posted; a failure to log its findings must not turn the
	# exit code into CHANGES.
	if [[ $verdict == PASS ]]; then
		log_low || echo "review-pr: could not log the low findings; add them to the \"$LOW_ISSUE\" issue by hand" >&2
	fi
fi
[[ $verdict == PASS ]]
