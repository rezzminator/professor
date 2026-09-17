#!/usr/bin/env bash
# run.sh — the Tier B runner: ONE container per run, the selected lanes in
# canonical order inside it, over the pfm state they accumulate.
#
#   run.sh [--lanes E1,F,…] [--root reuse|rebuild] [--seats cc:1] [--dry-run]
#
# Canonical order is O1 → E1 → E2 → E3 → F → M → A → O2 whatever order --lanes
# names, because each lane reads the state the ones before it built (E1's named
# chat is alive when F storms, when M restarts the daemon, when A rewrites the
# adopter's hooks). One lane alone (`--lanes M`) starts a fresh container from
# the same root and runs that lane only — its `need` prelude makes the
# preconditions the sequence would have made. There is NO --parallel and no
# lane-level concurrency: concurrency is a scripted beat (storm, two writers),
# never a scheduling strategy, or every red row becomes order-dependent.
#
# Written per run, under tmp/lanes/<stamp>/:
#   <lane>.log     every beat line, the failed beats' raw pane bytes, log slices
#   timeline.tsv   lane · beat · t+s · verdict · dur · seat · ids · detail
#   lanes.tsv      lane · wall_s · beats · failed · known · blocked (Wave 2 shape)
#   summary.md     the header (mode, root image, order, seats), the table, verdicts
#
# Exit 1, each named: a ✗ that is not a valid known gap · a landscape id a beat
# declared that map.tsv does not carry · a lane over its budget · a lane with no
# budget row at all. Exit 2 for a usage error, an unwritten lane, a ledger that
# does not satisfy known-gaps law, or a toolchain that is missing.
#
# --dry-run prints the plan (root hash, reuse decision, lane order, beats and
# seats per lane, budgets) and executes NOTHING: no container, no model turn.
#
# --check-budget NAME WALL_S prints the one budget verdict the runner would
# print for a recorded wall and exits 1 on a red — the form lanes/tests/run_test.sh
# drives, and the form to ask "would 700s have breached E1?" by hand.
#
# Test seam (the pfm `PFM_*` idiom): $LANE_ROOT_SH overrides the root builder and
# $LANE_OUT_ROOT the artifact directory, so lanes/tests/run_test.sh can drive the
# whole planner with a docker stub on PATH. Neither is set in normal use.
set -uo pipefail

HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
ROOT="$(cd -- "$HERE/../../.." && pwd -P)"
CANONICAL="O1 E1 E2 E3 F M A O2"
BUDGETS="$HERE/budgets.yml"
MAP="$HERE/map.tsv"
PENDING="$HERE/pending.txt"
ROOT_SH="${LANE_ROOT_SH:-$HERE/root.sh}"
OUT_ROOT="${LANE_OUT_ROOT:-$ROOT/tmp/lanes}"

WANT="" ROOT_MODE=reuse SEATS="cc:1" DRY=0 CHECK_BUDGET=""
while [ $# -gt 0 ]; do
  case "$1" in
    --lanes) WANT="$WANT $(printf '%s' "$2" | tr ',' ' ')"; shift 2 ;;
    --root) ROOT_MODE="$2"; shift 2 ;;
    --seats) SEATS="$(printf '%s' "$2" | tr ',' ' ')"; shift 2 ;;
    --dry-run) DRY=1; shift ;;
    --check-budget) CHECK_BUDGET="$2 $3"; shift 3 ;;
    -h|--help) sed -n '2,8p' "$0"; exit 0 ;;
    *) echo "usage: run.sh [--lanes E1,F,…] [--root reuse|rebuild] [--seats cc:1] [--dry-run]" >&2; exit 2 ;;
  esac
done
case "$ROOT_MODE" in reuse|rebuild) ;; *) echo "run: --root takes reuse|rebuild, not '$ROOT_MODE'" >&2; exit 2 ;; esac

# The beat library is the one ledger parser: run.sh never re-reads known-gaps.yml
# with its own rules. Both variables below are read by lib.sh on the next line.
# shellcheck disable=SC2034
LANE_GAPS="$HERE/known-gaps.yml"
# shellcheck disable=SC2034
LANE_OUT_DIR="${TMPDIR:-/tmp}/lanes-runner-$$"
# shellcheck source=lib.sh
. "$HERE/lib.sh"

