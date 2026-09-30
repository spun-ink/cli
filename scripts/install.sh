#!/bin/sh
# Installs spun, the spun.ink command line, from its GitHub releases:
#
#   curl -fsSL https://raw.githubusercontent.com/spun-ink/cli/main/scripts/install.sh | sh
#
# SPUN_VERSION picks a release (default: the latest), SPUN_INSTALL_DIR the directory
# (default: ~/.local/bin). The archive is checked against the release's checksums.txt before
# anything is installed. Never asks for sudo.
set -eu

repo="https://github.com/spun-ink/cli"
install_dir="${SPUN_INSTALL_DIR:-$HOME/.local/bin}"

fail() {
  echo "spun install: $*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

need curl
need tar
need uname

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "unsupported system $(uname -s) — on Windows use install.ps1" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported architecture $(uname -m)" ;;
esac

if [ -n "${SPUN_VERSION:-}" ]; then
  tag="v${SPUN_VERSION#v}"
else
  latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$repo/releases/latest") ||
    fail "could not reach $repo"
  tag="${latest##*/}"
  case "$tag" in v*) ;; *) fail "no release found at $repo/releases" ;; esac
fi
version="${tag#v}"

archive="spun_${version}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading spun $version for $os/$arch"
curl -fsSL -o "$tmp/$archive" "$repo/releases/download/$tag/$archive" ||
  fail "no $archive in release $tag"
curl -fsSL -o "$tmp/checksums.txt" "$repo/releases/download/$tag/checksums.txt" ||
  fail "no checksums.txt in release $tag"

expected=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$expected" ] || fail "$archive is not listed in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$archive" | awk '{ print $1 }')
else
  need shasum
  actual=$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')
fi
[ "$expected" = "$actual" ] || fail "checksum mismatch for $archive — nothing was installed"

tar -xzf "$tmp/$archive" -C "$tmp" spun
mkdir -p "$install_dir"
mv "$tmp/spun" "$install_dir/spun"
chmod 755 "$install_dir/spun"

echo "Installed $("$install_dir/spun" --version) to $install_dir/spun"
case ":$PATH:" in
  *":$install_dir:"*) ;;
  *) echo "Add $install_dir to your PATH, e.g.: export PATH=\"$install_dir:\$PATH\"" ;;
esac
echo "Next: spun signup (a new account) or spun login (an existing one)."
