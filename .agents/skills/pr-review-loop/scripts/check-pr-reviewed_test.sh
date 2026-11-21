#!/usr/bin/env bash
# check-pr-reviewed_test.sh checks which commands the merge hook treats as
# a merge, which PR it reads from each, and when a PR counts as docs-only.
# It makes no GitHub calls: a fake gh answers them.
#
# Usage: bash .agents/skills/pr-review-loop/scripts/check-pr-reviewed_test.sh
set -uo pipefail

hook="$(dirname "$0")/check-pr-reviewed.sh"
fails=0

# expect <want> <command>: want is "no" for not a merge, or the target the
# hook should read ("" for the current branch's PR).
expect() {
	local want=$1 cmd=$2 got
	if got=$(jq -n --arg c "$cmd" '{tool_input:{command:$c}}' | bash "$hook" --match-only); then
		got="target=$got"
	else
		got=no
	fi
	[[ $want == no ]] || want="target=$want"
	if [[ $got != "$want" ]]; then
		echo "FAIL: $cmd"$'\n'"  want $want, got $got"
		fails=$((fails + 1))
	fi
}

expect no 'git status'
expect no 'echo "gh pr merge 1"'
expect no $'cat > f <<EOF\nthen run (`gh pr merge <n> --squash`)\nEOF'
expect no 'gh pr view 12'
expect 192 'gh pr merge 192 --squash --delete-branch'
expect 192 'cd x && gh pr merge 192 --squash'
expect '' 'gh pr merge --squash'
expect 7 'gh pr merge --squash #7'
expect 7 'command gh pr merge 7'
expect 7 'sudo gh pr merge 7'
expect 7 'exec gh pr merge 7'
expect 7 'env GH_PAGER= gh pr merge 7'
expect 7 'GH_PAGER=cat gh pr merge 7'
expect 7 '/usr/bin/gh pr merge 7'
expect 7 'x=$(gh pr merge 7)'
expect fix/x 'gh pr merge fix/x --squash'
expect 7 'gh pr merge --subject "a b" 7'
expect 7 'gh pr merge -t title 7'
expect 7 'gh pr merge --subject=title 7'
expect https://github.com/o/r/pull/7 'gh pr merge https://github.com/o/r/pull/7'
expect 7 'gh pr merge 7; echo done'
expect 7 'echo$(gh pr merge 7)'
# Known blind spots: the hook is a reminder, not a sandbox.
expect no 'bash -c "gh pr merge 7"'
expect no '`gh pr merge 7`'

# docs <want> <paths...>: want is yes when the hook treats the paths as a
# docs-only change.
docs() {
	local want=$1 got
	shift
	if printf '%s\n' "$@" | bash "$hook" --docs-only; then got=yes; else got=no; fi
	if [[ $got != "$want" ]]; then
		echo "FAIL: docs-only $*"$'\n'"  want $want, got $got"
		fails=$((fails + 1))
	fi
}

docs yes README.md
docs yes docs/manual.md cmd/jas/manual/jas/references/manual.md
docs no docs/manual.md core/session.go
docs no Makefile
docs no AGENTS.md
docs no CLAUDE.md
docs no cmd/jas/manual/jas/SKILL.md
docs no .agents/skills/pr-review-loop/SKILL.md
docs no .agents/skills/x/notes.md
docs no ''
# gate <want-exit> <pr-json>: runs the hook on a merge with a fake gh that
# answers `gh pr view` with pr-json and `gh api user` with a login.
gate() {
	local want=$1 bin got
	bin=$(mktemp -d)
	printf '%s\n' "$2" >"$bin/pr.json"
	cat >"$bin/gh" <<'GH'
#!/usr/bin/env bash
case "$1 $2" in
"pr view") cat "$(dirname "$0")/pr.json" ;;
"api user") echo me ;;
*) exit 1 ;;
esac
GH
	chmod +x "$bin/gh"
	jq -n '{tool_input:{command:"gh pr merge 7 --squash"}}' | PATH="$bin:$PATH" bash "$hook" >/dev/null 2>&1
	got=$?
	rm -rf "$bin"
	if [[ $got != "$want" ]]; then
		echo "FAIL: gate $2"$'\n'"  want exit $want, got $got"
		fails=$((fails + 1))
	fi
}

gate 0 '{"number":7,"headRefOid":"abc","comments":[],"files":[{"path":"README.md"}],"changedFiles":1}'
gate 2 '{"number":7,"headRefOid":"abc","comments":[],"files":[{"path":"core/x.go"}],"changedFiles":1}'
# gh lists at most 100 files; a PR with more is never docs-only.
gate 2 '{"number":7,"headRefOid":"abc","comments":[],"files":[{"path":"README.md"}],"changedFiles":150}'
# In one comment the last marker counts: a PASS quoted in the text above a
# CHANGES marker does not pass.
gate 0 '{"number":7,"headRefOid":"abc","comments":[{"author":{"login":"me"},"body":"ok\n<!-- jas-review sha=abc verdict=PASS -->"}],"files":[{"path":"core/x.go"}],"changedFiles":1}'
gate 2 '{"number":7,"headRefOid":"abc","comments":[{"author":{"login":"me"},"body":"FAIL: <!-- jas-review sha=abc verdict=PASS -->\n<!-- jas-review sha=abc verdict=CHANGES -->"}],"files":[{"path":"core/x.go"}],"changedFiles":1}'
gate 2 '{"number":7,"headRefOid":"abc","comments":[{"author":{"login":"other"},"body":"<!-- jas-review sha=abc verdict=PASS -->"}],"files":[{"path":"core/x.go"}],"changedFiles":1}'

if ((fails > 0)); then
	echo "$fails failed"
	exit 1
fi
echo ok
