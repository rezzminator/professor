#!/bin/sh
# install.sh — installs a released pfm binary, checksum-verified, into ~/.local/bin.
#
#   curl -fsSL https://raw.githubusercontent.com/rezzminator/professor/main/scripts/install.sh | sh
#
# It downloads pfm_{tag}_{os}_{arch} and SHA256SUMS from the release, compares
# the binary's SHA-256 with its SHA256SUMS row and installs it mode 0755. It
# writes nothing else: `pfm install` (a read-only preview) is the user's next step.
#
#   PFM_VERSION           the release tag to install (default: the latest release)
#   PFM_INSTALL_DIR       where pfm lands (default: $HOME/.local/bin)
#   PFM_INSTALL_BASE_URL  the release download root holding {tag}/{asset}
#                         (default: the GitHub release downloads; a mirror or a test sets it)
#
# Piped from curl, it runs with no checkout beside it and can source nothing:
# the OS and architecture names repeat scripts/platform.sh's by hand.
#
# BROKEN STATE: every failure ends `pfm-install: FAIL {step}: {cause}` on stderr
# with exit 1 and leaves any pfm already installed untouched; success ends
# `pfm-install: installed pfm {tag} at {path}`.
set -eu

REPO=rezzminator/professor

fail() {
  echo "pfm-install: FAIL $*" >&2
  exit 1
}

# main runs only once the whole script has arrived, so a cut download executes nothing.
main() {
  for tool in curl uname awk mktemp install; do
    command -v "$tool" >/dev/null 2>&1 || fail "setup: $tool is not on PATH"
  done

  case $(uname -s) in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) fail "platform: $(uname -s) is not supported (linux, darwin)" ;;
  esac
  case $(uname -m) in
    arm64 | aarch64) arch=arm64 ;;
    x86_64 | amd64) arch=amd64 ;;
    *) fail "platform: $(uname -m) is not supported (amd64, arm64)" ;;
  esac

  tag=${PFM_VERSION:-}
  if [ -z "$tag" ]; then
    latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") ||
      fail "version: cannot reach https://github.com/$REPO/releases/latest"
    tag=${latest##*/}
  fi
  case $tag in
    v[0-9]*) ;;
    *) fail "version: '$tag' is not a release tag (v{X.Y.Z})" ;;
  esac

  base=${PFM_INSTALL_BASE_URL:-https://github.com/$REPO/releases/download}
  dir=${PFM_INSTALL_DIR:-$HOME/.local/bin}
  asset="pfm_${tag}_${os}_${arch}"

  tmp=$(mktemp -d "${TMPDIR:-/tmp}/pfm-install.XXXXXX") || fail "setup: mktemp failed"
  trap 'rm -rf -- "$tmp"' EXIT

  curl -fsSL -o "$tmp/$asset" "$base/$tag/$asset" || fail "download: $base/$tag/$asset"
  curl -fsSL -o "$tmp/SHA256SUMS" "$base/$tag/SHA256SUMS" || fail "download: $base/$tag/SHA256SUMS"

  want=$(awk -v file="$asset" '$2 == file { print $1 }' "$tmp/SHA256SUMS")
  [ -n "$want" ] || fail "checksum: SHA256SUMS has no row for $asset"
  if command -v sha256sum >/dev/null 2>&1; then
    got=$(sha256sum "$tmp/$asset" | awk '{ print $1 }')
  elif command -v shasum >/dev/null 2>&1; then
    got=$(shasum -a 256 "$tmp/$asset" | awk '{ print $1 }')
  else
    fail "checksum: neither sha256sum nor shasum is on PATH"
  fi
  [ "$got" = "$want" ] || fail "checksum: $asset is $got, SHA256SUMS says $want"

  mkdir -p "$dir" || fail "install: cannot create $dir"
  install -m 0755 "$tmp/$asset" "$dir/pfm" || fail "install: cannot write $dir/pfm"

  case ":$PATH:" in
    *":$dir:"*) ;;
    *) echo "pfm-install: $dir is not on PATH; add it: export PATH=\"$dir:\$PATH\"" ;;
  esac
  echo "pfm-install: next, preview what pfm sets up: pfm install"
  echo "pfm-install: installed pfm $tag at $dir/pfm"
}

main "$@"
