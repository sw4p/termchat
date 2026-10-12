#!/bin/sh
# Install termchat: https://github.com/sw4p/termchat
#
#   curl -fsSL https://github.com/sw4p/termchat/releases/latest/download/install.sh | sh
#
# Downloads the latest release for this OS and CPU, verifies its SHA-256
# checksum, and installs the binary to /usr/local/bin (or ~/.local/bin if
# that isn't writable). Settings, all optional:
#
#   TERMCHAT_VERSION      install this version (e.g. 1.2.0) instead of the latest
#   TERMCHAT_INSTALL_DIR  install here instead
#   TERMCHAT_BASE_URL     download from here instead of the GitHub release
#                         (used to test against a local build: file:///…/dist)

set -eu

REPO="sw4p/termchat"

say() { printf 'termchat: %s\n' "$*" >&2; }
die() { say "error: $*"; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "this script needs '$1'"; }
need curl
need tar
need uname

# ─── What to download ────────────────────────────────────────────────────────

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) die "unsupported OS $(uname -s); termchat runs on macOS and Linux" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) die "unsupported CPU $(uname -m); termchat is built for amd64 and arm64" ;;
esac

version="${TERMCHAT_VERSION:-}"
if [ -z "$version" ]; then
  # /releases/latest redirects to /releases/tag/vX.Y.Z; read the tag from it.
  latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") ||
    die "couldn't look up the latest release"
  version="${latest##*/v}"
  case "$version" in
    [0-9]*) ;;
    *) die "couldn't work out the latest version from $latest" ;;
  esac
fi
version="${version#v}"

base="${TERMCHAT_BASE_URL:-https://github.com/$REPO/releases/download/v$version}"
archive="termchat_${version}_${os}_${arch}.tar.gz"

# ─── Download and verify ─────────────────────────────────────────────────────

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "downloading termchat $version for $os/$arch"
curl -fsSL -o "$tmp/$archive" "$base/$archive" || die "download failed: $base/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || die "download failed: $base/checksums.txt"

want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || die "$archive isn't listed in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$archive" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
  got=$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')
else
  die "need sha256sum or shasum to verify the download"
fi
[ "$got" = "$want" ] || die "checksum mismatch for $archive (expected $want, got $got); not installing"
say "checksum OK"

tar -xzf "$tmp/$archive" -C "$tmp" termchat || die "couldn't unpack $archive"

# ─── Install ─────────────────────────────────────────────────────────────────

dir="${TERMCHAT_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
  if [ -w /usr/local/bin ]; then
    dir=/usr/local/bin
  else
    dir="$HOME/.local/bin"
  fi
fi
mkdir -p "$dir" || die "couldn't create $dir"

# Write to a temporary name and rename, so a running termchat is never
# half-overwritten.
if ! { cp "$tmp/termchat" "$dir/.termchat.new" && chmod 755 "$dir/.termchat.new" && mv -f "$dir/.termchat.new" "$dir/termchat"; }; then
  rm -f "$dir/.termchat.new"
  die "couldn't install to $dir (try TERMCHAT_INSTALL_DIR=… or run with sudo)"
fi

say "installed $dir/termchat"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) say "note: $dir isn't on your PATH; add it, e.g.  export PATH=\"$dir:\$PATH\"" ;;
esac
"$dir/termchat" --version >/dev/null 2>&1 || say "warning: the installed binary didn't run"
