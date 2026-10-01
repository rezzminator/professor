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
cat >"$LANES/budgets.yml" <<'YML'
tolerance: 1.25

# The whole canonical sequence, O1 → … → O2, in one container.
sequence: unpinned

lanes:
  O1: unpinned
  E1: unpinned
  E2: unpinned
  E3: unpinned
  F: unpinned
  M: unpinned
  A: unpinned
  O2: unpinned
YML
cp "$LANES/budgets.yml" "$T/budgets.fixture.yml"
cp -R "$SUT_DIR/fixtures" "$SUT_DIR/scenarios" "$LANES/"
cp "$SUT_DIR/../fence-env.sh" "$T/infra/fence/fence-env.sh" 2>/dev/null
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
  "exec "*)
    case "$*" in
      *cred-scan.sh*)
        if [ "${STUB_REFUSE:-0}" = 1 ]; then
          echo 'cred-scan: ✗ CREDENTIAL-REFUSED /root/.codex/auth.json — not a registered fixture'
          exit 1
        fi ;;
      *'test -e '*egress.ready*) [ "${STUB_EGRESS_READY:-1}" = 1 ] || exit 1 ;;
      *'grep -q '^EGRESS*) [ -n "${STUB_EGRESS_VERDICT-PASS}" ] || exit 1 ;;
    esac
    exit 0 ;;
  "cp "*)
    case "$2" in
      *.row.tsv) [ -n "${STUB_ROW_FIXTURE:-}" ] && cp "$STUB_ROW_FIXTURE" "$3" ;;
      *.waits.tsv) [ -n "${STUB_WAITS_FIXTURE:-}" ] && cp "$STUB_WAITS_FIXTURE" "$3" ;;
      *.out)
        [ "${STUB_EGRESS_COPY_FAIL:-0}" = 0 ] || exit 1
        [ -n "${STUB_EGRESS_VERDICT-PASS}" ] || exit 1
        printf '%s\n' "${STUB_EGRESS_OUTPUT:-EGRESS ${STUB_EGRESS_VERDICT-PASS} fixture}" >"$3" ;;
      */egress/.)
        [ "${STUB_EGRESS_EVIDENCE_COPY_FAIL:-0}" = 0 ] || exit 1
        mkdir -p "$3"
        printf 'fixture pcap\n' >"$3/fixture.pcap"
        printf 'fixture log\n' >"$3/fixture.log" ;;
    esac
    exit 0 ;;
  *) exit 0 ;;
esac
STUB
chmod +x "$BIN/docker"
cat >"$BIN/sleep" <<'STUB'
#!/usr/bin/env bash
exit 0
STUB
chmod +x "$BIN/sleep"
cat >"$T/root-stub.sh" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${STUB_ROOT_LOG:-/dev/null}"
# The root builder receives its own flags only.
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
  grep -q 'PLAN (--dry-run — nothing was executed' <<<"$OUT" &&
  grep -q 'root hash   deadbeefcafe' <<<"$OUT" &&
  grep -q 'root image  pfm-lane-root:deadbeefcafe — REUSE (image present)' <<<"$OUT" &&
  grep -qE 'lane O1 · [0-9]+ beats · spends' <<<"$OUT" &&
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
if [ "$RC" -eq 0 ] && grep -q 'NOT WRITTEN — pending lanes dropped from this plan: F M' <<<"$OUT"; then
  ok "a bare run names every pending lane it dropped instead of shrinking the sequence silently"
else
  bad "pending drop" "rc=$RC" "$OUT"
fi

# ---- 5: a NAMED pending lane is refused, exit 2 --------------------------

run_sut --lanes F --dry-run
if [ "$RC" -eq 2 ] && grep -q 'lane F is NOT WRITTEN' <<<"$OUT"; then
  ok "--lanes F (pending) is refused with exit 2, naming pending.txt"
else
  bad "named pending lane" "rc=$RC" "$OUT"
fi
mv "$T/F.sh.aside" "$LANES/F.sh" && mv "$T/M.sh.aside" "$LANES/M.sh"
: >"$LANES/pending.txt"

