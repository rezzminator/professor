#!/usr/bin/env bash
# Self-test for scripts/install-engine-sync.sh: it must build a checkout's
# cmd/engine-sync unstamped, using the binary's reported version for
# {engine-sync home}/versions/{version}/engine-sync and point the stable link
# {engine-sync home}/bin/engine-sync at it, {engine-sync home} resolving
# $ENGINE_SYNC_HOME > $XDG_DATA_HOME/engine-sync > $HOME/.local/share/
# engine-sync; a second version moves the link; and a missing checkout, a
# malformed version, a failed build or a stable path that is the user's own
# file each exit 1 leaving what was installed untouched.
#
# The fixture checkout is a throwaway Go module (never the real engine-sync),
# built for real, so every case runs the script's own go build.
#
# Harness style follows scripts/install-downgrade-guard_test.sh.
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="$ROOT/scripts/install-engine-sync.sh"
SHTEST_TAG=pfm-install-engine-sync-test
# shellcheck source=/dev/null
source "$ROOT/../scripts/shtest.sh"

GOENV=(GOCACHE="$(go env GOCACHE)" GOPATH="$(go env GOPATH)" GOTOOLCHAIN=local GOFLAGS=)

# mkcheckout <dir> <version>: a minimal engine-sync checkout whose binary
# reports its own version for `version`, independent of the manifest.
mkcheckout() {
  local dir=$1
  mkdir -p "$dir/cmd/engine-sync" "$dir/plugins/engine-sync/.claude-plugin"
  printf 'module example.invalid/engine-sync\n\ngo 1.21\n' > "$dir/go.mod"
  setmanifest "$dir" 1.2.0
  setversion "$dir" "$2"
}

setmanifest() {
  printf '{\n  "name": "engine-sync",\n  "version": "%s",\n  "license": "MIT"\n}\n' "$2" \
    > "$1/plugins/engine-sync/.claude-plugin/plugin.json"
}

setversion() {
  setreport "$1" "engine-sync $2"
}

