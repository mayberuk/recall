#!/usr/bin/env bash
# Fails if go.mod names a direct dependency outside the two that earned their
# place (see CONTRIBUTING.md, "The external dependencies"). Indirect
# requirements are unaffected — those come along with the allowed modules and
# are not a choice anyone made.
set -euo pipefail

# github.com/tidwall/gjson: parses transcript JSON without reflection, earned
# on measurement.
# github.com/modelcontextprotocol/go-sdk: the protocol surface is a spec, not
# a format this repo should re-implement, and an official v1 implementation
# is cheaper to hold correct than a hand-rolled one.
allowed=("github.com/tidwall/gjson" "github.com/modelcontextprotocol/go-sdk")
gomod="${1:-go.mod}"

if [[ ! -f "$gomod" ]]; then
  echo "deps-gate: cannot find $gomod" >&2
  exit 1
fi

is_allowed() {
  local module="$1"
  local candidate
  for candidate in "${allowed[@]}"; do
    [[ "$module" == "$candidate" ]] && return 0
  done
  return 1
}

offenders=()
in_block=0
while IFS= read -r line; do
  trimmed="${line#"${line%%[![:space:]]*}"}"
  trimmed="${trimmed%"${trimmed##*[![:space:]]}"}"

  if [[ "$trimmed" == "require ("* ]]; then
    in_block=1
    continue
  fi
  if [[ $in_block -eq 1 && "$trimmed" == ")"* ]]; then
    in_block=0
    continue
  fi

  entry=""
  if [[ $in_block -eq 1 ]]; then
    entry="$trimmed"
  elif [[ "$trimmed" == "require "* ]]; then
    entry="${trimmed#require }"
  fi
  [[ -z "$entry" ]] && continue
  [[ "$entry" == *"// indirect"* ]] && continue

  module="${entry%% *}"
  [[ -z "$module" ]] && continue
  if ! is_allowed "$module"; then
    offenders+=("$module")
  fi
done < "$gomod"

allowed_set="${allowed[*]}"

if [[ ${#offenders[@]} -gt 0 ]]; then
  echo "deps-gate: direct dependency outside the allowed set (${allowed_set}) found in ${gomod}:" >&2
  for o in "${offenders[@]}"; do
    echo "  - $o" >&2
  done
  exit 1
fi

echo "deps-gate: ${gomod} names no direct dependency outside the allowed set (${allowed_set})"