# ---- 6: an unknown lane is refused, exit 2 ------------------------------

run_sut --lanes Q7 --dry-run
if [ "$RC" -eq 2 ] && grep -q "unknown lane 'Q7'" <<<"$OUT"; then
  ok "an unknown lane name is refused with the canonical list"
else
  bad "unknown lane" "rc=$RC" "$OUT"
fi

# ---- 7: budget verdicts — unpinned records but does not judge -----------

run_sut --check-budget E1 4242
if [ "$RC" -eq 0 ] && grep -q 'budget: E1 unpinned — recorded, not judged: 4242s' <<<"$OUT"; then
  ok "budget: an unpinned lane is recorded, not judged, and says so by name"
else
  bad "unpinned budget" "rc=$RC" "$OUT"
fi

# ---- 8: a pinned budget inside tolerance passes, outside it breaches ----

sed -i.bak 's/^  E1: unpinned$/  E1: 600/' "$LANES/budgets.yml"
run_sut --check-budget E1 700
if [ "$RC" -eq 0 ] && grep -q 'budget: ✓ E1 — 700s within 750s (budget 600s ×1.25)' <<<"$OUT"; then
  ok "budget: 700s against 600s ×1.25 is within tolerance"
else
  bad "budget within" "rc=$RC" "$OUT"
fi
run_sut --check-budget E1 900
if [ "$RC" -eq 1 ] && grep -q 'budget: ✗ E1 — 900s over budget 600s ×1.25 = 750s' <<<"$OUT"; then
  ok "budget breach: 900s over 600s ×1.25 exits 1 and names the limit"
else
  bad "budget breach" "rc=$RC" "$OUT"
fi
mv "$LANES/budgets.yml.bak" "$LANES/budgets.yml"

# ---- 9: a lane with no budget row at all is red -------------------------

grep -v '^  E1:' "$LANES/budgets.yml" >"$LANES/budgets.yml.tmp" && mv "$LANES/budgets.yml.tmp" "$LANES/budgets.yml"
run_sut --check-budget E1 10
if [ "$RC" -eq 1 ] && grep -q 'budget: ✗ E1 — UNBUDGETED: no row in budgets.yml' <<<"$OUT"; then
  ok "budget: a lane with no row is UNBUDGETED and red"
else
  bad "unbudgeted" "rc=$RC" "$OUT"
fi
run_sut --lanes E1 --dry-run
if [ "$RC" -eq 1 ] && grep -q 'UNBUDGETED' <<<"$OUT" &&
  grep -q 'lane(s) in this plan have no budget row' <<<"$OUT"; then
  ok "budget: --dry-run over an unbudgeted lane exits 1 (a plan that cannot pass is a red plan)"
else
  bad "unbudgeted plan" "rc=$RC" "$OUT"
fi
cp "$T/budgets.fixture.yml" "$LANES/budgets.yml"

# ---- 10: a ledger that breaks the known-gap law stops the run ------------

cat >"$LANES/known-gaps.yml" <<'YML'
gaps:
  - beat: E1.99-fixture
    why: "no expiry"
    owner: FIXTURE
    date: 2026-09-17
YML
run_sut --lanes E1 --dry-run
if [ "$RC" -eq 2 ] && grep -q 'no expires:' <<<"$OUT" &&
  grep -q 'the known-gap ledger is not valid' <<<"$OUT"; then
  ok "an entry with no expires: stops the run at exit 2, before any lane"
else
  bad "ledger gate" "rc=$RC" "$OUT"
fi
cp "$SUT_DIR/known-gaps.yml" "$LANES/known-gaps.yml"

# ---- 11: a missing budgets file is TOOLCHAIN-MISSING, never a clean plan --

mv "$LANES/budgets.yml" "$T/budgets.away"
run_sut --lanes E1 --dry-run
if [ "$RC" -eq 2 ] && grep -q 'TOOLCHAIN-MISSING' <<<"$OUT"; then
  ok "a missing budgets.yml is named, never treated as 'no budgets to check'"
else
  bad "missing budgets" "rc=$RC" "$OUT"
fi
mv "$T/budgets.away" "$LANES/budgets.yml"

