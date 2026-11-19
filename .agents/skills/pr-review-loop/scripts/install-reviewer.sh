#!/usr/bin/env bash
# install-reviewer.sh installs the reviewer profile kept in this skill's
# reviewer/ folder into the jas home, as committed at <ref>. review-pr.sh
# runs it with the base branch before every review, so a `git pull` updates
# the reviewer, and a PR that changes the reviewer is still reviewed by the
# one on its base branch.
#
# AGENTS.md and skills/ are replaced. config.json is written only when
# missing, so a model chosen in the jas home is kept.
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

dest=${JAS_HOME:-$HOME/.jas}/profiles/$PROFILE
mkdir -p "$dest"
rm -rf "$dest/skills"
mkdir "$dest/skills"
git archive "$ref:$SRC/skills" | tar -x -C "$dest/skills"
git show "$ref:$SRC/AGENTS.md" >"$dest/AGENTS.md"
[[ -f $dest/config.json ]] || git show "$ref:$SRC/config.json" >"$dest/config.json"
