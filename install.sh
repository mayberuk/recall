#!/bin/sh
# Installs recall from its GitHub releases.
#
#   curl -fsSL https://raw.githubusercontent.com/mayberuk/recall/main/install.sh | sh
#
# Downloads the binary for this machine, checks it against the published
# sha256, and puts it in ~/.local/bin. Registers recall with nothing: reaching
# it from a coding agent is `recall mcp install <client>`, which is a separate
# and deliberate step.
#
# Honours RECALL_VERSION (default: the latest release) and RECALL_INSTALL_DIR
# (default: ~/.local/bin). Building from a checkout instead is
# scripts/install-from-source.sh.
set -eu

repo=mayberuk/recall
install_dir=${RECALL_INSTALL_DIR:-$HOME/.local/bin}

say()  { printf 'install: %s\n' "$*"; }
die()  { printf 'install: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# fetch writes a URL to a path, through whichever of curl or wget is present.
# Both are told to fail loudly on an HTTP error: the default for each is to
# write the error page to the file and exit 0, which would install a 404.
fetch() {
  if have curl; then
    curl -fsSL "$1" -o "$2"
  elif have wget; then
    wget -qO "$2" "$1"
  else
    die "neither curl nor wget is installed"
  fi
}

case "$(uname -s)" in
  Linux)  os=linux ;;
  Darwin) os=darwin ;;
  *)      die "unsupported OS $(uname -s); Windows builds are attached to the release at https://github.com/${repo}/releases" ;;
esac

case "$(uname -m)" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *)             die "unsupported architecture $(uname -m); recall publishes amd64 and arm64" ;;
esac

version=${RECALL_VERSION:-}
if [ -z "$version" ]; then
  say "resolving the latest release"
  tmp_meta=$(mktemp)
  fetch "https://api.github.com/repos/${repo}/releases/latest" "$tmp_meta" \
    || die "cannot reach the GitHub releases API"
  version=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$tmp_meta" | head -n 1)
  rm -f "$tmp_meta"
  [ -n "$version" ] || die "could not read a tag_name from the releases API"
fi

asset="recall-${version}-${os}-${arch}"
base="https://github.com/${repo}/releases/download/${version}"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT INT TERM

say "downloading ${asset}"
fetch "${base}/${asset}" "${work}/recall" || die "no such asset: ${base}/${asset}"

# The checksum is the reason this script is safe to pipe into a shell: without
# it, anything that can answer for the release host decides what gets run.
say "verifying the checksum"
fetch "${base}/checksums.txt" "${work}/checksums.txt" || die "the release publishes no checksums.txt"
want=$(awk -v a="$asset" '$2 == a || $2 == "*" a {print $1}' "${work}/checksums.txt" | head -n 1)
[ -n "$want" ] || die "checksums.txt does not list ${asset}"

if have sha256sum; then
  got=$(sha256sum "${work}/recall" | cut -d' ' -f1)
elif have shasum; then
  got=$(shasum -a 256 "${work}/recall" | cut -d' ' -f1)
else
  die "neither sha256sum nor shasum is installed, so the download cannot be verified"
fi
[ "$got" = "$want" ] || die "checksum mismatch for ${asset}: got ${got}, expected ${want}"

chmod +x "${work}/recall"
"${work}/recall" --version >/dev/null 2>&1 || die "the downloaded binary does not run on this machine"

mkdir -p "$install_dir"
mv -f "${work}/recall" "${install_dir}/recall"
say "installed $("${install_dir}/recall" --version) to ${install_dir}/recall"

case ":${PATH}:" in
  *":${install_dir}:"*) ;;
  *) say "note: ${install_dir} is not on your PATH; add it to your shell profile" ;;
esac

cat <<'NEXT'

  recall guide                    which command answers which question
  recall find <query>             which past session talked about something
  recall mcp install claude-code  reach it from a coding agent instead

NEXT
