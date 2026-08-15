#!/usr/bin/env bash
# Runs every acceptance case and writes raw evidence to logs/acceptance/<case>/.
# It rules nothing. A fresh judge agent reads that directory and returns PASS / FAIL / BLOCKED.
set -euo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd -- "${here}/../.." && pwd)"

cd -- "${repo}"
exec go run ./tests/acceptance/runner "$@"
