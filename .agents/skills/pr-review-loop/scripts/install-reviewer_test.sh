#!/usr/bin/env bash
# install-reviewer_test.sh runs install-reviewer.sh in a throwaway repo: it
# installs the committed profile, not the working tree's, replaces skills,
# keeps an existing config.json, and refuses a ref without a profile.
#
# Usage: bash .agents/skills/pr-review-loop/scripts/install-reviewer_test.sh
set -uo pipefail

script="$(cd "$(dirname "$0")" && pwd)/install-reviewer.sh"
fails=0
fail() {
	echo "FAIL: $1"
	fails=$((fails + 1))
}

repo=$(mktemp -d)
home=$(mktemp -d)
trap 'rm -rf "$repo" "$home"' EXIT
git -C "$repo" init -q
git -C "$repo" -c user.name=t -c user.email=t@t commit -q --allow-empty -m empty
empty=$(git -C "$repo" rev-parse HEAD)
src=$repo/.agents/skills/pr-review-loop/reviewer
mkdir -p "$src/skills/pr-review"
echo AGENTS-1 >"$src/AGENTS.md"
echo '{"model":"from-repo"}' >"$src/config.json"
echo SKILL-1 >"$src/skills/pr-review/SKILL.md"
git -C "$repo" add -A
git -C "$repo" -c user.name=t -c user.email=t@t commit -q -m reviewer
# Uncommitted edits must not be installed.
echo AGENTS-DIRTY >"$src/AGENTS.md"

install() { (cd "$repo" && JAS_HOME=$home bash "$script" "$1" >/dev/null 2>&1); }
dest=$home/profiles/reviewer

install HEAD || fail "install exited non-zero"
[[ $(cat "$dest/AGENTS.md") == AGENTS-1 ]] || fail "AGENTS.md is not the committed one"
[[ $(cat "$dest/skills/pr-review/SKILL.md") == SKILL-1 ]] || fail "SKILL.md not installed"
[[ $(jq -r .model "$dest/config.json") == from-repo ]] || fail "config.json not written when missing"

# A second install replaces skills/ and keeps the owner's config.json.
echo '{"model":"mine"}' >"$dest/config.json"
mkdir -p "$dest/skills/stale"
install HEAD || fail "second install exited non-zero"
[[ $(jq -r .model "$dest/config.json") == mine ]] || fail "existing config.json was overwritten"
[[ ! -e $dest/skills/stale ]] || fail "a skill not in the repo was left behind"

if install "$empty"; then
	fail "a ref without a reviewer profile was accepted"
fi

if ((fails > 0)); then
	echo "$fails failed"
	exit 1
fi
echo ok