# ---- 12: the image decision names a BUILD when no image carries the hash --

STUB_IMAGE_PRESENT=0 run_sut --lanes E1 --dry-run
if [ "$RC" -eq 0 ] && grep -q 'BUILD (no image for this hash)' <<<"$OUT"; then
  ok "root image: absent for this hash → the plan says BUILD"
else
  bad "image decision" "rc=$RC" "$OUT"
fi

# ---- 13: seats are passed to the lanes, while both fixtures stay in the root.
: >"$T/root.log"; : >"$T/docker.log"
run_sut --lanes E1 --seats cc:2
if grep -q -- '-e LANE_SEATS=cc:2' "$T/docker.log" &&
  [ "$(cat "$T/root.log")" = --print-hash ] &&
  grep -q -- '--network none' "$T/docker.log" && grep -q -- '-e GOPROXY=off' "$T/docker.log"; then
  ok "--seats reaches LANE_SEATS; root receives only --print-hash; run is offline"
else bad "seat plumbing" "root=[$(cat "$T/root.log")]" "$(cat "$T/docker.log")"; fi

# ---- 14: the same tree gives the same root for different selected seats.
REAL_ROOT="$LANES/root.sh"
LANE_ROOT_SH="$REAL_ROOT" run_sut --lanes E1 --seats cc:1 --dry-run
h1="$(sed -n 's/^run: root hash   \([^ ]*\).*/\1/p' <<<"$OUT")"
LANE_ROOT_SH="$REAL_ROOT" run_sut --lanes E1 --seats cc:2 --dry-run
h2="$(sed -n 's/^run: root hash   \([^ ]*\).*/\1/p' <<<"$OUT")"
if [ "$RC" -eq 0 ] && [ -n "$h1" ] && [ "$h1" = "$h2" ]; then ok "root hash is stable across selected seats"
else bad "root hash across seats" "h1=[$h1] h2=[$h2]" "$OUT"; fi

rm -rf "$T/out"

# ---- 15: a lane that produces no result row fails the run's exit code -----
# (F11) The docker stub never actually copies a row.tsv into $OUT — a real
# lane container the exec exited 0 for but that died before lane_end (crash,
# OOM-kill) looks exactly like this: exec rc 0, no row file landed. lib.sh's
# own docstring says a missing .row.tsv is never a pass; the exit code must
# say so too, not just the printed "✗ lane … produced no result row" line.

run_sut --lanes E1 --root reuse
if grep -q 'run: ✗ lane E1 produced no result row' <<<"$OUT" && [ "$RC" -ne 0 ]; then
  ok "a lane with no result row (exec 0, no row.tsv landed) fails the run's own exit code, not just its printed line"
else
  bad "missing row exit code" "rc=$RC" "$OUT"
fi

# ---- 16: run.sh delegates its own beat↔map contract to check-map.sh (F7) --
# The old inline "unmapped" walk at run.sh:289 was a partial near-copy of
# check-map.sh's own check — removed; run.sh now calls the real
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

# ---- 17: a broken map.tsv (a row naming a beat E1.sh lacks) fails the run
# and is NAMED — never absorbed into the same "no result row" red as test 15
# above; this is specifically the static map contract, read from
# check-map.sh's own verdict (MISSING-BEAT).

cp "$LANES/map.tsv" "$T/map.tsv.bak"
printf 'pfm doctor\tE1\tE1.99-ghost\n' >>"$LANES/map.tsv"
rm -rf "$T/out"
run_sut --lanes E1 --root reuse
RUNOUT_DIR2="$(find "$T/out" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | head -1)"
if [ "$RC" -ne 0 ] && grep -q '^map: ✗' <<<"$OUT" &&
  [ -n "$RUNOUT_DIR2" ] && grep -q 'MISSING-BEAT: E1.99-ghost' "$RUNOUT_DIR2/check-map.log" 2>/dev/null; then
  ok "a map.tsv row naming a beat its lane lacks fails the run (map: ✗) and check-map.log names MISSING-BEAT"
else
  bad "broken map fails run" "rc=$RC" "$OUT" "$(cat "$RUNOUT_DIR2/check-map.log" 2>&1)"
