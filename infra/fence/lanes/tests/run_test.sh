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
T="$(mktemp -d "${TMPDIR:-/tmp}/lane-run-test.XXXXXX")"
trap 'rm -rf -- "$T"' EXIT

PASS=0 FAIL=0
ok() { printf 'PASS  %s\n' "$1"; PASS=$((PASS + 1)); }
bad() { printf 'FAIL  %s\n' "$1" >&2; shift; [ $# -gt 0 ] && printf '      %s\n' "$@" >&2; FAIL=$((FAIL + 1)); }

# A COPY of the lanes directory: the budget and ledger fixtures are edited in
# place, and run.sh resolves every sibling from its own location.
LANES="$T/lanes"
mkdir -p "$LANES"
cp "$SUT_DIR"/*.sh "$SUT_DIR"/*.yml "$SUT_DIR"/*.tsv "$SUT_DIR"/pending.txt "$LANES/" 2>/dev/null
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
case "${1:-}" in
  --print-hash) printf 'deadbeefcafe\n' ;;
  *) printf 'pfm-lane-root:deadbeefcafe\n' ;;
esac
STUB
chmod +x "$T/root-stub.sh"
export PATH="$BIN:$PATH"
export LANE_ROOT_SH="$T/root-stub.sh"
export LANE_OUT_ROOT="$T/out"
export STUB_DOCKER_LOG="$T/docker.log"

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

run_sut --dry-run
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -q 'NOT WRITTEN — pending lanes dropped from this plan: E2 E3 F M A O2'; then
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

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
