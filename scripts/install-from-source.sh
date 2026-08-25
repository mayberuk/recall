#!/usr/bin/env bash
set -euo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo="${here}"
bin_dir="${HOME}/.local/bin"
target="${bin_dir}/recall"

usage() {
  cat <<'EOF'
usage: install.sh [--force] [--help]

builds recall from this checkout (CGO_ENABLED=0, ./cmd/recall) and installs
it to ~/.local/bin/recall, stamped with a version derived from git describe.

flags:
  --force   reinstall even if the built version matches what is installed
  --help    show this message
EOF
}

force=0
for arg in "$@"; do
  case "$arg" in
    --help|-h) usage; exit 0 ;;
    --force) force=1 ;;
    *)
      echo "install.sh: unknown flag '${arg}'" >&2
      usage >&2
      exit 2
      ;;
  esac
done

version_stamp() {
  if ! git -C "$repo" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "dev"
    return
  fi
  local describe date
  describe="$(git -C "$repo" describe --tags --always --dirty 2>/dev/null || true)"
  if [[ -z "$describe" ]]; then
    describe="$(git -C "$repo" rev-parse --short HEAD 2>/dev/null || true)"
  fi
  if [[ -z "$describe" ]]; then
    echo "dev"
    return
  fi
  date="$(git -C "$repo" log -1 --format=%cd --date=format:%Y-%m-%d 2>/dev/null || true)"
  if [[ -n "$date" ]]; then
    echo "${describe}+${date}"
  else
    echo "$describe"
  fi
}

# Installing a binary is not permission to edit another tool's config file, so
# this script never registers recall with anything: it names the two commands
# that do and leaves the choice with whoever ran it.
next_steps() {
  echo "install.sh: to reach recall from an agent, 'recall mcp config <client>' prints the entry and 'recall mcp install <client>' writes it; this script registers it with nothing"
}

installed_version() {
  if [[ -x "$target" ]]; then
    "$target" --version 2>/dev/null | head -n1 | awk '{print $2}'
  fi
}

new_stamp="$(version_stamp)"
old_version="$(installed_version || true)"

echo "install.sh: currently installed at ${target}: ${old_version:-none}"
echo "install.sh: build version for this checkout: ${new_stamp}"

if [[ -n "${old_version}" && "${old_version}" == "${new_stamp}" && "${force}" -eq 0 ]]; then
  echo "install.sh: already current, nothing to do"
  next_steps
  exit 0
fi

if [[ ! -d "$bin_dir" ]]; then
  mkdir -p "$bin_dir"
  echo "install.sh: created ${bin_dir}"
fi

# Same directory as the install target, so the final mv is a same-filesystem
# rename and therefore atomic; a temp file under /tmp would not guarantee that.
build_tmp="$(mktemp "${bin_dir}/.recall.build.XXXXXX")"
cleanup() { rm -f "$build_tmp"; }
trap cleanup EXIT

echo "install.sh: building to ${build_tmp}"
( cd "$repo" && CGO_ENABLED=0 go build -ldflags "-X main.version=${new_stamp}" -o "$build_tmp" ./cmd/recall )
chmod +x "$build_tmp"

echo "install.sh: verifying the built binary"
if ! doctor_out="$("$build_tmp" doctor 2>&1)"; then
  echo "install.sh: doctor check failed, refusing to install"
  echo "$doctor_out"
  exit 1
fi
if ! version_out="$("$build_tmp" --version 2>&1)"; then
  echo "install.sh: --version check failed, refusing to install"
  echo "$version_out"
  exit 1
fi

mv -f "$build_tmp" "$target"
trap - EXIT

if [[ -z "${old_version}" ]]; then
  echo "install.sh: installed recall ${new_stamp} to ${target}"
elif [[ "${old_version}" == "${new_stamp}" ]]; then
  echo "install.sh: reinstalled recall ${new_stamp} to ${target} (--force)"
else
  echo "install.sh: upgraded recall from ${old_version} to ${new_stamp} at ${target}"
fi
next_steps