say() { printf '%s\n' "$1"; }
die() { printf 'run: %s\n' "$1" >&2; exit "${2:-2}"; }

# ─── lane selection, canonical order ────────────────────────────────────────

lane_known() { case " $CANONICAL " in *" $1 "*) return 0 ;; esac; return 1; }
lane_script() { printf '%s/%s.sh' "$HERE" "$1"; }

EXPLICIT=1
if [ -z "$(printf '%s' "$WANT" | tr -d ' ')" ]; then
  WANT="$CANONICAL"
  EXPLICIT=0
fi
for l in $WANT; do
  lane_known "$l" || die "unknown lane '$l' — the lanes are: $CANONICAL"
done
ORDER=""
for l in $CANONICAL; do
  case " $WANT " in *" $l "*) ORDER="$ORDER $l" ;; esac
done
ORDER="$(printf '%s' "$ORDER" | sed 's/^ //')"

# An unwritten lane: refused when it was NAMED (the caller asked for something
# that does not exist), dropped with a named line when the whole sequence was
# implied — a sequence that silently shrank would report coverage it never ran.
KEPT="" SKIPPED=""
for l in $ORDER; do
  if [ -f "$(lane_script "$l")" ]; then
    KEPT="$KEPT $l"
    continue
  fi
  if grep -qx "$l" "$PENDING" 2>/dev/null; then
    [ "$EXPLICIT" -eq 1 ] &&
      die "lane $l is NOT WRITTEN — $PENDING lists it as pending (spec build order step 2); nothing to run" 2
    SKIPPED="$SKIPPED $l"
    continue
  fi
  die "lane $l has no $(lane_script "$l") and is not listed in $PENDING — the lane set and the scripts disagree" 2
done
ORDER="$(printf '%s' "$KEPT" | sed 's/^ //')"
[ -n "$ORDER" ] || die "no selected lane is written yet (pending:$SKIPPED)" 2
COUNT="$(printf '%s\n' "$ORDER" | wc -w | tr -d ' ')"
MODE=sequence
[ "$COUNT" -eq 1 ] && MODE=solo

# ─── the known-gap ledger, before anything runs ─────────────────────────────

gaps_validate || die "the known-gap ledger is not valid (lines above) — fix it before a run" 2

# ─── budgets ────────────────────────────────────────────────────────────────

[ -f "$BUDGETS" ] || die "TOOLCHAIN-MISSING — $BUDGETS not found; every lane needs a budget row" 2
TOLERANCE="$(awk '/^tolerance:/ { print $2; exit }' "$BUDGETS")"
[ -n "$TOLERANCE" ] || die "$BUDGETS carries no tolerance: — the ×1.25 rule cannot be applied" 2
budget_for() { # budget_for <lane> — the value, or empty when the lane has no row
  awk -v want="$1" '
    /^lanes:/ { inl = 1; next }
    /^[a-z]/ { inl = 0 }
    inl && $1 ~ /:$/ { k = $1; sub(/:$/, "", k); if (k == want) { print $2; exit } }
  ' "$BUDGETS"
}
SEQ_BUDGET="$(awk '/^sequence:/ { print $2; exit }' "$BUDGETS")"
[ -n "$SEQ_BUDGET" ] || die "$BUDGETS carries no sequence: budget row" 2

# budget_verdict <name> <wall_s> <value> — prints the line, returns 1 on a red
budget_verdict() {
  local name="$1" wall="$2" value="$3" limit
  if [ -z "$value" ]; then
    printf 'budget: ✗ %s — UNBUDGETED: no row in %s (add one, `unpinned` until three green runs)\n' "$name" "$(basename "$BUDGETS")"
    return 1
  fi
  if [ "$value" = unpinned ]; then
    printf 'budget: %s unpinned — recorded, not judged: %ss\n' "$name" "$wall"
    return 0
  fi
  limit="$(awk -v v="$value" -v t="$TOLERANCE" 'BEGIN { printf "%d", v * t }')"
  if [ "$wall" -gt "$limit" ]; then
    printf 'budget: ✗ %s — %ss over budget %ss ×%s = %ss\n' "$name" "$wall" "$value" "$TOLERANCE" "$limit"
    return 1
  fi
  printf 'budget: ✓ %s — %ss within %ss (budget %ss ×%s)\n' "$name" "$wall" "$limit" "$value" "$TOLERANCE"
}

