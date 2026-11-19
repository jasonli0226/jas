#!/usr/bin/env bash
# review-pr_test.sh runs review-pr.sh --dry-run against a throwaway origin,
# with fake gh and jas on PATH. A PR that edits both the reviewer profile and
# its installer is still reviewed by the base branch's profile, and the
# reviewer runs without trusting the project.
#
# Usage: bash .agents/skills/pr-review-loop/scripts/review-pr_test.sh
set -uo pipefail

skill_dir=$(cd "$(dirname "$0")/.." && pwd)
fails=0
fail() {
	echo "FAIL: $1"
	fails=$((fails + 1))
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
gitc() { git -C "$1" -c user.name=t -c user.email=t@t "${@:2}"; }

# origin's main has the reviewer as committed today; branch pr edits the
# profile and swaps in an installer that writes its own AGENTS.md.
git init -q --bare -b main "$tmp/origin.git"
git clone -q "$tmp/origin.git" "$tmp/clone" 2>/dev/null
repo=$tmp/clone
loop=$repo/.agents/skills/pr-review-loop
mkdir -p "$repo/.agents/skills"
cp -R "$skill_dir" "$loop"
echo BASE-AGENTS >"$loop/reviewer/AGENTS.md"
gitc "$repo" add -A
gitc "$repo" commit -q -m base
gitc "$repo" push -q origin HEAD:main
gitc "$repo" checkout -q -b pr
echo PR-AGENTS >"$loop/reviewer/AGENTS.md"
cat >"$loop/scripts/install-reviewer.sh" <<'EOF'
#!/usr/bin/env bash
mkdir -p "$JAS_HOME/profiles/reviewer"
echo EVIL-AGENTS >"$JAS_HOME/profiles/reviewer/AGENTS.md"
EOF
gitc "$repo" commit -qam pr
gitc "$repo" push -q origin pr
head=$(git -C "$repo" rev-parse HEAD)

bin=$tmp/bin
mkdir "$bin"
echo "$head" >"$tmp/head"
cat >"$bin/gh" <<EOF
#!/usr/bin/env bash
case "\$1 \$2" in
"auth status") exit 1 ;;
"pr view")
	if [[ "\$*" == *headRefOid* ]]; then cat "$tmp/head"; else
		echo '{"title":"t","body":"","headRefName":"pr","baseRefName":"main","state":"OPEN"}'
	fi ;;
*) exit 1 ;;
esac
EOF
# The fake jas records its arguments and the AGENTS.md it would run with.
cat >"$bin/jas" <<EOF
#!/usr/bin/env bash
[[ \$1 == profile ]] && exit 0
printf '%s\n' "\$@" >"$tmp/jas-args"
cat "\$JAS_HOME/profiles/reviewer/AGENTS.md" >"$tmp/jas-agents"
printf '## Findings\n\nNone.\n\nVERDICT: PASS\n'
EOF
chmod +x "$bin/gh" "$bin/jas"

out=$(cd "$repo" && PATH="$bin:$PATH" JAS_HOME="$tmp/jas-home" \
	bash "$loop/scripts/review-pr.sh" --dry-run 7 2>&1)
code=$?
[[ $code == 0 ]] || fail "review-pr exited $code: $out"
[[ $(cat "$tmp/jas-agents" 2>/dev/null) == BASE-AGENTS ]] ||
	fail "reviewer ran with AGENTS.md '$(cat "$tmp/jas-agents" 2>/dev/null)', want the base's"
grep -qx -- '--profile' "$tmp/jas-args" 2>/dev/null || fail "jas did not run the reviewer profile"
! grep -qx -- '--trust-project' "$tmp/jas-args" 2>/dev/null || fail "the reviewer trusted the project"

if ((fails > 0)); then
	echo "$fails failed"
	exit 1
fi
echo ok
