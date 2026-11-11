#!/usr/bin/env bash
# Self-test for scripts/platform.sh. The OS is faked through the seam the door
# itself uses, PATH: every case runs the door under /bin/sh with a PATH holding
# only that case's tool stubs — GNU tools (the real ones, linked in), BSD-shaped
# stubs, or nothing — so each function's Linux branch, macOS branch and
# no-tool branch each run on this host. BROKEN STATE: a missing real
# sha256sum/stat on this host is an error (the GNU branch cannot run), never a skip.
set -uo pipefail
umask 002
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SHTEST_TAG=platform-test
source "$ROOT/scripts/shtest.sh"
DOOR="$ROOT/scripts/platform.sh"

REAL_SHA256=$(command -v sha256sum) || { echo "test-platform: sha256sum absent on this host" >&2; exit 2; }
REAL_STAT=$(command -v stat) || { echo "test-platform: stat absent on this host" >&2; exit 2; }

stubs() { # stubs NAME [tool=body | tool=@realpath]...: a fresh PATH dir holding only those tools
  local dir="$T/path-$1" spec
  shift
  rm -rf "$dir"; mkdir -p "$dir"
  for spec in "$@"; do
    case "${spec#*=}" in
      @*) ln -s "${spec#*=@}" "$dir/${spec%%=*}" ;;
      *) printf '#!/bin/sh\n%s\n' "${spec#*=}" > "$dir/${spec%%=*}"; chmod 755 "$dir/${spec%%=*}" ;;
    esac
  done
  printf '%s\n' "$dir"
}

door() { # door PATHDIR function [arg...]: run one door function under /bin/sh with only PATHDIR on PATH
  local dir=$1
  shift
  RC=0
  env -i PATH="$dir" /bin/sh -c '. "$1" && shift && "$@"' sh "$DOOR" "$@" > "$T/out" 2> "$T/err" || RC=$?
}

expect() { # expect LABEL WANT_RC WANT_OUT WANT_ERR: exact stdout, stderr and rc of the last door call
  local label=$1 want_rc=$2 want_out=$3 want_err=$4 got_out got_err
  got_out=$(cat "$T/out"); got_err=$(sed 's/^sh: [0-9]*: //' "$T/err") # dash prefixes a missing tool with its line number
  if [ "$RC" = "$want_rc" ] && [ "$got_out" = "$want_out" ] && [ "$got_err" = "$want_err" ]; then ok "$label"
  else bad "$label" "want rc=$want_rc out=[$want_out] err=[$want_err]" "got  rc=$RC out=[$got_out] err=[$got_err] (artifacts $T/out $T/err)"; fi
}

printf 'abc\n' > "$T/abc.txt"
chmod 600 "$T/abc.txt"
touch -d '2026-11-02 12:00:00 UTC' "$T/abc.txt"
SHA='edeaaff3f1774ad2888673770c6d64097e391bc362d7d6fb34982ddf0efd18cb'
BSD_SHASUM='[ "$1 $2" = "-a 256" ] || { echo "shasum: unexpected arguments: $*" >&2; exit 64; }; shift 2; exec '"$REAL_SHA256"' "$@"'

# ---- sourcing defines functions and nothing else -----------------------------
RC=0
env -i /bin/sh -c 'set > "$2"; . "$1"; set > "$3"; cmp -s "$2" "$3" && echo clean' sh "$DOOR" "$T/set-before" "$T/set-after" > "$T/out" 2> "$T/err" || RC=$?
expect 'sourcing leaves variables, options and output untouched' 0 clean ''

# ---- platform_sha256 ---------------------------------------------------------
door "$(stubs gnu-sha sha256sum=@"$REAL_SHA256")" platform_sha256 "$T/abc.txt"
expect 'sha256 with sha256sum prints the sha256sum line' 0 "$SHA  $T/abc.txt" ''
door "$(stubs bsd-sha shasum="$BSD_SHASUM")" platform_sha256 "$T/abc.txt"
expect 'sha256 with only shasum prints the same line (shasum -a 256)' 0 "$SHA  $T/abc.txt" ''
door "$(stubs both-sha sha256sum='echo gnu' shasum='echo bsd')" platform_sha256 "$T/abc.txt"
expect 'sha256 prefers sha256sum when both exist' 0 gnu ''
door "$(stubs no-sha)" platform_sha256 "$T/abc.txt"
expect 'sha256 with neither tool is 127 and silent' 127 '' ''
door "$(stubs fail-sha sha256sum='echo "sha256sum: boom" >&2; exit 3')" platform_sha256 "$T/abc.txt"
expect 'sha256 passes a failing tool its own stderr and rc through' 3 '' 'sha256sum: boom'
door "$(stubs tools-sha)" platform_sha256_tools nor
expect 'sha256 tools are named for a caller missing both' 0 'sha256sum nor shasum' ''
door "$(stubs tools-sha-or)" platform_sha256_tools or
expect 'sha256 tools take the caller own join word' 0 'sha256sum or shasum' ''

