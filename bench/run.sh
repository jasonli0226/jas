#!/usr/bin/env bash
# Run each task in bench/tasks.txt once against a fresh copy of bench/fixture
# and print: task | turns | prompt tokens | completion tokens | cached tokens | wall s
#
#   bench/run.sh                 # jas (bin/jas)
#   bench/run.sh jas             # same
#   bench/run.sh pi              # pi --mode json -p --model "$BENCH_MODEL"
#
# Needs OPENROUTER_API_KEY in the environment (passed to jas as JAS_API_KEY;
# never printed). Needs jq.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
agent="${1:-jas}"
model="${BENCH_MODEL:-z-ai/glm-5.3}"
base_url="${BENCH_BASE_URL:-https://openrouter.ai/api/v1}"
: "${OPENROUTER_API_KEY:?set OPENROUTER_API_KEY}"
command -v jq >/dev/null || { echo "run.sh: jq is required" >&2; exit 2; }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
# Agent stderr goes to a file that outlives the run (it may hold provider errors).
logdir="$(mktemp -d "${TMPDIR:-/tmp}/jas-bench-logs.XXXXXX")"

printf '%-62s | %5s | %13s | %17s | %13s | %7s\n' task turns "prompt tokens" "completion tokens" "cached tokens" "wall s"
while IFS= read -r task; do
  [ -n "$task" ] || continue
  work="$(mktemp -d "$tmp/work.XXXXXX")"
  cp -R "$root/bench/fixture/." "$work/"
  git -C "$work" init -q
  out="$tmp/out.jsonl"
  log="$logdir/agent-$(date +%s%N).log"
  rc=0
  start=$(date +%s%N)
  case "$agent" in
    jas)
      (cd "$work" && JAS_HOME="$tmp/home" JAS_API_KEY="$OPENROUTER_API_KEY" JAS_BASE_URL="$base_url" JAS_MODEL="$model" \
        "$root/bin/jas" run --json "$task" >"$out" 2>"$log" </dev/null) || rc=$?
      sums=$(jq -s -r '[.[] | select(.type=="usage")] |
        "\(length) \(map(.prompt_tokens)|add // 0) \(map(.completion_tokens)|add // 0) \(map(.cached_tokens)|add // 0)"' "$out" 2>/dev/null) || sums=""
      ;;
    pi)
      (cd "$work" && pi --mode json -p --model "$model" "$task" >"$out" 2>"$log" </dev/null) || rc=$?
      # pi's input excludes cache reads; add them back so prompt tokens are comparable.
      sums=$(jq -s -r '[.[] | select(.type=="turn_end") | .message.usage] |
        "\(length) \(map(.input + .cacheRead)|add // 0) \(map(.output)|add // 0) \(map(.cacheRead)|add // 0)"' "$out" 2>/dev/null) || sums=""
      ;;
    *) echo "run.sh: unknown agent '$agent' (jas|pi)" >&2; exit 2 ;;
  esac
  end=$(date +%s%N)
  [ -n "$sums" ] || { echo "  (could not parse agent output; reporting zeros)" >&2; sums="0 0 0 0"; }
  read -r turns prompt completion cached <<<"$sums"
  printf '%-62s | %5s | %13s | %17s | %13s | %7s\n' "$task" "$turns" "$prompt" "$completion" "$cached" \
    "$(awk -v s="$start" -v e="$end" 'BEGIN{printf "%.1f", (e-s)/1e9}')"
  if [ "$turns" = 0 ] || [ "$rc" != 0 ]; then echo "  (agent exit code $rc; stderr log: $log)" >&2; fi
done <"$root/bench/tasks.txt"
