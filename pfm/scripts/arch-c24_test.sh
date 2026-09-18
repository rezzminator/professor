#!/usr/bin/env bash
# Self-test for scripts/arch-c24.sh, the C24-unwrapped-door census: it must
# COUNT a bare http.Client{ / database/sql import / exec.Command / tmux.Command
# outside the doors, must NOT count one inside a door or one handed to
# obs.WrapClient, must refuse a count above the committed baseline naming the
# row, and must report ERROR — never PASS — when it could not enumerate. It
# runs against throwaway git fixtures (PFM=<fixture>), never this repo.
#
# Harness style follows scripts/arch-check_test.sh, the sibling shell test.
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="$ROOT/scripts/arch-c24.sh"
T="$(mktemp -d "${TMPDIR:-/tmp}/pfm-arch-c24-test.XXXXXX")"
cleanup() { rm -rf -- "$T"; }
trap cleanup EXIT

PASS=0
FAIL=0
ok()  { printf 'PASS  %s\n' "$1"; PASS=$((PASS+1)); }
bad() { printf 'FAIL  %s\n' "$1" >&2; shift; [ $# -gt 0 ] && printf '      %s\n' "$@" >&2; FAIL=$((FAIL+1)); }

# fixture <dir>: a minimal git tree with one door of each family outside its
# wrapper, one inside, and one wrapped construction that must not count.
fixture() {
  local dir=$1
  mkdir -p "$dir/internal/loud" "$dir/internal/obs" "$dir/internal/sqlitedb" "$dir/internal/tmux" "$dir/internal/deps" "$dir/internal/testjail" "$dir/.arch"
  cat > "$dir/internal/loud/loud.go" <<'EOF'
package loud

import (
	"database/sql"
	"net/http"
	"os/exec"

	pfmtmux "hostops/pfm/internal/tmux"
)

func Bare() *http.Client   { return &http.Client{} }
func Wrapped() *http.Client { return obs.WrapClient(&http.Client{}) }
func Proc()                 { _ = exec.Command("git"); _ = pfmtmux.Command(nil, "", "", "kill-server") }
EOF
  printf 'package obs\n\nimport "net/http"\n\nfunc WrapClient(c *http.Client) *http.Client { return c }\nvar d = http.DefaultClient\n' > "$dir/internal/obs/httpout.go"
  printf 'package sqlitedb\n\nimport "database/sql"\n\nvar _ sql.DB\n' > "$dir/internal/sqlitedb/sqlitedb.go"
  printf 'package tmux\n\nimport "os/exec"\n\nfunc Command() *exec.Cmd { return exec.Command("tmux") }\n' > "$dir/internal/tmux/tmux.go"
  printf 'package deps\n\nimport "os/exec"\n\nfunc look() { _, _ = exec.LookPath("git") }\n' > "$dir/internal/deps/runner.go"
  printf 'package testjail\n\nimport "os/exec"\n\nfunc jail() { _ = exec.Command("sh") }\n' > "$dir/internal/testjail/testjail.go"
  printf 'package loud\n\nimport "os/exec"\n\nfunc TestX() { _ = exec.Command("sh") }\n' > "$dir/internal/loud/loud_test.go"
  git -C "$dir" init -q 2>/dev/null || return 1
  git -C "$dir" -c user.email=t@example.invalid -c user.name=t add -A 2>/dev/null || return 1
}

# The fixture is its own git repo, so the fence's mounted-repo overrides are
# dropped (arch-check_test.sh explains why).
c24() {
  env -u PFM_DEV_REPO_GIT_DIR -u PFM_DEV_REPO_WORK_TREE PFM="$1" bash "$SUT" "${2:-check}" </dev/null 2>&1
}

# ---- 1: a missing baseline is ERROR, never PASS ------------------------------
REPO="$T/repo"
if fixture "$REPO"; then
  line=$(c24 "$REPO"); rc=$?
  if [[ "$line" == *ERROR* && "$line" == *unwrapped-door.txt* && $rc -eq 2 ]]; then
    ok "C24: a missing baseline reports ERROR (rc 2), not PASS"
  else
    bad "C24: expected ERROR rc 2 for a missing baseline" "$line (rc $rc)"
  fi
else
  bad "C24: could not build the git fixture (is git available?)"
fi

# ---- 2: --measure writes exactly the three unwrapped rows -------------------
line=$(c24 "$REPO" --measure)
want=$'http:internal/loud/loud.go 1\nproc:internal/loud/loud.go 2\nsql:internal/loud/loud.go 1'
if [[ "$line" == *MEASURE* ]] && [ "$(cat "$REPO/.arch/unwrapped-door.txt")" = "$want" ]; then
  ok "C24: measures one bare http.Client, one database/sql import, exec+tmux.Command as two proc doors — and nothing inside obs/sqlitedb/tmux/deps/testjail/tests or behind WrapClient"
else
  bad "C24: unexpected census" "$line" "$(cat "$REPO/.arch/unwrapped-door.txt")"
fi

# ---- 3: with the baseline in place the same tree passes ---------------------
line=$(c24 "$REPO"); rc=$?
if [[ "$line" == *PASS* && $rc -eq 0 ]]; then
  ok "C24: the measured tree passes (rc 0)"
else
  bad "C24: expected PASS after measure" "$line (rc $rc)"
fi

# ---- 4: a planted http.Client{ in a scratch copy is FAIL naming the row ------
printf 'package loud\n\nimport "net/http"\n\nfunc Planted() *http.Client { return &http.Client{} }\n' > "$REPO/internal/loud/planted.go"
line=$(c24 "$REPO"); rc=$?
if [[ "$line" == *FAIL* && "$line" == *"http:internal/loud/planted.go (new 1)"* && $rc -eq 1 ]]; then
  ok "C24: a planted http.Client{ in a new file FAILs (rc 1) naming the row"
else
  bad "C24: expected FAIL naming the planted row" "$line (rc $rc)"
fi
rm -f "$REPO/internal/loud/planted.go"

# ---- 5: a count above baseline in a baselined file is FAIL with old->new ----
printf 'package loud\n\nimport "net/http"\n\nfunc Bare() *http.Client { return &http.Client{} }\nfunc Two() *http.Client { return &http.Client{} }\n' > "$REPO/internal/loud/loud.go"
line=$(c24 "$REPO")
if [[ "$line" == *FAIL* && "$line" == *"http:internal/loud/loud.go (1->2)"* ]]; then
  ok "C24: a second bare client in a baselined file FAILs with the old->new count"
else
  bad "C24: expected FAIL 1->2" "$line"
fi

# ---- 6: --measure only shrinks: it never raises a count or adds a row --------
line=$(c24 "$REPO" --measure)
if grep -q '^http:internal/loud/loud.go 1$' "$REPO/.arch/unwrapped-door.txt" && ! grep -q 'planted' "$REPO/.arch/unwrapped-door.txt"; then
  ok "C24: --measure keeps the lower count and adds no row"
else
  bad "C24: --measure raised the baseline" "$(cat "$REPO/.arch/unwrapped-door.txt")"
fi

# ---- 7: an unreadable source list is ERROR, never PASS ----------------------
EMPTY="$T/empty"
mkdir -p "$EMPTY/.arch" && git -C "$EMPTY" init -q 2>/dev/null
: > "$EMPTY/.arch/unwrapped-door.txt"
line=$(c24 "$EMPTY"); rc=$?
if [[ "$line" == *ERROR* && "$line" == *"no Go sources"* && $rc -eq 2 ]]; then
  ok "C24: a tree with no Go sources reports ERROR (the enumerator did not run), not PASS"
else
  bad "C24: expected ERROR for an empty source list" "$line (rc $rc)"
fi

# ---- 8: a door under internal/mockengine/ is exempt like internal/testjail --
MOCK="$T/mock"
if fixture "$MOCK"; then
  mkdir -p "$MOCK/internal/mockengine"
  printf 'package mockengine\n\nimport "os/exec"\n\nfunc Spawn() { _ = exec.Command("sh") }\n' \
    > "$MOCK/internal/mockengine/hooks.go"
  git -C "$MOCK" -c user.email=t@example.invalid -c user.name=t add -A 2>/dev/null
  line=$(c24 "$MOCK" --measure)
  baseline="$(cat "$MOCK/.arch/unwrapped-door.txt")"
  if [[ "$line" == *MEASURE* ]] \
    && ! grep -q 'mockengine' <<<"$baseline" \
    && grep -q '^proc:internal/loud/loud.go 2$' <<<"$baseline"; then
    ok "C24: a proc door under internal/mockengine/ is not counted while the same door in internal/loud/ still is"
  else
    bad "C24: expected mockengine exempt and internal/loud/loud.go still counted" "$baseline"
  fi
else
  bad "C24: could not build the mockengine fixture"
fi

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
