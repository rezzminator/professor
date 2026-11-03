#!/usr/bin/env bash
# One private fence machine, canonical lane order, and filesystem checkpoints.
# run.sh [--lanes LIST] [--from checkpoint:LANE] [--root reuse|rebuild] [--dry-run]
# Exit 1: assertions/map/budget red. Exit 2: usage, unavailable substrate, interrupted run.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
TREE="$(cd -- "$HERE/../../.." && pwd -P)"
DESIGN="$TREE/docs/design/pfm-integration-suite"
CANONICAL="$(python3 "$HERE/checkpoint.py" order)" || exit 2
WANT='' FROM='' ROOT_MODE=reuse DRY=0
COMMAND="$(printf '%q ' "$0" "$@")"
error() { printf 'journey ERROR %s\n' "$*" >&2; exit 2; }
while [ "$#" -gt 0 ]; do
  case "$1" in
    --lanes|--from|--root)
      [ "$#" -gt 1 ] || error "$1 requires a value"
      case "$1" in
        --lanes) WANT="$WANT ${2//,/ }" ;;
        --from) FROM="$2" ;;
        --root) ROOT_MODE="$2" ;;
      esac
      shift 2 ;;
    --dry-run) DRY=1; shift ;;
    *) error 'usage: run.sh [--lanes LIST] [--from checkpoint:LANE] [--root reuse|rebuild] [--dry-run]' ;;
  esac
done
case "$ROOT_MODE" in reuse|rebuild) ;; *) error '--root requires reuse|rebuild' ;; esac
python3 "$HERE/isolation.py" check || exit 2
for lane in $WANT; do
  case " $CANONICAL " in *" $lane "*) ;; *) error "unknown lane $lane" ;; esac
done
ORDER='' PENDING='' BEFORE=''
for lane in $CANONICAL; do
  if [ -n "$WANT" ]; then case " $WANT " in *" $lane "*) ;; *) continue ;; esac; fi
  LANE_PATH="$(python3 "$HERE/checkpoint.py" script "$HERE" "$lane")" || error 'lane source unavailable'
  if [ -f "$LANE_PATH" ]; then ORDER="${ORDER:+$ORDER }$lane"
  elif [ -n "$WANT" ]; then error "lane not written: $lane"
  else PENDING="${PENDING:+$PENDING }$lane"; fi
done
[ -n "$ORDER" ] || error 'no implemented lanes selected'
if [ -n "$FROM" ]; then
  case "$FROM" in checkpoint:*) FROM="${FROM#checkpoint:}" ;; *) error '--from requires checkpoint:LANE' ;; esac
  found=0
  for lane in $CANONICAL; do
    BEFORE="${BEFORE:+$BEFORE }$lane"
    [ "$lane" != "$FROM" ] || { found=1; break; }
  done
  [ "$found" -eq 1 ] || error "unknown checkpoint lane $FROM"
  # --from starts after the saved lane; that lane's state is already in the image.
  KEPT=''
  for lane in $ORDER; do case " $BEFORE " in *" $lane "*) ;; *) KEPT="${KEPT:+$KEPT }$lane" ;; esac; done
  ORDER="$KEPT"
  [ -n "$ORDER" ] || error 'checkpoint leaves no selected successor lane'
fi
MODE=sequence
case "$ORDER" in *' '*) ;; *) MODE=solo ;; esac
. "$HERE/container.sh" || error 'container substrate unavailable'
journey_fence_env "$TREE" || error 'fence mount resolution failed'
now_ms() { python3 "$HERE/runtime.py"; }
ROOT_STARTED="$(now_ms)"
if [ "$DRY" -eq 1 ]; then
  ROOT_PLAN="$(./.claude/scripts/dev.sh iso run 'bash infra/fence/journey/root.sh --print-hash')" || error 'root hash unavailable'
  ROOT_HASH="$(printf '%s\n' "$ROOT_PLAN" | sed -n '/^[0-9a-f]\{20\}$/p' | tail -n 1)"