if [ -n "$CHECK_BUDGET" ]; then
  set -- $CHECK_BUDGET
  name="$1" wall="$2"
  case "$name" in sequence) value="$SEQ_BUDGET" ;; *) value="$(budget_for "$name")" ;; esac
  budget_verdict "$name" "$wall" "$value" || exit 1
  exit 0
fi

# ─── the root image ─────────────────────────────────────────────────────────

HASH="$(bash "$ROOT_SH" --print-hash 2>&1)" || die "root hash: $HASH" 2
IMAGE="pfm-lane-root:$HASH"
if [ "$ROOT_MODE" = rebuild ]; then
  ROOT_DECISION="REBUILD (--root rebuild)"
elif ! command -v docker >/dev/null; then
  ROOT_DECISION="UNKNOWN — docker is not on PATH (TOOLCHAIN-MISSING at run time)"
elif docker image inspect "$IMAGE" >/dev/null 2>&1; then
  ROOT_DECISION="REUSE (image present)"
else
  ROOT_DECISION="BUILD (no image for this hash)"
fi

beats_in() { grep -cE '^[[:space:]]*beat ' "$(lane_script "$1")" 2>/dev/null || echo 0; }
# The seat tokens a lane's `spends` lines declare. A token the lane resolves at
# run time stays the variable it is written as (`cc:$SEAT`), because the plan is
# printed before any container exists to resolve it.
seats_in() {
  local s
  s="$(grep -oE '^[[:space:]]*spends [^ ]+' "$(lane_script "$1")" 2>/dev/null |
    awk '{ print $2 }' | tr -d '"' | sort -u | tr '\n' ' ')"
  printf '%s' "${s:-none}"
}
prior_of() { # the lanes that run before <lane> in this run
  local l out=""
  for l in $ORDER; do
    [ "$l" = "$1" ] && break
    out="$out $l"
  done
  printf '%s' "$(printf '%s' "$out" | sed 's/^ //')"
}

STAMP="$(date +%Y%m%d-%H%M%S)"
OUT="$OUT_ROOT/$STAMP"

if [ "$DRY" -eq 1 ]; then
  say "run: PLAN (--dry-run — nothing was executed, no container, no model turn)"
  say "run: mode        $MODE"
  say "run: root hash   $HASH (pfm/**, templates/**, docs/SETUP.md, infra/fence/**)"
  say "run: root image  $IMAGE — $ROOT_DECISION"
  say "run: lane order  $(printf '%s' "$ORDER" | tr ' ' '>' | sed 's/>/ → /g')"
  say "run: seats       $SEATS"
  say "run: out dir     $OUT"
  [ -n "$SKIPPED" ] && say "run: NOT WRITTEN — pending lanes dropped from this plan:$SKIPPED"
  plan_reds=0
  for l in $ORDER; do
    verdict="$(budget_verdict "lane $l" 0 "$(budget_for "$l")")" || plan_reds=$((plan_reds + 1))
    say "lane $l · $(beats_in "$l") beats · spends $(seats_in "$l" | sed 's/ *$//') · $(printf '%s' "$verdict" | sed 's/^budget: //')"
  done
  verdict="$(budget_verdict sequence 0 "$SEQ_BUDGET")" || plan_reds=$((plan_reds + 1))
  say "run: sequence budget — $(printf '%s' "$verdict" | sed 's/^budget: //')"
  if [ "$plan_reds" -gt 0 ]; then
    say "run: ✗ $plan_reds lane(s) in this plan have no budget row — a run would exit 1 on them"
    exit 1
  fi
  exit 0
fi

# ─── the run ────────────────────────────────────────────────────────────────