fi
mv "$T/map.tsv.bak" "$LANES/map.tsv"

# ---- 18: the credential gate runs before any lane and EXIT removes the run.
: >"$T/docker.log"
STUB_REFUSE=1 run_sut --lanes E1
if [ "$RC" -eq 1 ] && grep -q 'run: ✗ CREDENTIAL-REFUSED' <<<"$OUT" &&
  ! grep -q '/lanes/E1.sh' "$T/docker.log" &&
  [ "$(grep -c '^rm -f pfm-lane-' "$T/docker.log")" -eq 2 ]; then
  ok "credential refusal stops before the first lane and removes the container"
else bad "run credential gate" "rc=$RC" "$OUT" "$(cat "$T/docker.log")"; fi
# ---- 19: profile copies each lane's waits and prints the longest first ---

printf 'E1\t12\t3\t0\t0\t0\n' >"$T/row.fixture.tsv"
printf 'lane\tbeat\thelper\tcondition\telapsed_s\toutcome\n' >"$T/waits.fixture.tsv"
printf 'E1\tE1.01\twait_for\tfirst state\t0.125\tok\n' >>"$T/waits.fixture.tsv"
printf 'E1\tE1.02\twait_last\tCHAT answer\t2.500\ttimeout\n' >>"$T/waits.fixture.tsv"
rm -rf "$T/out"
STUB_ROW_FIXTURE="$T/row.fixture.tsv" STUB_WAITS_FIXTURE="$T/waits.fixture.tsv" LANE_PROFILE=1 run_sut --lanes E1 --root reuse
profile_out="$(find "$T/out" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | head -1)"
if [ "$RC" -eq 0 ] &&
  grep -qF -- '-e LANE_PROFILE=1' "$T/docker.log" &&
  grep -qF -- 'E1.waits.tsv' "$T/docker.log" &&
  [ "$(head -1 "$profile_out/waits.tsv")" = "$(head -1 "$T/waits.fixture.tsv")" ] &&
  grep -qF "$(tail -1 "$T/waits.fixture.tsv")" "$profile_out/waits.tsv" &&
  grep -qF 'profile: top waits' <<<"$OUT" &&
  grep -qF 'wait 2.500s E1 E1.02 wait_last timeout CHAT answer' <<<"$OUT" &&
  grep -qF 'wait 0.125s E1 E1.01 wait_for ok first state' <<<"$OUT"; then
  ok "profile runner: docker env and cp, merged waits, longest-first top waits"
else
  bad "profile runner" "rc=$RC" "$OUT" "$(cat "$profile_out/waits.tsv" 2>&1)"
fi

rm -rf "$T/out"
STUB_ROW_FIXTURE="$T/row.fixture.tsv" LANE_PROFILE=1 run_sut --lanes E1 --root reuse
if [ "$RC" -eq 0 ] && grep -qF 'profile: ✗ lane E1 wrote no waits.tsv' <<<"$OUT"; then
  ok "profile runner: a missing lane waits file is named without changing exit status"
else
  bad "profile missing waits" "rc=$RC" "$OUT"
fi

# ---- 20: the capture starts after credential scan and before the first lane.
: >"$T/docker.log"
STUB_ROW_FIXTURE="$T/row.fixture.tsv" run_sut --lanes E1
capture_line="$(grep -n 'exec -d .*egress.sh run' "$T/docker.log" | head -1 | cut -d: -f1)"
scan_line="$(grep -n 'cred-scan.sh' "$T/docker.log" | head -1 | cut -d: -f1)"
lane_line="$(grep -n '/lanes/E1.sh' "$T/docker.log" | head -1 | cut -d: -f1)"
ready_line="$(grep -n 'test -e .*egress.ready' "$T/docker.log" | head -1 | cut -d: -f1)"
if [ "$RC" -eq 0 ] && [ -n "$capture_line" ] && [ -n "$ready_line" ] &&
  [ "$scan_line" -lt "$capture_line" ] && [ "$capture_line" -lt "$ready_line" ] &&
  [ "$ready_line" -lt "$lane_line" ] &&
  grep -q 'touch .*/egress.stop' "$T/docker.log" &&
  [ "$(tail -1 <<<"$OUT")" = 'EGRESS PASS fixture' ]; then
  ok "capture starts after credential scan, waits ready before the lane, then stops with PASS last"