else
  journey_cache_inventory || error 'existing root container inventory unavailable'
  ROOT_HASH_PLAN="$(./.claude/scripts/dev.sh iso run 'bash infra/fence/journey/root.sh --print-hash')" || error 'root hash unavailable'
  PREPARE_HASH="$(printf '%s\n' "$ROOT_HASH_PLAN" | sed -n '/^[0-9a-f]\{20\}$/p' | tail -n 1)"
  journey_cache_retain "$PREPARE_HASH" || error 'root cache retention failed'
  SUBJECT_BEFORE="$(python3 "$HERE/ledger.py" subject "$TREE")" || error 'initial subject unavailable'
  ROOT_ARGS=''; [ "$ROOT_MODE" != rebuild ] || ROOT_ARGS='--rebuild'
  ROOT_PLAN="$(PFM_FENCE_NET=shared ./.claude/scripts/dev.sh iso run "bash infra/fence/journey/root.sh $ROOT_ARGS")" || error 'in-fence root build failed'
  ROOT_HASH="$(printf '%s\n' "$ROOT_PLAN" | sed -n 's/^JOURNEY_ROOT_HASH=//p')"
  [ "$ROOT_HASH" = "$PREPARE_HASH" ] || error 'root inputs changed after cache preparation'
  ROOT_ARCHIVE="$(printf '%s\n' "$ROOT_PLAN" | sed -n 's/^JOURNEY_ROOT_ARCHIVE=//p')"
  [ "$(python3 "$HERE/ledger.py" subject "$TREE")" = "$SUBJECT_BEFORE" ] || error 'tree changed during root preparation'
  journey_cache_retain "$ROOT_HASH" || error 'root cache retention failed'
  journey_cache_images "$ROOT_PLAN" || error 'obsolete root image retirement failed'
fi
[ -n "$ROOT_HASH" ] || error 'root builder produced no hash'
printf 'journey root-preparation wall_ms=%s\n' "$(( $(now_ms) - ROOT_STARTED ))"
printf 'journey plan mode=%s order=%s root=%s root-mode=%s\n' "$MODE" "$ORDER" "$ROOT_HASH" "$ROOT_MODE"
[ -z "$PENDING" ] || printf 'journey NOT-WRITTEN %s\n' "$PENDING"
printf 'journey budget canonical walked cost_ms rows plus configured headroom; original section11 comparison and sequence target reported with measured totals\n'
[ "$DRY" -eq 0 ] || exit 0

OUT="$(mktemp -d /tmp/professor-journey.XXXXXXXX)" || error 'artifact directory unavailable'
TOKEN="${OUT##*.}"
RUN="$MODE-$ROOT_HASH-$(date -u +%Y%m%dT%H%M%S)-$TOKEN"
MACHINE="pfm-journey-$TOKEN"
RUN_ROOT="/j/$TOKEN"
PORT="$((20000 + $(printf '%s' "$TOKEN" | cksum | awk '{print $1}') % 40000))"
CACHE="/tmp/professor/journey-suite/journey-harness/checkpoints"
mkdir -p "$CACHE"
CREATED=0 ARTIFACTS_READY=0 HEADER=0 FINALIZED=0 STATUS=2
copy_artifacts() {
  if declare -F inside >/dev/null; then
    inside python3 /worktree/infra/fence/journey/artifacts.py || return 2
  fi
  docker cp "$MACHINE:$RUN_ROOT/out/." "$OUT/" >/dev/null
}
finish() {
  local status="$?"
  trap - EXIT INT TERM
  if [ "$CREATED" -eq 1 ]; then
    if [ "$ARTIFACTS_READY" -eq 1 ]; then
      copy_artifacts || { printf 'journey ERROR artifact copy failed\n' >&2; status=2; }
    fi
    docker rm -f "$MACHINE" >/dev/null 2>&1 || { printf 'journey ERROR container cleanup failed: %s\n' "$MACHINE" >&2; status=2; }
  fi
  if [ "$HEADER" -eq 1 ] && [ "$FINALIZED" -eq 0 ]; then
    python3 "$HERE/ledger.py" finish "$OUT" "$TREE" ERROR || true
  fi
  if [ -f "$OUT/ledger.tsv" ]; then
    mkdir -p "$DESIGN/runs"
    mkdir -p "$DESIGN/runs/$RUN"
    cp -a "$OUT/." "$DESIGN/runs/$RUN/" || status=2
    if [ -f "$OUT/$RUN.activity.jsonl" ]; then cp "$OUT/$RUN.activity.jsonl" "$DESIGN/runs/$RUN.activity.jsonl" || status=2; fi
    cp "$OUT/ledger.tsv" "$DESIGN/runs/$RUN.tsv" || status=2
  fi
  printf 'journey artifacts: %s\n' "$OUT"
  printf 'journey ledger: %s/runs/%s.tsv\n' "$DESIGN" "$RUN"
  exit "$status"
}
trap finish EXIT
trap 'exit 2' INT TERM

