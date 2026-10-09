#!/usr/bin/env bash
# Self-test for scripts/install-engine-sync.sh: it must build a checkout's
# cmd/engine-sync, stamped with the plugin manifest's version, into
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
# prints "engine-sync {stamped version}" for `version`.
mkcheckout() {
  local dir=$1
  mkdir -p "$dir/cmd/engine-sync" "$dir/plugins/engine-sync/.claude-plugin"
  printf 'module example.invalid/engine-sync\n\ngo 1.21\n' > "$dir/go.mod"
  cat > "$dir/cmd/engine-sync/main.go" <<'EOF'
package main

import (
	"fmt"
	"os"
)

var version = "unstamped"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println("engine-sync " + version)
		return
	}
	os.Exit(2)
}
EOF
  setversion "$dir" "$2"
}

setversion() {
  printf '{\n  "name": "engine-sync",\n  "version": "%s",\n  "license": "MIT"\n}\n' "$2" \
    > "$1/plugins/engine-sync/.claude-plugin/plugin.json"
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

# (a) ENGINE_SYNC_HOME wins: the versioned copy, the relative link, the stamp.
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

# (e) a malformed version builds nothing.
setversion "$CHECKOUT" '1.2 bad'
if ! runsut "$T/home-e" ENGINE_SYNC_HOME="$T/esh-e" && [ ! -e "$T/esh-e" ] \
  && grep -q 'no usable "version"' "$T/err"; then
  ok "a malformed manifest version is refused before any build"
else
  bad "malformed version" "$(cat "$T/out" "$T/err" 2>/dev/null)"
fi
setversion "$CHECKOUT" 1.2.5

# (f) a failed build leaves the installed version and the link untouched.
printf 'package main\n\nfunc main() { broken }\n' > "$CHECKOUT/cmd/engine-sync/main.go"
if ! runsut "$T/home-a" ENGINE_SYNC_HOME="$ESH" && [ ! -e "$ESH/versions/1.2.5/engine-sync" ] \
  && [ "$("$ESH/bin/engine-sync" version)" = "engine-sync 1.2.4" ] \
  && [ -z "$(find "$ESH/versions/1.2.5" -name 'engine-sync.new.*' 2>/dev/null)" ] \
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