else bad "capture order and PASS" "rc=$RC" "$OUT" "$(cat "$T/docker.log")"; fi

# ---- 21: no ready file is red, but the lane still runs.
: >"$T/docker.log"
STUB_EGRESS_READY=0 STUB_ROW_FIXTURE="$T/row.fixture.tsv" run_sut --lanes E1
if [ "$RC" -eq 1 ] &&
  grep -qF 'egress: ✗ NOT RECORDED — the capture did not start in 10s' <<<"$OUT" &&
  grep -q '/lanes/E1.sh' "$T/docker.log"; then
  ok "capture readiness timeout names NOT RECORDED and still runs the lane"
else bad "capture readiness timeout" "rc=$RC" "$OUT"; fi

# ---- 22: capture FAIL carries its DNS detail and fails the run.
fail_output=$'EGRESS FAIL 1 DNS queries, 0 outside destinations (fixture.pcap)\n  dns www.rfc-editor.org ×1 first 12:00:00Z'
STUB_EGRESS_OUTPUT="$fail_output" STUB_ROW_FIXTURE="$T/row.fixture.tsv" run_sut --lanes E1
if [ "$RC" -eq 1 ] &&
  grep -qF '  dns www.rfc-editor.org ×1' <<<"$OUT" &&
  [ "$(tail -1 <<<"$OUT")" = 'EGRESS FAIL 1 DNS queries, 0 outside destinations (fixture.pcap)' ]; then
  ok "FAIL prints DNS detail, ends on its verdict, and exits red"
else bad "capture FAIL" "rc=$RC" "$OUT"; fi

# ---- 23: NOT RECORDED is a red verdict.
STUB_EGRESS_OUTPUT='EGRESS NOT RECORDED — tcpdump not listening' STUB_ROW_FIXTURE="$T/row.fixture.tsv" run_sut --lanes E1
if [ "$RC" -eq 1 ] && [ "$(tail -1 <<<"$OUT")" = 'EGRESS NOT RECORDED — tcpdump not listening' ]; then
  ok "NOT RECORDED is the final line and exits red"
else bad "capture NOT RECORDED" "rc=$RC" "$OUT"; fi

# ---- 24: a capture that never writes a verdict is named and red.
STUB_EGRESS_VERDICT='' STUB_ROW_FIXTURE="$T/row.fixture.tsv" run_sut --lanes E1
if [ "$RC" -eq 1 ] &&
  [ "$(tail -1 <<<"$OUT")" = 'egress: ✗ NOT RECORDED — no verdict from egress.sh in 30s' ]; then
  ok "missing verdict is named and exits red"
else bad "capture missing verdict" "rc=$RC" "$OUT"; fi

# ---- 25: the recorder's artifacts are copied beside the lane output.
STUB_ROW_FIXTURE="$T/row.fixture.tsv" run_sut --lanes E1
capture_out="$(find "$T/out" -mindepth 1 -maxdepth 1 -type d | head -1)"
if [ "$RC" -eq 0 ] && [ -f "$capture_out/egress.out" ] &&
  [ -f "$capture_out/egress/fixture.pcap" ] && [ -f "$capture_out/egress/fixture.log" ]; then
  ok "capture output, pcap and recorder log are copied to the run output"
else bad "capture evidence copy" "rc=$RC" "$OUT"; fi

STUB_EGRESS_EVIDENCE_COPY_FAIL=1 STUB_ROW_FIXTURE="$T/row.fixture.tsv" run_sut --lanes E1
if [ "$RC" -eq 0 ] && grep -q 'egress: could not copy recorder evidence' <<<"$OUT" &&
  [ "$(tail -1 <<<"$OUT")" = 'EGRESS PASS fixture' ]; then
  ok "failed evidence copy is named without changing a PASS verdict"
else bad "capture evidence copy failure" "rc=$RC" "$OUT"; fi

shtest_end