if [ -n "$FROM" ]; then
  KEY="$(python3 "$HERE/checkpoint.py" key "$ROOT_HASH" "$HERE" "$BEFORE")" || error 'checkpoint key unavailable'
  MANIFEST="$CACHE/$KEY.json"
  [ -r "$MANIFEST" ] || error "checkpoint absent for $FROM at this root and source hash"
  IMAGE="pfm-journey-checkpoint:$KEY"
  docker image inspect "$IMAGE" >/dev/null 2>&1 || error 'checkpoint image missing'
  cp "$CACHE/$KEY.env.json" "$OUT/previous-env.json" || error 'checkpoint environment missing'
  PRIOR_PORT="$(jq -er '.JOURNEY_MCP_PORT' "$OUT/previous-env.json")" || error 'checkpoint port unreadable'
  [ "$PORT" != "$PRIOR_PORT" ] || PORT="$((20000 + (PORT - 20000 + 1) % 40000))"
else
  IMAGE="$(journey_import "$ROOT_ARCHIVE" "$ROOT_HASH")" || error 'prepared root import failed'
fi
python3 "$HERE/isolation.py" "$RUN_ROOT" "$RUN" "$PORT" "$OUT/env.json" >"$OUT/env.sh" || error 'run environment invalid'
printf 'export JOURNEY_OUT=%q JOURNEY_ACTIVITY=%q JOURNEY_DESIGN=%q\n' \
  "$RUN_ROOT/out" "$RUN_ROOT/home/.local/state/pfm/log/pfm.jsonl" /worktree/docs/design/pfm-integration-suite >>"$OUT/env.sh"
JOURNEY_LEDGER_LOG="$RUN_ROOT/out/$RUN.activity.jsonl" JOURNEY_LEDGER_ARTIFACTS="$RUN" python3 "$HERE/ledger.py" begin "$OUT" "$TREE" "$ROOT_HASH" "$MODE" "$ORDER" "$COMMAND" \
  "$(sha256sum "$OUT/env.json" | cut -d' ' -f1)" "$BEFORE" || error 'ledger header failed'
HEADER=1
PRELUDE_STARTED="$(now_ms)"
journey_container "$MACHINE" "$IMAGE" >/dev/null || error 'private fence boot failed'
CREATED=1
journey_boot "$MACHINE" "$OUT" || error 'private machine boot failed'
journey_exec "$MACHINE" systemctl stop user@1000.service || error 'private user manager stop failed'
if [ -n "$FROM" ]; then
  docker cp "$OUT/previous-env.json" "$MACHINE:/tmp/journey-previous-env.json" || error 'checkpoint environment staging failed'
  docker cp "$OUT/env.json" "$MACHINE:/tmp/journey-current-env.json" || error 'run environment staging failed'
  journey_exec "$MACHINE" python3 /worktree/infra/fence/journey/relocate.py /tmp/journey-previous-env.json /tmp/journey-current-env.json || error 'checkpoint relocation failed'
  journey_exec "$MACHINE" python3 -c 'import json,pathlib,sys; root=pathlib.Path(sys.argv[1]); previous=json.loads(pathlib.Path("/tmp/journey-previous-env.json").read_text()); saved=root/"prior-runs"/previous["PFM_RUN"]/"out"; saved.parent.mkdir(parents=True); (root/"out").rename(saved); (root/"out").mkdir()' "$RUN_ROOT" || error 'checkpoint evidence isolation failed'
