#!/usr/bin/env bash
# Shared by every plugin-lab self-test: the lab paths, one temp root, assertions.
# shellcheck disable=SC2034 # LAB_DIR and FAKES are read by the sourcing test
LAB_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
FAKES="$LAB_DIR/tests/fakes"
T=$(mktemp -d /tmp/plab.XXXXXX)
# shtest_wait_until, shtest_wait_gone, shtest_wait_line, shtest_hold: the one wait door of the shell tests
# shellcheck source=../../../scripts/shtest-wait.sh
source "$LAB_DIR/../../scripts/shtest-wait.sh" || { echo "testlib: the wait door scripts/shtest-wait.sh did not load" >&2; exit 2; }
# the fixed now lib/auth.sh's expiry check reads, so a credentials stamp is exact on any host and at any speed
export LAB_NOW_S=1767225600
PASSES=0
FAILS=0

testlib_cleanup() { # every private tmux server a test started, by its socket path
  local sock
  while IFS= read -r sock; do
    tmux -S "$sock" kill-server 2>/dev/null || true
  done < <(find "$T" -name t.sock 2>/dev/null)
  rm -rf "$T"
}
trap testlib_cleanup EXIT

ok() { PASSES=$((PASSES + 1)); printf 'ok - %s\n' "$1"; }
bad() { FAILS=$((FAILS + 1)); printf 'FAIL - %s\n  got: %s\n' "$1" "$2"; }
expect_eq() { # <name> <got> <want>
  if [[ "$2" == "$3" ]]; then ok "$1"; else bad "$1" "$2 (want: $3)"; fi
}
expect_has() { # <name> <text> <fixed string>
  if [[ "$2" == *"$3"* ]]; then ok "$1"; else bad "$1" "${2:0:600} (want to contain: $3)"; fi
}
expect_lacks() { # <name> <text> <fixed string>
  if [[ "$2" != *"$3"* ]]; then ok "$1"; else bad "$1" "${2:0:600} (must not contain: $3)"; fi
}
finish() { # a hung wait of this shell is a failure, whether or not the test read its status
  (( SHTEST_WAITS_HUNG == 0 )) || { printf 'FAIL - %d waits hung (each WAIT HUNG line above)\n' "$SHTEST_WAITS_HUNG"; FAILS=$((FAILS + SHTEST_WAITS_HUNG)); }
  printf '%s: %d passed, %d failed\n' "$(basename "$0")" "$PASSES" "$FAILS"
  [[ "$FAILS" == 0 ]]
}

lab_lib() { # <name>...: source lib files the way the lab does; a missing one leaves its asserts red
  local n
  for n in "$@"; do
    # shellcheck source=/dev/null
    source "$LAB_DIR/lib/$n.sh" 2>/dev/null || printf 'note: lib/%s.sh not loadable\n' "$n"
  done
}

sha256_of() { # <string>: hex sha256, either tool
  if command -v sha256sum >/dev/null; then printf '%s' "$1" | sha256sum | cut -c1-64
  else printf '%s' "$1" | shasum -a 256 | cut -c1-64; fi
}