command -v docker >/dev/null || die "TOOLCHAIN-MISSING — docker" 2
docker info >/dev/null 2>&1 || die "TOOLCHAIN-MISSING — the docker daemon is not reachable ('docker info' failed)" 2

mkdir -p "$OUT" || die "cannot write $OUT" 2
if [ "$ROOT_MODE" = rebuild ]; then
  bash "$ROOT_SH" --rebuild >/dev/null || die "the root image could not be rebuilt (lines above)" 1
elif ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  bash "$ROOT_SH" >/dev/null || die "the root image could not be built (lines above)" 1
fi
docker image inspect "$IMAGE" >/dev/null 2>&1 || die "no image $IMAGE after the root build — nothing to run" 1

# shellcheck source=container.sh
. "$HERE/container.sh"
FENCE_CALLER=lanes-run lane_fence_env "$ROOT"
CNAME="pfm-lane-$STAMP"
docker rm -f "$CNAME" >/dev/null 2>&1
lane_run "$CNAME" "$IMAGE" || die "the lane container would not start from $IMAGE" 1
trap 'docker rm -f "$CNAME" >/dev/null 2>&1' EXIT

say "run: $MODE · container $CNAME · image $IMAGE ($ROOT_DECISION) · seats $SEATS · out $OUT"
[ -n "$SKIPPED" ] && say "run: NOT WRITTEN — pending lanes not run:$SKIPPED"
CONT_OUT="/tmp/lanes/$STAMP"
failed_lanes="" missing_rows=""
for l in $ORDER; do
  prior="$(prior_of "$l")"
  say "── lane $l${prior:+ (after $prior)}"
  docker exec -w /tmp \
    -e "LANE_MODE=$MODE" -e "LANE_PRIOR=$prior" -e "LANE_SEATS=$SEATS" \
    -e "LANE_OUT_DIR=$CONT_OUT" -e "LANE_STAMP=$STAMP" -e IS_SANDBOX=1 \
    "$CNAME" bash "/worktree/infra/fence/lanes/$l.sh" 2>&1 | tee "$OUT/$l.stream.log"
  rc="${PIPESTATUS[0]}"
  for f in "$l.log" "$l.timeline.tsv" "$l.row.tsv" "$l.logstate" "$l.seats"; do
    docker cp "$CNAME:$CONT_OUT/$f" "$OUT/$f" >/dev/null 2>&1
  done
  if [ ! -f "$OUT/$l.row.tsv" ]; then
    say "run: ✗ lane $l produced no result row (exit $rc) — it died before lane_end; its stream is $OUT/$l.stream.log"
    missing_rows="$missing_rows $l"
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$l" 0 0 1 0 0 >"$OUT/$l.row.tsv"
  fi
  [ "$rc" -ne 0 ] && failed_lanes="$failed_lanes $l"
done

# ─── aggregation ────────────────────────────────────────────────────────────

printf 'lane\twall_s\tbeats\tfailed\tknown\tblocked\n' >"$OUT/lanes.tsv"
printf 'lane\tbeat\tt_plus_s\tverdict\tdur_s\tseat\tlandscape_ids\tdetail\n' >"$OUT/timeline.tsv"
for l in $ORDER; do
  [ -f "$OUT/$l.row.tsv" ] && cat "$OUT/$l.row.tsv" >>"$OUT/lanes.tsv"
  [ -f "$OUT/$l.timeline.tsv" ] && tail -n +2 "$OUT/$l.timeline.tsv" >>"$OUT/timeline.tsv"
done

total_wall=0 total_failed=0 total_known=0 total_blocked=0 total_beats=0
while IFS=$'\t' read -r l wall beats failed known blocked; do
  total_wall=$((total_wall + wall)) total_beats=$((total_beats + beats))
  total_failed=$((total_failed + failed)) total_known=$((total_known + known)) total_blocked=$((total_blocked + blocked))
done < <(tail -n +2 "$OUT/lanes.tsv")

