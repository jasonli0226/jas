#!/usr/bin/env bash
# install-reviewer.sh installs the reviewer profile kept in this skill's
# reviewer/ folder into the jas home, as committed at <ref>. review-pr.sh
# runs it with the base branch before every review, so a `git pull` updates
# the reviewer, and a PR that changes the reviewer is still reviewed by the
# one on its base branch.
#
# AGENTS.md, skills/ and config.json are replaced, except that the model in
# an existing config.json is kept, so a model chosen in the jas home stays.
#
# Usage: bash .agents/skills/pr-review-loop/scripts/install-reviewer.sh <ref>
# Exit codes: 0 installed, 1 <ref> has no reviewer profile, 2 usage error.
set -euo pipefail

readonly PROFILE=reviewer
readonly SRC=.agents/skills/pr-review-loop/reviewer

[[ $# -eq 1 ]] || { echo "usage: install-reviewer.sh <ref>" >&2; exit 2; }
ref=$1

cd "$(git rev-parse --show-toplevel)"
git cat-file -e "$ref:$SRC/AGENTS.md" 2>/dev/null || {
	echo "install-reviewer: $ref has no $SRC" >&2
	exit 1
}

# Like jas's own profiles: owner-only.
umask 077
dest=${JAS_HOME:-$HOME/.jas}/profiles/$PROFILE
mkdir -p "$dest"
# Stage everything first, so a failed install leaves the old profile whole.
stage=$(mktemp -d "$dest/.install.XXXXXX")
trap 'rm -rf "$stage"' EXIT
mkdir "$stage/skills"
git archive "$ref:$SRC/skills" | tar -x -C "$stage/skills"
git show "$ref:$SRC/AGENTS.md" >"$stage/AGENTS.md"
git show "$ref:$SRC/config.json" >"$stage/config.json"
if [[ -f $dest/config.json ]]; then
	model=$(jq -r '.model // empty' "$dest/config.json")
	if [[ -n $model ]]; then
		jq --arg model "$model" '.model = $model' "$stage/config.json" >"$stage/config.merged"
		mv "$stage/config.merged" "$stage/config.json"
	fi
fi

rm -rf "$dest/skills"
mv "$stage/skills" "$dest/skills"
mv "$stage/AGENTS.md" "$dest/AGENTS.md"
mv "$stage/config.json" "$dest/config.json"