fi
journey_exec "$MACHINE" bash -c 'usermod -d "$1/home" journey && mkdir -p "$1/out" && chown -R 1000:1000 "$1"' _ "$RUN_ROOT" || error 'private home ownership failed'
ARTIFACTS_READY=1
docker cp "$OUT/env.sh" "$MACHINE:$RUN_ROOT/env.sh" || error 'environment staging failed'
docker cp "$OUT/env.json" "$MACHINE:$RUN_ROOT/env.json" || error 'environment JSON staging failed'
docker cp "$OUT/header.json" "$MACHINE:$RUN_ROOT/out/header.json" || error 'header staging failed'
docker cp "$OUT/ledger.tsv" "$MACHINE:$RUN_ROOT/out/ledger.tsv" || error 'ledger staging failed'
journey_exec "$MACHINE" chown -R 1000:1000 "$RUN_ROOT" || error 'artifact ownership failed'
inside_as() {
  local identity="$1"
  shift
  journey_exec -u "$identity" "$MACHINE" env -i PATH=/usr/local/bin:/usr/bin:/bin \
    bash -c 'source "$1/env.sh"; shift; exec "$@"' _ "$RUN_ROOT" "$@"
}
inside() { inside_as 1000:1000 "$@"; }
inside_root() { inside_as 0:0 "$@"; }
ROOT_PHASE=installed
if [ -n "$FROM" ]; then ROOT_PHASE="$(inside jq -er '.phase // "installed"' "$RUN_ROOT/carry.json")" || error 'checkpoint phase unavailable'; fi
if [ "$ROOT_PHASE" != uninstalled ]; then
  inside bash /worktree/infra/fence/journey/provision.sh service-env || error 'service environment preparation failed'
fi
printf 'journey endpoint container=%s root=%s env=%s/env.sh\n' "$MACHINE" "$RUN_ROOT" "$RUN_ROOT"
journey_exec "$MACHINE" systemctl start user@1000.service || error 'private user manager failed'
if [ -z "$FROM" ]; then
  inside bash /worktree/infra/fence/journey/provision.sh home || error 'root HOME preparation failed'
  inside_root python3 /worktree/infra/fence/journey/ssh.py start || error 'private SSH endpoint preparation failed'
  case "$ORDER" in
    machine-lifecycle|machine-lifecycle\ *)
      inside bash /worktree/infra/fence/journey/provision.sh install-previous || error 'previous release install failed' ;;
    *) inside bash /worktree/infra/fence/journey/provision.sh install-current || error 'current release prelude install failed' ;;
  esac
  inside python3 /worktree/infra/fence/journey/bootstrap.py boot || error 'root topology or sentinel failed'
else
  inside_root python3 /worktree/infra/fence/journey/ssh.py start || error 'checkpoint SSH endpoint preparation failed'
  if [ "$ROOT_PHASE" != uninstalled ]; then
    inside bash -c 'started=$(python3 /worktree/infra/fence/journey/runtime.py); (cd "$HOME/.professor" && pfm install --yes) >"$JOURNEY_OUT/checkpoint-install.out" 2>&1; status=$?; printf "%s\n" "$status" >"$JOURNEY_OUT/checkpoint-install.status"; printf "journey checkpoint-install wall_ms=%s\n" "$(( $(python3 /worktree/infra/fence/journey/runtime.py)-started ))" | python3 /worktree/infra/fence/journey/runtime.py tee "$JOURNEY_OUT/checkpoint-install.timing"; exit "$status"' || error 'checkpoint generated-resource recreation failed'
  fi
  inside python3 /worktree/infra/fence/journey/bootstrap.py resume || error 'checkpoint process restart failed'