setreport() {
  cat > "$1/cmd/engine-sync/main.go" <<EOF
package main

import (
    "fmt"
    "os"
)

var report = \`$2\`

func main() {
    if len(os.Args) == 2 && os.Args[1] == "version" {
        fmt.Println(report)
        return
    }
    os.Exit(2)
}
EOF
}

# runsut <home> [VAR=value]...: runs the script over $CHECKOUT with only the
# named engine-sync home variables set.
runsut() {
  local home=$1
  shift
  env -u ENGINE_SYNC_HOME -u XDG_DATA_HOME "${GOENV[@]}" HOME="$home" "$@" bash "$SUT" "$CHECKOUT" \
    > "$T/out" 2> "$T/err"
}

CHECKOUT="$T/checkout"
mkcheckout "$CHECKOUT" 1.2.3

# (a) ENGINE_SYNC_HOME wins: the versioned copy, the relative link, the report.
ESH="$T/esh"
if runsut "$T/home-a" ENGINE_SYNC_HOME="$ESH" XDG_DATA_HOME="$T/xdg-a" \
  && [ -x "$ESH/versions/1.2.3/engine-sync" ] && [ ! -L "$ESH/versions/1.2.3/engine-sync" ] \
  && [ "$(readlink "$ESH/bin/engine-sync")" = "../versions/1.2.3/engine-sync" ] \
  && [ "$("$ESH/bin/engine-sync" version)" = "engine-sync 1.2.3" ] \
  && [ ! -e "$T/xdg-a" ] && [ ! -e "$T/home-a/.local/share/engine-sync" ]; then
  ok "ENGINE_SYNC_HOME: built into versions/1.2.3 and linked bin/engine-sync to it"
else
  bad "ENGINE_SYNC_HOME install" "$(cat "$T/out" "$T/err" 2>/dev/null)" "$(ls -lR "$ESH" 2>&1)"
fi

# (b) a second version moves the stable link and keeps the first copy.
setversion "$CHECKOUT" 1.2.4
setmanifest "$CHECKOUT" 1.2.0
if runsut "$T/home-a" ENGINE_SYNC_HOME="$ESH" \
  && [ "$("$ESH/bin/engine-sync" version)" = "engine-sync 1.2.4" ] \
  && [ -x "$ESH/versions/1.2.3/engine-sync" ] \
  && grep -q "(was ../versions/1.2.3/engine-sync)" "$T/out"; then
  ok "a new version moves the stable link and names the one it replaced"
else
  bad "second version" "$(cat "$T/out" "$T/err" 2>/dev/null)"
fi

# (c) XDG_DATA_HOME, then HOME; a relative ENGINE_SYNC_HOME is ignored.
if runsut "$T/home-c" ENGINE_SYNC_HOME=relative XDG_DATA_HOME="$T/xdg-c" \
  && [ "$("$T/xdg-c/engine-sync/bin/engine-sync" version)" = "engine-sync 1.2.4" ] && [ ! -e relative ]; then
  ok "XDG_DATA_HOME/engine-sync when ENGINE_SYNC_HOME is not absolute"
else
  bad "XDG_DATA_HOME install" "$(cat "$T/out" "$T/err" 2>/dev/null)"
fi
if runsut "$T/home-d" XDG_DATA_HOME=relative \
  && [ "$("$T/home-d/.local/share/engine-sync/bin/engine-sync" version)" = "engine-sync 1.2.4" ]; then
  ok "HOME/.local/share/engine-sync when neither override is absolute"
else
  bad "HOME install" "$(cat "$T/out" "$T/err" 2>/dev/null)"
fi

# (d) the user's own file at the stable path stays, and nothing is built.
mkdir -p "$T/esh-d/bin"
printf 'mine\n' > "$T/esh-d/bin/engine-sync"
if ! runsut "$T/home-e" ENGINE_SYNC_HOME="$T/esh-d" && [ "$(cat "$T/esh-d/bin/engine-sync")" = mine ] \
  && [ ! -e "$T/esh-d/versions" ] && grep -q "is not a link" "$T/err"; then
  ok "a stable path that is not a link is refused and left as is"
else
  bad "own file at the stable path" "$(cat "$T/out" "$T/err" 2>/dev/null)"
fi

# (e) a development binary owns its version even when the manifest pins a release.
setversion "$CHECKOUT" 2.0.0-alpha
if runsut "$T/home-e" ENGINE_SYNC_HOME="$T/esh-e" \
  && [ -x "$T/esh-e/versions/2.0.0-alpha/engine-sync" ] \
  && [ "$(readlink "$T/esh-e/bin/engine-sync")" = "../versions/2.0.0-alpha/engine-sync" ] \
  && [ "$("$T/esh-e/bin/engine-sync" version)" = "engine-sync 2.0.0-alpha" ]; then
  ok "the binary's version chooses its install slot when the manifest differs"
else
  bad "binary version differs from manifest" "$(cat "$T/out" "$T/err" 2>/dev/null)"
fi

# Release slots belong only to checksum-verified wrapper downloads.
setversion "$CHECKOUT" 1.2.0
if ! runsut "$T/release-home" ENGINE_SYNC_HOME="$T/release-es" && grep -q 'release slot' "$T/err"; then
  ok "a host build cannot occupy the plugin manifest release slot"
else bad "release slot collision" "$(cat "$T/out" "$T/err")"; fi
setversion "$CHECKOUT" 2.0.0-alpha

# A newer stable link is kept unless the operator explicitly forces a downgrade.
if ! runsut "$T/home-e" ENGINE_SYNC_HOME="$T/esh-e" && grep -q 'older than installed' "$T/err"; then
  bad "equal version must remain installable" "$(cat "$T/err")"
fi
setversion "$CHECKOUT" 1.9.0-alpha
if ! runsut "$T/home-e" ENGINE_SYNC_HOME="$T/esh-e" &&
  [ "$("$T/esh-e/bin/engine-sync" version)" = 'engine-sync 2.0.0-alpha' ] && grep -q 'older than installed' "$T/err"; then
  ok "a newer stable link refuses a backwards move with a named remedy"
else bad "newer stable link" "$(cat "$T/out" "$T/err")"; fi
if runsut "$T/home-e" ENGINE_SYNC_HOME="$T/esh-e" FORCE=1 &&
  [ "$("$T/esh-e/bin/engine-sync" version)" = 'engine-sync 1.9.0-alpha' ] && grep -q FORCED "$T/out"; then
  ok "FORCE=1 names and applies an intentional stable-link downgrade"
else bad "forced backwards move" "$(cat "$T/out" "$T/err")"; fi
setversion "$CHECKOUT" 2.0.0-alpha

# Migration verifies the checksum for the actual platform; unknown files in a slot stay.
mkdir -p "$CHECKOUT/plugins/engine-sync/libexec" "$T/migrate/versions/1.2.0"
printf '#!/bin/sh\necho "engine-sync 1.2.0"\n' > "$T/migrate/versions/1.2.0/engine-sync"
chmod +x "$T/migrate/versions/1.2.0/engine-sync"
case $(uname -s) in Darwin) platform=darwin;; *) platform=linux;; esac
case $(uname -m) in arm64|aarch64) architecture=arm64;; *) architecture=amd64;; esac
asset="engine-sync_1.2.0_${platform}_${architecture}"
checksum=$(sha256sum "$T/migrate/versions/1.2.0/engine-sync" | cut -d ' ' -f1)
printf '%s  %s\n' "$checksum" "$asset" > "$CHECKOUT/plugins/engine-sync/libexec/SHA256SUMS"
cp "$T/migrate/versions/1.2.0/engine-sync" "$T/published"
if runsut "$T/migrate-home" ENGINE_SYNC_HOME="$T/migrate" && cmp -s "$T/published" "$T/migrate/versions/1.2.0/engine-sync"; then
  ok "a genuine checksum-verified wrapper release survives host migration"
else bad "published release migration" "$(cat "$T/out" "$T/err")"; fi
printf '#!/bin/sh\necho "engine-sync 1.2.0"\n# host-built\n' > "$T/migrate/versions/1.2.0/engine-sync"
printf 'operator data\n' > "$T/migrate/versions/1.2.0/personal"
if runsut "$T/migrate-home" ENGINE_SYNC_HOME="$T/migrate" &&
  grep -q 'removed mislabeled host build' "$T/out" && [ "$(cat "$T/migrate/versions/1.2.0/personal")" = 'operator data' ]; then
  ok "a non-release checksum is migrated by name and preserves operator data"
else bad "mislabeled host migration" "$(cat "$T/out" "$T/err")"; fi
# A killed installer leaves an old temporary regular file; migration reports its cleanup.
printf 'unfinished\n' > "$T/migrate/versions/engine-sync.new.stale"
touch -t 200001010000 "$T/migrate/versions/engine-sync.new.stale"
if runsut "$T/migrate-home" ENGINE_SYNC_HOME="$T/migrate" && grep -q 'removed stale build' "$T/out"; then
  ok "a killed install's stale temporary build is retired with a named line"
else bad "stale temporary build" "$(cat "$T/out" "$T/err")"; fi

# An unparseable version report preserves the installed binary and stable link.
cp "$ESH/versions/1.2.4/engine-sync" "$T/installed-before"
for report in '' 'engine-sync .' 'engine-sync ..' 'engine-sync dev' 'engine-sync 1.2.3/path' 'unrelated 1.2.4' 'engine-sync 1.2 bad' $'engine-sync 1.2.4\nextra'; do
  setreport "$CHECKOUT" "$report"
  if ! runsut "$T/home-a" ENGINE_SYNC_HOME="$ESH" \
    && cmp -s "$T/installed-before" "$ESH/versions/1.2.4/engine-sync" \
    && [ "$(readlink "$ESH/bin/engine-sync")" = "../versions/1.2.4/engine-sync" ] \
    && [ -z "$(find "$ESH/versions" -name 'engine-sync.new.*')" ] \
    && grep -q 'no usable version report' "$T/err"; then
    ok "an unparseable version report is refused: '$report'"
  else
    bad "unparseable version report: '$report'" "$(cat "$T/out" "$T/err" 2>/dev/null)"
  fi
done
setversion "$CHECKOUT" 1.2.5

# (f) a failed build leaves the installed version and the link untouched.
printf 'package main\n\nfunc main() { broken }\n' > "$CHECKOUT/cmd/engine-sync/main.go"
if ! runsut "$T/home-a" ENGINE_SYNC_HOME="$ESH" && [ ! -e "$ESH/versions/1.2.5/engine-sync" ] \
  && [ "$("$ESH/bin/engine-sync" version)" = "engine-sync 1.2.4" ] \
  && [ -z "$(find "$ESH/versions" -name 'engine-sync.new.*')" ] \
  && grep -q "build FAILED" "$T/err"; then
  ok "a failed build changes nothing and leaves no partial file"
else
  bad "failed build" "$(cat "$T/out" "$T/err" 2>/dev/null)" "$(ls -l "$ESH/versions/1.2.5" 2>&1)"
fi

# (g) no checkout names the submodule step.
if ! env "${GOENV[@]}" HOME="$T/home-g" bash "$SUT" "$T/absent" > "$T/out" 2> "$T/err" \
  && grep -q "no engine-sync checkout at $T/absent" "$T/err" && [ ! -e "$T/home-g" ]; then
  ok "a missing checkout is refused, naming the submodule step"
else
  bad "missing checkout" "$(cat "$T/out" "$T/err" 2>/dev/null)"
fi

shtest_end
