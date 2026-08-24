#!/usr/bin/env bash
# Enforces a per-package coverage floor from a profile written by
# `go test -coverprofile`. A package absent from the profile is not
# automatically a pass or a failure — it is either untested or unmeasurable,
# and this prints which so that state stays visible instead of silent:
#
#   - no test files: go test never instrumented it; not this gate's business.
#   - no statements: it was instrumented but has nothing to cover (e.g. a pure
#     type-definitions file), so a percentage would be meaningless.
#
# internal/fixtures gets an 80% floor instead of 90%. Its remaining uncovered
# lines are seven t.Fatalf guards that only fire inside a re-exec'd subprocess
# test binary, and a subprocess's coverage counters do not merge into the
# profile this script reads — so 90% here is a number no honest run reaches.
# Do not delete this exception quietly, and do not widen it to cover a package
# that is merely undertested.
#
# bench gets a 65% floor. It is real measurement and reporting code with real
# tests, not a package to let rot, but its floor sits below the 90% default
# because a slice of it only runs against a real machine's CPU model file or
# a full `make bench` invocation, neither of which this gate exercises.
#
# bench/cmd/benchrun, bench/turns, bench/groupbench, tests/acceptance/runner and
# scripts/demo are exempt from any floor: none is reachable from ./cmd/recall
# (confirmed by `go list -deps ./cmd/recall`), so they are harness, not shipped
# code, and the acceptance runner in particular is validated by being run, not
# by being covered. scripts/demo is the same case: it writes the corpus the
# documented examples are taken from, and `scripts/demo.sh` failing to produce
# them is a louder signal than a covered line. groupbench is measured the same way — `make bench-gate` fails when
# its comparison breaches, which is a stronger check than a covered line. Each
# is still printed with its real percentage below — an exemption that hid the
# number is how a harness package rots unnoticed. New packages are exempted by
# name, one at a time, never by a path-prefix rule; that friction is
# deliberate.
set -euo pipefail

profile="${1:-coverage.out}"
default_floor=90

if [[ ! -f "$profile" ]]; then
  echo "coverage-gate: cannot find coverage profile $profile" >&2
  exit 1
fi

mode_line="$(head -n1 "$profile")"
if [[ "$mode_line" != mode:* ]]; then
  echo "coverage-gate: $profile has no 'mode:' header; is it a go coverage profile?" >&2
  exit 1
fi

module="$(go list -m)"
fixtures_pkg="${module}/internal/fixtures"
bench_pkg="${module}/bench"
harness_pkgs=(
  "${module}/bench/cmd/benchrun"
  "${module}/bench/turns"
  "${module}/bench/groupbench"
  "${module}/tests/acceptance/runner"
  "${module}/scripts/demo"
)

fail=0
tmp_profile="$(mktemp)"
trap 'rm -f "$tmp_profile"' EXIT

while IFS='|' read -r pkg test_files x_test_files; do
  test_file_count=$((test_files + x_test_files))

  awk -v pkg="$pkg" -v hdr="$mode_line" '
    BEGIN { print hdr }
    {
      file = $1
      sub(/:.*/, "", file)
      slash = match(file, /\/[^\/]*$/)
      dir = substr(file, 1, slash - 1)
      if (dir == pkg) print
    }
  ' "$profile" > "$tmp_profile"

  line_count="$(($(wc -l < "$tmp_profile") - 1))"
  if [[ "$line_count" -le 0 ]]; then
    if [[ "$test_file_count" -eq 0 ]]; then
      printf '%-60s %10s  (no test files)\n' "$pkg" "n/a"
    else
      printf '%-60s %10s  (no statements)\n' "$pkg" "n/a"
    fi
    continue
  fi

  pct="$(go tool cover -func="$tmp_profile" | awk '/^total:/ { gsub("%", "", $3); print $3 }')"

  is_harness=0
  for h in "${harness_pkgs[@]}"; do
    if [[ "$pkg" == "$h" ]]; then
      is_harness=1
      break
    fi
  done
  if [[ "$is_harness" -eq 1 ]]; then
    printf '%-60s %9s%%  harness, no floor\n' "$pkg" "$pct"
    continue
  fi

  floor="$default_floor"
  if [[ "$pkg" == "$fixtures_pkg" ]]; then
    floor=80
  elif [[ "$pkg" == "$bench_pkg" ]]; then
    floor=65
  fi

  status="ok"
  if ! awk -v p="$pct" -v f="$floor" 'BEGIN { exit !(p + 0 >= f + 0) }'; then
    status="FAIL (floor ${floor}%)"
    fail=1
  fi
  printf '%-60s %9s%%  %s\n' "$pkg" "$pct" "$status"
done < <(go list -f '{{.ImportPath}}|{{len .TestGoFiles}}|{{len .XTestGoFiles}}' ./...)

if [[ "$fail" -ne 0 ]]; then
  echo "coverage-gate: one or more packages are below their floor" >&2
  exit 1
fi
echo "coverage-gate: every package with statements meets its floor"
