#!/usr/bin/env bash
# Install the pinned Linux binaries the fence image and CI's Linux gate jobs both carry, and Herdr
# on macOS for CI's macOS journey job.
#   infra/fence/pinned-tools.sh <prefix> <tool>...   tools: shellcheck rumdl node herdr
# Binaries land in <prefix>/bin (node's whole tree under <prefix>), each archive verified against
# its recorded sha256 before it is unpacked. The pfm-dev image runs it with prefix /usr/local;
# .github/workflows/ci.yml runs it into the runner's tool directory, so both sides lint and test
# with byte-identical tools.
# Runs alone (an image build copies it without the tree), so it reads the OS itself: Bash 3.2 and BSD
# tools on macOS.
# BROKEN STATE: an unknown tool, a tool not pinned for this OS or architecture, a failed download, a checksum
# mismatch or a binary that does not answer --version = a named line and a non-zero exit.
set -euo pipefail

# shellcheck is the linter the plugins' scripts/gate.sh runs over their shell scripts, pinned to
# the release those gates pin (gpt-gateway SHELLCHECK_VERSION): apt's older release reports
# different findings.
SHELLCHECK_VERSION=0.11.0
# rumdl is the Markdown linter .claude/scripts/test.sh runs on every named .md file, pinned to the
# release `pfm install` installs on a host (pfm/internal/installer/markdown_tool.go
# rumdlPinnedVersion): bump both together. The static musl build.
RUMDL_VERSION=0.2.73
# Node pinned to the mirror compilers' minimum supported release.
NODE_VERSION=22.13.1
HERDR_VERSION=0.9.5

(( $# >= 2 )) || { echo "usage: pinned-tools.sh <prefix> <shellcheck|rumdl|node|herdr>..." >&2; exit 2; }
prefix="$1"; shift
os="$(uname -s)"
case "$os" in
  Linux) herdr_os=linux ;;
  Darwin) herdr_os=macos ;;
  *) echo "pinned-tools: BROKEN — unsupported OS $os" >&2; exit 2 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=x86_64; node_arch=x64 ;;
  aarch64 | arm64) arch=aarch64; node_arch=arm64 ;;
  *) echo "pinned-tools: BROKEN — unsupported architecture $(uname -m)" >&2; exit 2 ;;
esac
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$prefix/bin"

sha256() { # sha256 <file>: its digest; macOS has shasum where Linux has sha256sum
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1
}

fetch() { # fetch <url> <sha256> <file>
  curl -fsSL -o "$3" "$1" || { echo "pinned-tools: FAILED — download $1" >&2; exit 1; }
  [[ "$(sha256 "$3")" == "$2" ]] || { echo "pinned-tools: BROKEN — checksum mismatch for $1" >&2; exit 1; }
}

for tool in "$@"; do
  case "$tool:$os:$arch" in
    shellcheck:Linux:x86_64) sum=8c3be12b05d5c177a04c29e3c78ce89ac86f1595681cab149b65b97c4e227198 ;;
    shellcheck:Linux:aarch64) sum=12b331c1d2db6b9eb13cfca64306b1b157a86eb69db83023e261eaa7e7c14588 ;;
    rumdl:Linux:x86_64) sum=dc4aa9bf98f936f667039224a34fc2e26ddf6be3589bf59d63e4d6b038be4f5c ;;
    rumdl:Linux:aarch64) sum=f51548adfc71c4982cb5cc239d847bcd4c884e6ac8dc67efd68efd3eea9753a4 ;;
    herdr:Linux:x86_64) sum=14d9e8bc8b4a9e0118d77c32266f76d461f09b40342eb23752960bd89f59929d ;;
    herdr:Linux:aarch64) sum=17d7536f2da50859523680ef279dffcac8e4a235f4e46fe8da6c2ec44141897c ;;
    herdr:Darwin:x86_64) sum=5fb0a908ec7f6745e3e245738b06bfad56c46391c391ab3c0955fbead01f1369 ;;
    herdr:Darwin:aarch64) sum=fb77c2232a05a8035421382c894392f4174e4e38a18e48af3dd0290157c3a7e2 ;;
    node:Linux:*) sum='' ;; # verified against the release's SHASUMS256.txt below
    shellcheck:* | rumdl:* | node:* | herdr:*) echo "pinned-tools: BROKEN — $tool is not pinned for $os $arch" >&2; exit 2 ;;
    *) echo "pinned-tools: BROKEN — unknown tool $tool" >&2; exit 2 ;;
  esac
  case "$tool" in
    shellcheck)
      fetch "https://github.com/koalaman/shellcheck/releases/download/v${SHELLCHECK_VERSION}/shellcheck-v${SHELLCHECK_VERSION}.linux.${arch}.tar.xz" "$sum" "$work/shellcheck.tar.xz"
      tar -C "$work" -xJf "$work/shellcheck.tar.xz"
      install -m 0755 "$work/shellcheck-v${SHELLCHECK_VERSION}/shellcheck" "$prefix/bin/shellcheck"
      version="$({ "$prefix/bin/shellcheck" --version | grep -F "$SHELLCHECK_VERSION"; } 2>/dev/null || true)" ;;
    rumdl)
      fetch "https://github.com/rvben/rumdl/releases/download/v${RUMDL_VERSION}/rumdl-v${RUMDL_VERSION}-${arch}-unknown-linux-musl.tar.gz" "$sum" "$work/rumdl.tar.gz"
      mkdir "$work/rumdl" && tar -C "$work/rumdl" -xzf "$work/rumdl.tar.gz"
      install -m 0755 "$(find "$work/rumdl" -type f -name rumdl | head -n 1)" "$prefix/bin/rumdl"
      version="$({ "$prefix/bin/rumdl" --version | grep -F "$RUMDL_VERSION"; } 2>/dev/null || true)" ;;
    node)
      tarball="node-v${NODE_VERSION}-linux-${node_arch}.tar.xz"
      curl -fsSL -o "$work/SHASUMS256.txt" "https://nodejs.org/dist/v${NODE_VERSION}/SHASUMS256.txt" \
        || { echo "pinned-tools: FAILED — download node SHASUMS256.txt" >&2; exit 1; }
      sum="$(awk -v f="$tarball" '$2 == f {print $1}' "$work/SHASUMS256.txt")"
      [[ -n "$sum" ]] || { echo "pinned-tools: BROKEN — $tarball not in SHASUMS256.txt" >&2; exit 1; }
      fetch "https://nodejs.org/dist/v${NODE_VERSION}/${tarball}" "$sum" "$work/$tarball"
      tar -C "$prefix" --strip-components=1 -xJf "$work/$tarball"
      version="$({ "$prefix/bin/node" --version | grep -F "v$NODE_VERSION"; } 2>/dev/null || true)" ;;
    herdr)
      fetch "https://github.com/herdrdev/herdr/releases/download/v${HERDR_VERSION}/herdr-${herdr_os}-${arch}" "$sum" "$work/herdr"
      install -m 0755 "$work/herdr" "$prefix/bin/herdr"
      version="$({ "$prefix/bin/herdr" --version | grep -F "$HERDR_VERSION"; } 2>/dev/null || true)" ;;
  esac
  [[ -n "$version" ]] || { echo "pinned-tools: BROKEN — $prefix/bin/$tool does not report its pinned version" >&2; exit 1; }
  printf 'TOOL %-10s %s\n' "$tool" "$version"
done
