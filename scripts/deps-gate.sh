#!/usr/bin/env bash
# Fails if go.mod names a direct dependency other than the one that earned its
# place on measurement (see CONTRIBUTING.md, "The one external dependency").
# Indirect requirements are unaffected — those come along with gjson and are
# not a choice anyone made.
set -euo pipefail

allowed="github.com/tidwall/gjson"
gomod="${1:-go.mod}"

if [[ ! -f "$gomod" ]]; then
  echo "deps-gate: cannot find $gomod" >&2
  exit 1
fi

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
  if [[ "$module" != "$allowed" ]]; then
    offenders+=("$module")
  fi
done < "$gomod"

if [[ ${#offenders[@]} -gt 0 ]]; then
  echo "deps-gate: direct dependency other than ${allowed} found in ${gomod}:" >&2
  for o in "${offenders[@]}"; do
    echo "  - $o" >&2
  done
  exit 1
fi

echo "deps-gate: ${gomod} names no direct dependency other than ${allowed}"
