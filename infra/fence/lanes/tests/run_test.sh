#!/usr/bin/env bash
# Fixture-driven tests for lanes/run.sh — the plan it prints, the canonical
# order it imposes, solo vs sequence, the budget verdicts (unpinned, within,
# breached, UNBUDGETED) and every refusal. docker and root.sh are stubs, so no
# container is ever started and no model turn is ever spent.
#
#   bash infra/fence/lanes/tests/run_test.sh
#   LANE_SUT_DIR=/tmp/mutated-lanes bash …/run_test.sh    # red-first
set -uo pipefail

SUT_DIR="${LANE_SUT_DIR:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)}"
SHTEST_TAG=lane-run-test
# shellcheck source=/dev/null
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"

# A COPY of the lanes directory: the budget and ledger fixtures are edited in
# place, and run.sh resolves every sibling from its own location. Nested under
# $T/infra/fence/lanes (not bare $T/lanes) so run.sh's own HERE/../../..
# resolves ROOT to $T exactly as it does in the real tree — the non-dry-run
# path (test 15) sources container.sh's lane_fence_env, which needs a real
# $ROOT/infra/fence/fence-env.sh and a real git repo to read.
LANES="$T/infra/fence/lanes"
mkdir -p "$LANES" "$T/infra/fence"
cp "$SUT_DIR"/*.sh "$SUT_DIR"/*.yml "$SUT_DIR"/*.tsv "$SUT_DIR"/*.md "$SUT_DIR"/pending.txt "$LANES/" 2>/dev/null
cp "$SUT_DIR/../fence-env.sh" "$T/infra/fence/fence-env.sh" 2>/dev/null
# The real landscape.md too — run.sh now delegates its own map gate to
# check-map.sh (F7), and check-map.sh reads $ROOT/docs/dev/testing/landscape.md
# (ROOT resolves to $T here); without it every non-dry-run invocation would
# report LANDSCAPE-UNREADABLE regardless of what this suite is actually
# testing.
mkdir -p "$T/docs/dev/testing"
cp "$SUT_DIR/../../../docs/dev/testing/landscape.md" "$T/docs/dev/testing/landscape.md" 2>/dev/null
(cd "$T" && git init -q && git config user.email t@t && git config user.name t && git commit -q --allow-empty -m fixture)
RUN="$LANES/run.sh"
[ -f "$RUN" ] || { echo "run_test: no run.sh at $RUN" >&2; exit 2; }

# ── stubs ───────────────────────────────────────────────────────────────────
BIN="$T/bin"
mkdir -p "$BIN"
cat >"$BIN/docker" <<'STUB'
#!/usr/bin/env bash
# `image inspect` answers according to $STUB_IMAGE_PRESENT; everything else is
# recorded and accepted, so a --dry-run never depends on a real daemon.
printf '%s\n' "$*" >>"${STUB_DOCKER_LOG:-/dev/null}"
case "$1 ${2:-}" in
  "image inspect") [ "${STUB_IMAGE_PRESENT:-1}" = 1 ] ;;
  "info") exit 0 ;;
  *) exit 0 ;;
esac
STUB
chmod +x "$BIN/docker"
cat >"$T/root-stub.sh" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${STUB_ROOT_LOG:-/dev/null}"
# The real root.sh parses its flags in any order, so the stub must too: the
# runner now passes the seat roster (--accounts N) ahead of the verb.
for arg in "$@"; do
  if [ "$arg" = --print-hash ]; then printf 'deadbeefcafe\n'; exit 0; fi
done
printf 'pfm-lane-root:deadbeefcafe\n'
STUB
chmod +x "$T/root-stub.sh"
export PATH="$BIN:$PATH"
export LANE_ROOT_SH="$T/root-stub.sh"
export LANE_OUT_ROOT="$T/out"
export STUB_DOCKER_LOG="$T/docker.log"
export STUB_ROOT_LOG="$T/root.log"

run_sut() { OUT="$(bash "$RUN" "$@" 2>&1)"; RC=$?; }

# ---- 1: --dry-run prints the plan and starts nothing ----------------------

: >"$T/docker.log"
run_sut --lanes O1,E1 --dry-run
if [ "$RC" -eq 0 ] &&
  printf '%s' "$OUT" | grep -q 'PLAN (--dry-run — nothing was executed' &&
  printf '%s' "$OUT" | grep -q 'root hash   deadbeefcafe' &&
  printf '%s' "$OUT" | grep -q 'root image  pfm-lane-root:deadbeefcafe — REUSE (image present)' &&
  printf '%s' "$OUT" | grep -qE 'lane O1 · [0-9]+ beats · spends' &&
  ! grep -q '^run -d' "$T/docker.log"; then
  ok "--dry-run: plan with hash, image decision, per-lane beats/seats; no container started"
else
  bad "dry-run plan" "rc=$RC" "$OUT" "docker: $(cat "$T/docker.log")"
fi

# ---- 2: canonical order, whatever order --lanes names --------------------

run_sut --lanes E1,O1 --dry-run
order="$(printf '%s' "$OUT" | sed -n 's/^run: lane order  //p')"
if [ "$order" = "O1 → E1" ]; then
  ok "canonical order: --lanes E1,O1 runs O1 → E1"
else
  bad "canonical order" "order=[$order]" "$OUT"
fi

# ---- 3: solo vs sequence in the header -----------------------------------

run_sut --lanes E1 --dry-run
solo="$(printf '%s' "$OUT" | sed -n 's/^run: mode        //p')"
run_sut --lanes E1,O1 --dry-run
seq="$(printf '%s' "$OUT" | sed -n 's/^run: mode        //p')"
if [ "$solo" = solo ] && [ "$seq" = sequence ]; then
  ok "header names the mode: one lane = solo, two = sequence"
else
  bad "mode header" "solo=[$solo] sequence=[$seq]"
fi

# ---- 4: the default selection drops pending lanes BY NAME ----------------
# The pending state is this suite's own fixture: two lane scripts moved aside
# and a pending.txt naming them — never the real directory's pending list, which
# is empty once every lane is written.

mv "$LANES/F.sh" "$T/F.sh.aside" && mv "$LANES/M.sh" "$T/M.sh.aside" ||
  { echo "run_test: the lanes copy has no F.sh/M.sh to move aside" >&2; exit 2; }
printf 'F\nM\n' >"$LANES/pending.txt"
run_sut --dry-run
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -q 'NOT WRITTEN — pending lanes dropped from this plan: F M'; then
  ok "a bare run names every pending lane it dropped instead of shrinking the sequence silently"
else
  bad "pending drop" "rc=$RC" "$OUT"
fi

# ---- 5: a NAMED pending lane is refused, exit 2 --------------------------

run_sut --lanes F --dry-run
if [ "$RC" -eq 2 ] && printf '%s' "$OUT" | grep -q 'lane F is NOT WRITTEN'; then
  ok "--lanes F (pending) is refused with exit 2, naming pending.txt"
else
  bad "named pending lane" "rc=$RC" "$OUT"
fi
mv "$T/F.sh.aside" "$LANES/F.sh" && mv "$T/M.sh.aside" "$LANES/M.sh"
: >"$LANES/pending.txt"

# ---- 6: an unknown lane is refused, exit 2 ------------------------------

run_sut --lanes Q7 --dry-run
if [ "$RC" -eq 2 ] && printf '%s' "$OUT" | grep -q "unknown lane 'Q7'"; then
  ok "an unknown lane name is refused with the canonical list"
else
  bad "unknown lane" "rc=$RC" "$OUT"
fi

# ---- 7: budget verdicts — unpinned records but does not judge -----------

run_sut --check-budget E1 4242
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -q 'budget: E1 unpinned — recorded, not judged: 4242s'; then
  ok "budget: an unpinned lane is recorded, not judged, and says so by name"
else
  bad "unpinned budget" "rc=$RC" "$OUT"
fi

# ---- 8: a pinned budget inside tolerance passes, outside it breaches ----

sed -i.bak 's/^  E1: unpinned$/  E1: 600/' "$LANES/budgets.yml"
run_sut --check-budget E1 700
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -q 'budget: ✓ E1 — 700s within 750s (budget 600s ×1.25)'; then
  ok "budget: 700s against 600s ×1.25 is within tolerance"
else
  bad "budget within" "rc=$RC" "$OUT"
fi
run_sut --check-budget E1 900
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q 'budget: ✗ E1 — 900s over budget 600s ×1.25 = 750s'; then
  ok "budget breach: 900s over 600s ×1.25 exits 1 and names the limit"
else
  bad "budget breach" "rc=$RC" "$OUT"
fi
mv "$LANES/budgets.yml.bak" "$LANES/budgets.yml"

# ---- 9: a lane with no budget row at all is red -------------------------

grep -v '^  E1:' "$LANES/budgets.yml" >"$LANES/budgets.yml.tmp" && mv "$LANES/budgets.yml.tmp" "$LANES/budgets.yml"
run_sut --check-budget E1 10
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q 'budget: ✗ E1 — UNBUDGETED: no row in budgets.yml'; then
  ok "budget: a lane with no row is UNBUDGETED and red"
else
  bad "unbudgeted" "rc=$RC" "$OUT"
fi
run_sut --lanes E1 --dry-run
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q 'UNBUDGETED' &&
  printf '%s' "$OUT" | grep -q 'lane(s) in this plan have no budget row'; then
  ok "budget: --dry-run over an unbudgeted lane exits 1 (a plan that cannot pass is a red plan)"
else
  bad "unbudgeted plan" "rc=$RC" "$OUT"
fi
cp "$SUT_DIR/budgets.yml" "$LANES/budgets.yml"

# ---- 10: a ledger that breaks the known-gap law stops the run ------------

cat >"$LANES/known-gaps.yml" <<'YML'
gaps:
  - beat: E1.99-fixture
    landscape_id: Z1
    why: "no expiry"
    owner: FIXTURE
    date: 2026-09-17
YML
run_sut --lanes E1 --dry-run
if [ "$RC" -eq 2 ] && printf '%s' "$OUT" | grep -q 'no expires:' &&
  printf '%s' "$OUT" | grep -q 'the known-gap ledger is not valid'; then
  ok "an entry with no expires: stops the run at exit 2, before any lane"
else
  bad "ledger gate" "rc=$RC" "$OUT"
fi
cp "$SUT_DIR/known-gaps.yml" "$LANES/known-gaps.yml"

# ---- 11: a missing budgets file is TOOLCHAIN-MISSING, never a clean plan --

mv "$LANES/budgets.yml" "$T/budgets.away"
run_sut --lanes E1 --dry-run
if [ "$RC" -eq 2 ] && printf '%s' "$OUT" | grep -q 'TOOLCHAIN-MISSING'; then
  ok "a missing budgets.yml is named, never treated as 'no budgets to check'"
else
  bad "missing budgets" "rc=$RC" "$OUT"
fi
mv "$T/budgets.away" "$LANES/budgets.yml"

# ---- 12: the image decision names a BUILD when no image carries the hash --

STUB_IMAGE_PRESENT=0 run_sut --lanes E1 --dry-run
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -q 'BUILD (no image for this hash)'; then
  ok "root image: absent for this hash → the plan says BUILD"
else
  bad "image decision" "rc=$RC" "$OUT"
fi

# ---- 13: --seats selects the root's seat roster --------------------------
# A root built with every seat, driven by a run that was told `--seats cc:1`,
# offers the lane a second seat it holds no credential for: the account-switch
# beats then fail for the harness's reason, not the product's.

: >"$T/root.log"
run_sut --lanes E1 --seats cc:1 --dry-run
if [ "$RC" -eq 0 ] && grep -q -- '--accounts 1' "$T/root.log" &&
  printf '%s' "$OUT" | grep -q 'run: seats       cc:1'; then
  ok "--seats cc:1 reaches the root builder as --accounts 1 (the container roster is the run's seats)"
else
  bad "seat plumbing" "rc=$RC" "root.log=[$(cat "$T/root.log")]" "$OUT"
fi

: >"$T/root.log"
run_sut --lanes E1 --seats cc:1,cc:2 --dry-run
if [ "$RC" -eq 0 ] && grep -q -- '--accounts 1,2' "$T/root.log"; then
  ok "--seats cc:1,cc:2 reaches the root builder as --accounts 1,2"
else
  bad "seat plumbing (two seats)" "rc=$RC" "root.log=[$(cat "$T/root.log")]"
fi

# ---- 14: the root hash covers the seat roster ---------------------------
# Same tree, different seats = a different container config. One hash for both
# would serve a one-seat image to a two-seat run and call it REUSE.

REAL_ROOT="$SUT_DIR/root.sh"
h1="$(bash "$REAL_ROOT" --print-hash --accounts 1 2>&1)"
h2="$(bash "$REAL_ROOT" --print-hash --accounts 1,2 2>&1)"
h3="$(bash "$REAL_ROOT" --print-hash --accounts 1 2>&1)"
if [ -n "$h1" ] && [ "$h1" = "$h3" ] && [ "$h1" != "$h2" ]; then
  ok "root hash: the seat roster is a hash input ($h1 vs $h2), and it is stable for one roster"
else
  bad "root hash over seats" "h1=[$h1] h2=[$h2] h3=[$h3]"
fi

# ---- 15: a lane that produces no result row fails the run's exit code -----
# (F11) The docker stub never actually copies a row.tsv into $OUT — a real
# lane container the exec exited 0 for but that died before lane_end (crash,
# OOM-kill) looks exactly like this: exec rc 0, no row file landed. lib.sh's
# own docstring says a missing .row.tsv is never a pass; the exit code must
# say so too, not just the printed "✗ lane … produced no result row" line.

run_sut --lanes E1 --root reuse
if printf '%s' "$OUT" | grep -q 'run: ✗ lane E1 produced no result row' && [ "$RC" -ne 0 ]; then
  ok "a lane with no result row (exec 0, no row.tsv landed) fails the run's own exit code, not just its printed line"
else
  bad "missing row exit code" "rc=$RC" "$OUT"
fi

# ---- 16: run.sh delegates its own beat↔map contract to check-map.sh (F7) --
# The old inline "unmapped" walk at run.sh:289 was a partial near-copy of
# check-map.sh's own direction-2 check — removed; run.sh now calls the real
# gate once per run and writes its full output to $OUT/check-map.log, never a
# second, partial re-implementation of the same walk.

rm -rf "$T/out"
run_sut --lanes E1 --root reuse
RUNOUT_DIR="$(find "$T/out" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | head -1)"
if [ -n "$RUNOUT_DIR" ] && [ -f "$RUNOUT_DIR/check-map.log" ] &&
  grep -q '^check-map: clean' "$RUNOUT_DIR/check-map.log"; then
  ok "run.sh calls the real check-map.sh --no-derive once per run and keeps its full output at check-map.log (clean, against the real fixtures)"
else
  bad "check-map delegation" "runout=[$RUNOUT_DIR]" "$(cat "$RUNOUT_DIR/check-map.log" 2>&1)"
fi

# ---- 17: a broken map.tsv (one beat's row gone) fails the run and is NAMED -
# Never absorbed into the same "no result row" red as test 15 above — this is
# specifically the static map contract, read from check-map.sh's own verdict
# (removing E1.01-open-seat1's only row leaves the beat itself in E1.sh with
# no map row at all: direction (b), UNMAPPED-BEAT).

cp "$LANES/map.tsv" "$T/map.tsv.bak"
grep -vF "$(printf 'K1\tE1\tE1.01-open-seat1')" "$LANES/map.tsv" >"$LANES/map.tsv.tmp" && mv "$LANES/map.tsv.tmp" "$LANES/map.tsv"
rm -rf "$T/out"
run_sut --lanes E1 --root reuse
RUNOUT_DIR2="$(find "$T/out" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | head -1)"
if [ "$RC" -ne 0 ] && printf '%s' "$OUT" | grep -q '^map: ✗' &&
  [ -n "$RUNOUT_DIR2" ] && grep -q 'UNMAPPED-BEAT: E1.01-open-seat1' "$RUNOUT_DIR2/check-map.log" 2>/dev/null; then
  ok "a map.tsv with a beat's row removed fails the run (map: ✗) and check-map.log names UNMAPPED-BEAT"
else
  bad "broken map fails run" "rc=$RC" "$OUT" "$(cat "$RUNOUT_DIR2/check-map.log" 2>&1)"
fi
mv "$T/map.tsv.bak" "$LANES/map.tsv"

shtest_end