fi
printf 'journey root-prelude from=%s wall_ms=%s\n' "${FROM:-root}" "$(( $(now_ms) - PRELUDE_STARTED ))"
STATUS=0
CHAIN=1
START="$(now_ms)"
printf '{}\n' >"$OUT/lanes.json"
for lane in $ORDER; do
  LANE_PATH="$(python3 "$HERE/checkpoint.py" script "$HERE" "$lane")" || error 'lane source unavailable'
  LANE_ARGUMENT="$(python3 "$HERE/checkpoint.py" argument "$HERE" "$lane")" || error 'lane argument unavailable'
  ROW_LANE="$(basename "$LANE_PATH" .sh)"
  ROW_START="$(inside python3 /worktree/infra/fence/journey/results.py count "$RUN_ROOT/out/ledger.tsv")" || error 'lane row start unavailable'
  T0="$(now_ms)"
  if inside env JOURNEY_LANE="$ROW_LANE" JOURNEY_STATE_LANE="$lane" bash "/worktree/infra/fence/journey/lanes/$(basename "$LANE_PATH")" ${LANE_ARGUMENT:+"$LANE_ARGUMENT"} 2>&1 | python3 "$HERE/runtime.py" tee "$OUT/$lane.log"; then RC=0
  else RC="$?"; STATUS=1; fi
  if ! inside env JOURNEY_LANE="$ROW_LANE" JOURNEY_STATE_LANE="$lane" python3 /worktree/infra/fence/journey/beatstate.py audit; then RC=1; STATUS=1; fi
  WALL="$(( $(now_ms) - T0 ))"
  ROW_END="$(inside python3 /worktree/infra/fence/journey/results.py count "$RUN_ROOT/out/ledger.tsv")" || error 'lane row end unavailable'
  jq --arg lane "$lane" --argjson wall "$WALL" --argjson exit "$RC" \
    --argjson start "$ROW_START" --argjson end "$ROW_END" \
    '. + {($lane): {wall_ms:$wall, exit:$exit,row_start:$start,row_end:$end}}' "$OUT/lanes.json" >"$OUT/lanes.next.json"
  mv "$OUT/lanes.next.json" "$OUT/lanes.json"
  if [ "$RC" -eq 0 ] && [ "$CHAIN" -eq 1 ]; then
    BEFORE="${BEFORE:+$BEFORE }$lane"
    KEY="$(python3 "$HERE/checkpoint.py" key "$ROOT_HASH" "$HERE" "$BEFORE")" || error 'checkpoint hash failed'
    inside python3 /worktree/infra/fence/journey/bootstrap.py snapshot "$lane" || error 'checkpoint observed receipt capture failed'
    SNAPSHOT_STARTED="$(now_ms)"
    journey_checkpoint_save "$MACHINE" "$KEY" || error 'checkpoint snapshot or cache retention failed'
    printf 'journey checkpoint-snapshot lane=%s wall_ms=%s\n' "$lane" "$(( $(now_ms) - SNAPSHOT_STARTED ))"
    jq -n --arg root "$RUN_ROOT" --arg order "$BEFORE" '{root:$root,order:$order}' >"$CACHE/$KEY.json"
    cp "$OUT/env.json" "$CACHE/$KEY.env.json"
  else
    CHAIN=0
    printf 'journey lane-red %s; next prelude runs in create mode\n' "$lane"
  fi
done
# A copied ledger is observed evidence; root builds and setup remain outside lane timing.
copy_artifacts || error 'final evidence copy failed'
python3 "$HERE/activity.py" export "$OUT" "$OUT/$RUN.activity.jsonl" || STATUS=1
python3 "$HERE/results.py" "$OUT" "$DESIGN" "$(( $(now_ms) - START ))" || STATUS=1
python3 "$HERE/ledger.py" finish "$OUT" "$TREE" "$([ "$STATUS" -eq 0 ] && echo PASS || echo FAIL)" || STATUS=1
FINALIZED=1
# The EXIT copy must not replace the host's completed hash-bound ledger.
docker cp "$OUT/ledger.tsv" "$MACHINE:$RUN_ROOT/out/ledger.tsv" >/dev/null || error 'final ledger staging failed'
docker cp "$OUT/header.json" "$MACHINE:$RUN_ROOT/out/header.json" >/dev/null || error 'final header staging failed'
docker cp "$OUT/$RUN.activity.jsonl" "$MACHINE:$RUN_ROOT/out/$RUN.activity.jsonl" >/dev/null || error 'final activity staging failed'
printf 'journey %s\n' "$([ "$STATUS" -eq 0 ] && echo PASS || echo FAIL)"
exit "$STATUS"