# ---- platform_mtime ----------------------------------------------------------
door "$(stubs gnu-stat stat=@"$REAL_STAT")" platform_mtime "$T/abc.txt"
expect 'mtime with GNU stat prints epoch seconds' 0 1793620800 ''
BSD_STAT='case "$1" in -c) echo "stat: illegal option -- c" >&2; exit 1 ;; esac
[ "$1 $2" = "-f %m" ] || { echo "stat: unexpected arguments: $*" >&2; exit 64; }
case "$4" in */missing) echo "stat: $4: stat: No such file or directory" >&2; exit 1 ;; esac
echo 1793620800'
door "$(stubs bsd-stat stat="$BSD_STAT")" platform_mtime "$T/abc.txt"
expect 'mtime with BSD stat prints epoch seconds, GNU attempt silent' 0 1793620800 ''
door "$(stubs bsd-stat stat="$BSD_STAT")" platform_mtime "$T/missing"
expect 'mtime failure on BSD returns nonzero with the BSD attempt stderr' 1 '' "stat: $T/missing: stat: No such file or directory"
door "$(stubs gnu-stat stat=@"$REAL_STAT")" platform_mtime "$T/missing"
expect 'mtime failure on GNU stat is nonzero with no stdout' 1 '' "stat: cannot read file system information for '%m': No such file or directory
stat: cannot read file system information for '$T/missing': No such file or directory"
door "$(stubs no-stat)" platform_mtime "$T/abc.txt"
expect 'mtime with no stat is 127' 127 '' 'stat: not found'

# ---- platform_owner_mode -----------------------------------------------------
door "$(stubs gnu-stat stat=@"$REAL_STAT")" platform_owner_mode "$T/abc.txt"
expect 'owner_mode with GNU stat prints uid and octal mode' 0 "$(id -u) 600" ''
BSD_OWNER='case "$1" in -c) echo "stat: illegal option -- c" >&2; exit 1 ;; esac
[ "$1 $2" = "-f %u %Lp" ] || { echo "stat: unexpected arguments: $*" >&2; exit 64; }
echo "1000 600"'
door "$(stubs bsd-owner stat="$BSD_OWNER")" platform_owner_mode "$T/abc.txt"
expect 'owner_mode with BSD stat prints uid and octal mode' 0 '1000 600' ''
door "$(stubs bsd-owner stat='echo "stat: illegal option" >&2; exit 1')" platform_owner_mode "$T/abc.txt"
expect 'owner_mode failure returns nonzero with the BSD attempt stderr' 1 '' 'stat: illegal option'

# ---- platform_os and platform_arch -------------------------------------------
for row in 'Darwin darwin' 'Linux linux' 'FreeBSD unknown'; do
  set -- $row
  door "$(stubs "uname-s-$1" uname="echo $1")" platform_os
  expect "os maps uname -s $1 to $2" 0 "$2" ''
done
for row in 'arm64 arm64' 'aarch64 arm64' 'x86_64 amd64' 'amd64 amd64' 'i386 unknown'; do
  set -- $row
  door "$(stubs "uname-m-$1" uname="echo $1")" platform_arch
  expect "arch maps uname -m $1 to $2" 0 "$2" ''
done

# ---- platform_cpus -----------------------------------------------------------
door "$(stubs both-cpu nproc='echo 23' getconf='echo 7')" platform_cpus
expect 'cpus prefers nproc' 0 23 ''
door "$(stubs getconf-cpu getconf='[ "$1" = _NPROCESSORS_ONLN ] && echo 7')" platform_cpus
expect 'cpus falls back to getconf _NPROCESSORS_ONLN' 0 7 ''
door "$(stubs no-cpu)" platform_cpus
expect 'cpus with neither tool is rc 1 and silent' 1 '' ''

# ---- platform_in_fence ------------------------------------------------------
# fence MARKER [VAR=value]...: platform_in_fence MARKER under /bin/sh with only those variables set.
fence() {
  local marker=$1
  shift
  RC=0
  env -i PATH="$(stubs no-tools)" "$@" /bin/sh -c '. "$1" && platform_in_fence "$2"' sh "$DOOR" "$marker" \
    > "$T/out" 2> "$T/err" || RC=$?
}
: > "$T/dockerenv"
fence "$T/dockerenv" PFM_DEV_FENCE=1
expect 'in_fence: the fence variable inside a container passes' 0 '' ''
fence "$T/no-dockerenv" PFM_DEV_FENCE=1 PFM_STATE_DB="$T/live.db"
expect 'in_fence: a host shell exporting the fence variable and a store path is refused' 1 '' ''
fence "$T/dockerenv"
expect 'in_fence: a container without the fence variable is refused' 1 '' ''
fence "$T/dockerenv" PFM_DEV_FENCE=true
expect 'in_fence: the fence variable set to another word is refused' 1 '' ''
fence "$T/no-dockerenv" GITHUB_ACTIONS=true RUNNER_ENVIRONMENT=github-hosted
expect 'in_fence: a GitHub-hosted runner passes' 0 '' ''
fence "$T/no-dockerenv" GITHUB_ACTIONS=true RUNNER_ENVIRONMENT=self-hosted
expect 'in_fence: a self-hosted runner is refused' 1 '' ''
fence "$T/no-dockerenv" RUNNER_ENVIRONMENT=github-hosted
expect 'in_fence: a runner environment outside GitHub Actions is refused' 1 '' ''

shtest_end
