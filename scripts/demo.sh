#!/usr/bin/env bash
# Builds recall, writes the demo corpus, and runs every command the README
# quotes against it. The output is what the README shows: run this and the
# blocks there should match, modulo the wall-clock figures in the footer.
set -euo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/recall-demo.XXXXXX")"
trap 'rm -rf "$work"' EXIT

CGO_ENABLED=0 go build -o "$work/recall" ./cmd/recall
projects="$(go run ./scripts/demo "$work/home")"

export CLAUDE_PROJECTS_DIR="$projects"
export RECALL_HOME="$work/archive"
export RECALL_NO_STATS="${RECALL_NO_STATS:-}"
unset CLAUDE_SESSION_ID CLAUDECODE CODEX_THREAD_ID CODEX_SESSION_ID RECALL_AGENT 2>/dev/null || true

run() {
  printf '\n$ recall %s\n' "$*"
  ( cd "$work/home/src/payments" && "$work/recall" "$@" ) || true
}

run guide
run find idempotency
run turns "connection pool" --limit 1
run when "rate limit"
run show 6e2b8d15 --turn 6e2b8d15-0001-4000-8000-000000000001 --around 1
run doctor