# Every landscape id a beat declared must have its row in map.tsv — the beat
# side of the map gate (check-map.sh walks the landscape side).
unmapped=""
while IFS=$'\t' read -r l beat _t _v _d _s ids _detail; do
  for id in $ids; do
    awk -F'\t' -v i="$id" -v l="$l" -v b="$beat" \
      '$1 == i && $2 == l && $3 == b { found = 1 } END { exit(found ? 0 : 1) }' "$MAP" && continue
    unmapped="$unmapped $id($l/$beat)"
  done
done < <(tail -n +2 "$OUT/timeline.tsv")

budget_reds=0 budget_lines=""
for l in $ORDER; do
  wall="$(awk -F'\t' -v l="$l" '$1 == l { print $2; exit }' "$OUT/lanes.tsv")"
  line="$(budget_verdict "lane $l" "${wall:-0}" "$(budget_for "$l")")" || budget_reds=$((budget_reds + 1))
  budget_lines="$budget_lines$line
"
done
if [ "$MODE" = sequence ]; then
  line="$(budget_verdict sequence "$total_wall" "$SEQ_BUDGET")" || budget_reds=$((budget_reds + 1))
  budget_lines="$budget_lines$line
"
fi

log_absent=""
for l in $ORDER; do
  [ "$(cat "$OUT/$l.logstate" 2>/dev/null)" = ABSENT ] && log_absent="$log_absent $l"
done

{
  printf '# Tier B lane run %s\n\n' "$STAMP"
  printf -- '- mode: **%s**%s\n' "$MODE" "$( [ "$MODE" = sequence ] && printf ' (each lane reads the state the lanes before it built)' )"
  printf -- '- lane order: %s\n' "$(printf '%s' "$ORDER" | sed 's/ / → /g')"
  for l in $ORDER; do
    prior="$(prior_of "$l")"
    printf -- '  - %s — ran after: %s\n' "$l" "${prior:-nothing (first in this run)}"
  done
  printf -- '- root image: `%s` (%s)\n' "$IMAGE" "$ROOT_DECISION"
  printf -- '- seats: %s\n' "$SEATS"
  [ -n "$SKIPPED" ] && printf -- '- NOT WRITTEN (pending.txt), not run:%s\n' "$SKIPPED"
  printf -- '- totals: %s beats · %s failed · %s known-gap · %s blocked · %ss wall\n\n' \
    "$total_beats" "$total_failed" "$total_known" "$total_blocked" "$total_wall"
  printf '| lane | wall_s | beats | failed | known | blocked |\n|---|---|---|---|---|---|\n'
  tail -n +2 "$OUT/lanes.tsv" | awk -F'\t' '{ printf "| %s | %s | %s | %s | %s | %s |\n", $1, $2, $3, $4, $5, $6 }'
  printf '\n'
  printf '%s' "$budget_lines"
  [ -n "$unmapped" ] && printf 'map: ✗ unmapped landscape id(s):%s\n' "$unmapped"
  [ -n "$missing_rows" ] && printf 'run: ✗ lane(s) with no result row:%s\n' "$missing_rows"
  if [ -n "$log_absent" ]; then
    printf 'activity log: ABSENT (Wave 6 not landed) — log assertions not enforced [lanes:%s]\n' "$log_absent"
  else
    printf 'activity log: present — every beat judged on its slice as well as its assertion\n'
  fi
  printf '\nRead a red row: find it in `timeline.tsv` (lane · beat · t+s), then that beat in `<lane>.log` — the raw pane bytes are beside the assertion.\n'
} >"$OUT/summary.md"

printf '%s' "$budget_lines"
[ -n "$unmapped" ] && say "map: ✗ unmapped landscape id(s):$unmapped — every id a beat declares needs a map.tsv row"
if [ -n "$log_absent" ]; then
  say "activity log: ABSENT (Wave 6 not landed) — log assertions not enforced"
fi
say "run: $total_beats beats · $total_failed failed · $total_known known-gap · $total_blocked blocked · ${total_wall}s · $OUT/summary.md"

status=0
[ -n "$failed_lanes" ] && { say "run: ✗ lane(s) with a failing beat:$failed_lanes"; status=1; }
[ -n "$unmapped" ] && status=1
[ "$budget_reds" -gt 0 ] && { say "run: ✗ $budget_reds budget verdict(s) red"; status=1; }
exit "$status"
