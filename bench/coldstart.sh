#!/usr/bin/env bash
# Cold-start hygiene benchmarks (design §7): `jas --version` and
# time-to-first-request. Usage: bench/coldstart.sh [path/to/jas]
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
jas="$(realpath "${1:-$root/bin/jas}")"
[ -x "$jas" ] || { echo "coldstart: $jas not found; run 'make build' first" >&2; exit 2; }

if command -v hyperfine >/dev/null 2>&1; then
  hyperfine -N --warmup 3 "$jas --version"
else
  runs=50
  times=()
  for _ in $(seq "$runs"); do
    s=$(date +%s%N)
    "$jas" --version >/dev/null
    e=$(date +%s%N)
    times+=($(( (e - s) / 1000 )))
  done
  sorted=($(printf '%s\n' "${times[@]}" | sort -n))
  printf 'jas --version: median %d us, min %d us, max %d us (%d runs, loop fallback)\n' \
    "${sorted[$((runs / 2))]}" "${sorted[0]}" "${sorted[$((runs - 1))]}" "$runs"
fi

(cd "$root" && go run ./bench/ttfr "$jas")
