#!/usr/bin/env bash
# Self-test for scripts/install.sh. The release is a fixture directory served
# over file:// through PFM_INSTALL_BASE_URL, the installer's own seam; the OS
# and architecture are faked through PATH, with a stub uname ahead of the real
# tools. BROKEN STATE: a missing real sha256sum or curl on this host is an
# error (no case can run), never a skip.
set -uo pipefail
umask 002
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SHTEST_TAG=install-test
source "$ROOT/scripts/shtest.sh"
INSTALLER="$ROOT/scripts/install.sh"

command -v sha256sum >/dev/null || { echo "test-install: sha256sum absent on this host" >&2; exit 2; }
command -v curl >/dev/null || { echo "test-install: curl absent on this host" >&2; exit 2; }

TAG=v9.9.9
REL="$T/rel/$TAG"
mkdir -p "$REL"
for asset in linux_amd64 linux_arm64 darwin_arm64; do
  printf '#!/bin/sh\necho fixture pfm %s\n' "$asset" > "$REL/pfm_${TAG}_${asset}"
done
(cd "$REL" && sha256sum pfm_${TAG}_linux_amd64 pfm_${TAG}_darwin_arm64 > SHA256SUMS) # linux_arm64 has no row

uname_stub() { # uname_stub KERNEL MACHINE: a PATH whose uname answers -s and -m with them
  local dir="$T/uname-$1-$2"
  mkdir -p "$dir"
  printf '#!/bin/sh\ncase $1 in -s) echo %s ;; -m) echo %s ;; esac\n' "$1" "$2" > "$dir/uname"
  chmod 755 "$dir/uname"
  printf '%s\n' "$dir"
}

run_installer() { # run_installer KERNEL MACHINE DEST [VAR=value...]: the installer's rc in RC, stdout and stderr in $T/out, $T/err
  local stub dest=$3
  stub=$(uname_stub "$1" "$2")
  shift 3
  env PATH="$stub:$dest:$PATH" PFM_VERSION="$TAG" PFM_INSTALL_BASE_URL="file://$T/rel" PFM_INSTALL_DIR="$dest" "$@" \
    sh "$INSTALLER" > "$T/out" 2> "$T/err"
  RC=$?
}

expect() { # expect LABEL WANT_RC WANT_OUT WANT_ERR: exact rc, stdout and stderr of the last run
  local label=$1 want_rc=$2 want_out=$3 want_err=$4 got_out got_err
  got_out=$(cat "$T/out"); got_err=$(/usr/bin/grep '^pfm-install:' "$T/err") # curl's own error line sits above the installer's
  if [ "$RC" = "$want_rc" ] && [ "$got_out" = "$want_out" ] && [ "$got_err" = "$want_err" ]; then ok "$label"
  else bad "$label" "want rc=$want_rc out=[$want_out] err=[$want_err]" "got  rc=$RC out=[$got_out] err=[$got_err] (artifacts $T/out $T/err)"; fi
}

expect_binary() { # expect_binary LABEL DEST WANT_CONTENT|absent: the installed file's exact bytes and mode, or its absence
  local label=$1 file=$2/pfm want=$3 got mode
  if [ "$want" = absent ]; then
    if [ -e "$file" ]; then bad "$label" "want no $file" "got one"; else ok "$label"; fi
    return
  fi
  got=$(cat "$file" 2>&1); mode=$(stat -c %a "$file" 2>&1)
  if [ "$got" = "$want" ] && [ "$mode" = 755 ]; then ok "$label"
  else bad "$label" "want mode=755 content=[$want]" "got  mode=$mode content=[$got] ($file)"; fi
}

run_installer Linux x86_64 "$T/bin-linux"
expect 'linux x86_64: installs and names the next step' 0 \
  "pfm-install: next, preview what pfm sets up: pfm install
pfm-install: installed pfm $TAG at $T/bin-linux/pfm" ''
expect_binary 'linux x86_64: the linux_amd64 asset lands mode 755' "$T/bin-linux" '#!/bin/sh
echo fixture pfm linux_amd64'

run_installer Darwin arm64 "$T/bin-darwin"
expect 'darwin arm64: installs' 0 \
  "pfm-install: next, preview what pfm sets up: pfm install
pfm-install: installed pfm $TAG at $T/bin-darwin/pfm" ''
expect_binary 'darwin arm64: the darwin_arm64 asset lands mode 755' "$T/bin-darwin" '#!/bin/sh
echo fixture pfm darwin_arm64'

# A destination off PATH is named with the line that adds it.
stub=$(uname_stub Linux amd64)
env PATH="$stub:$PATH" PFM_VERSION="$TAG" PFM_INSTALL_BASE_URL="file://$T/rel" PFM_INSTALL_DIR="$T/bin-offpath" \
  sh "$INSTALLER" > "$T/out" 2> "$T/err"
RC=$?
expect 'a destination off PATH: the export line is printed' 0 \
  "pfm-install: $T/bin-offpath is not on PATH; add it: export PATH=\"$T/bin-offpath:\$PATH\"
pfm-install: next, preview what pfm sets up: pfm install
pfm-install: installed pfm $TAG at $T/bin-offpath/pfm" ''

run_installer Linux aarch64 "$T/bin-norow"
expect 'an asset with no SHA256SUMS row is refused' 1 '' \
  "pfm-install: FAIL checksum: SHA256SUMS has no row for pfm_${TAG}_linux_arm64"
expect_binary 'an asset with no SHA256SUMS row: nothing installed' "$T/bin-norow" absent

# A corrupted download: the asset's bytes change after SHA256SUMS was written.
mkdir -p "$T/bin-corrupt"
printf 'the pfm already installed\n' > "$T/bin-corrupt/pfm"; chmod 755 "$T/bin-corrupt/pfm"
good=$(awk -v f="pfm_${TAG}_darwin_arm64" '$2 == f { print $1 }' "$REL/SHA256SUMS")
printf 'tampered\n' > "$REL/pfm_${TAG}_darwin_arm64"
tampered=$(sha256sum "$REL/pfm_${TAG}_darwin_arm64" | awk '{ print $1 }')
run_installer Darwin arm64 "$T/bin-corrupt"
expect 'a checksum mismatch is refused with both sums' 1 '' \
  "pfm-install: FAIL checksum: pfm_${TAG}_darwin_arm64 is $tampered, SHA256SUMS says $good"
expect_binary 'a checksum mismatch: the pfm already installed is untouched' "$T/bin-corrupt" 'the pfm already installed'

run_installer Linux x86_64 "$T/bin-missing" PFM_VERSION=v0.0.1
expect 'a release without the asset is refused' 1 '' \
  "pfm-install: FAIL download: file://$T/rel/v0.0.1/pfm_v0.0.1_linux_amd64"
expect_binary 'a release without the asset: nothing installed' "$T/bin-missing" absent

run_installer Linux x86_64 "$T/bin-badtag" PFM_VERSION=latest
expect 'a version that is no release tag is refused' 1 '' \
  "pfm-install: FAIL version: 'latest' is not a release tag (v{X.Y.Z})"

run_installer FreeBSD x86_64 "$T/bin-bsd"
expect 'an unsupported OS is refused' 1 '' \
  'pfm-install: FAIL platform: FreeBSD is not supported (linux, darwin)'

run_installer Linux riscv64 "$T/bin-riscv"
expect 'an unsupported architecture is refused' 1 '' \
  'pfm-install: FAIL platform: riscv64 is not supported (amd64, arm64)'
expect_binary 'an unsupported architecture: nothing installed' "$T/bin-riscv" absent

shtest_end
