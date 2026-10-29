#!/usr/bin/env bash
# The plugin-log conformance suite: each language's implementation against ../spec.json. A missing toolchain fails.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
fail=0
step() { echo "== $1"; shift; if "$@"; then echo "PASS  $*"; else echo "FAIL  $*" >&2; fail=1; fi; }
step ts node --experimental-strip-types --no-warnings --test "$HERE/ts/plugin-log.test.ts"
step go bash -c 'cd "$1" && GOFLAGS=-mod=mod go vet ./... && go test -count=1 ./...' _ "$HERE/go"
step python python3 "$HERE/python/plugin_log_test.py"
exit "$fail"
