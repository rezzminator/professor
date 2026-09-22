#!/usr/bin/env bash
set -euo pipefail

# drain-wait.sh — blocking barrier: drain the queue of project {project}. Waits until
# that roster project's background queue consumer finishes ALL outstanding work, then
# RETURNS a result line and exits. Launch it backgrounded; the harness wakes the caller
# when it exits. A runtime without background execution blocks on it. Typical use:
# after seeding/enqueueing work, wait for the queue to fully drain before running the
# next step (tests, an assertion, a downstream stage).
#
# OPTIONAL — only for a roster with an asynchronous queue consumer (an LLM worker, a
# job runner) whose progress a health endpoint reports. A roster with no such project
# ships without this script; /dev FRESH skips the drain step.
#
# ── ADAPT PER PROJECT ─────────────────────────────────────────────────────────────
# This barrier polls a health endpoint that reports queue progress as JSON. Wire the
# jq paths in the poll loop below to YOUR endpoint's shape. The template expects:
#   .progress.status   → "complete" when drained | "in_progress" while working
#   .progress.done / .progress.total / .progress.remaining → integer counters
#   .services.worker.status / .services.db.status → "ok" when healthy
# HEALTH_URL below targets the project that serves that endpoint — its port is the
# project's `{PROJECT}_PORT` (the name alloc-ports.sh / dev.sh use), defaulting to
# its {PROJECT_PORT}.
# ──────────────────────────────────────────────────────────────────────────────────
#
# RESULT (printed to stdout — the caller reads it directly; launch backgrounded and
# the harness delivers stdout on exit):
#   DRAIN_RESULT=clean   exit 0  — progress.status == complete (all work done)
#   DRAIN_RESULT=error   exit 1  — a core service stayed down, work stalled, the health
#                                  endpoint stayed unreachable past boot grace, or jq
#                                  is missing (reason= names which)
#   DRAIN_RESULT=timeout exit 2  — MAX_WAIT elapsed without completing
#
# Every poll prints a heartbeat line to stdout, so the running process's captured
# output shows progress without waiting for exit — no status file, no tailing.
#
# Source of truth is PROGRESS, not log noise. A worker logs transient, retried errors
# and recovers — a logged error NEVER aborts the barrier on its own. Only a genuine
# stall (done count frozen for STALL_LIMIT while work remains), a sustained service
# outage, or the hard timeout end the wait. Errors seen are reported as context on a
# stall, never as a trigger.

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
PROJECT="$(basename "$REPO_ROOT")"; PROJECT="${PROJECT#.}"
LOG="${WORKER_LOG:-/tmp/$PROJECT/dev/worker.log}"
HEALTH_URL="http://localhost:${A_PORT:-{PROJECT_PORT}}/health"   # A_PORT → the health-serving project's {PROJECT}_PORT

POLL_INTERVAL="${POLL_INTERVAL:-15}"   # seconds between polls
MAX_WAIT="${MAX_WAIT:-5400}"           # 90 min hard cap — long sequential batches run long
STALL_LIMIT="${STALL_LIMIT:-900}"      # 15 min frozen while work remains = stall
SVC_FAIL_LIMIT="${SVC_FAIL_LIMIT:-4}"  # consecutive bad polls before erroring (debounce health-ping/curl flap)
BOOT_GRACE="${BOOT_GRACE:-180}"        # tolerate /health unreachable this long during early boot
ERR_RE='\[error[[:space:]]*\]|\[critical[[:space:]]*\]|Traceback \(most recent call last\)'

# emit: print to stdout (line-flushed) — the caller reads the captured output directly
emit() { printf '%s\n' "$*"; }

command -v jq >/dev/null || { emit "DRAIN_RESULT=error reason=jq-not-installed"; exit 1; }

start=$(date +%s)
log_start=$( [[ -f "$LOG" ]] && wc -l <"$LOG" || echo 0 )
last_done=-1
last_progress_at=$start
svc_fail=0
unreachable=0
last_sig=""                                       # last emitted heartbeat signature
polls_since_emit=0                                # keepalive counter
KEEPALIVE_EVERY="${KEEPALIVE_EVERY:-8}"           # emit a pulse at least every Nth poll (~2min) even if unchanged

emit "[drain] started — watching $HEALTH_URL (max ${MAX_WAIT}s, stall ${STALL_LIMIT}s)"

while :; do
  now=$(date +%s); elapsed=$(( now - start ))
  if (( elapsed > MAX_WAIT )); then emit "DRAIN_RESULT=timeout elapsed=${elapsed}s done=${last_done}"; exit 2; fi

  body=$(curl -s --max-time 10 "$HEALTH_URL" 2>/dev/null || echo '')
  if [[ -z "$body" ]] || ! printf '%s' "$body" | jq -e . >/dev/null 2>&1; then
    unreachable=$(( unreachable + 1 ))
    if (( elapsed > BOOT_GRACE )) && (( unreachable >= SVC_FAIL_LIMIT )); then
      emit "DRAIN_RESULT=error reason=health-unreachable url=$HEALTH_URL polls=$unreachable"; exit 1
    fi
    emit "[drain] health unreachable (${unreachable}/${SVC_FAIL_LIMIT}) elapsed=${elapsed}s"
    sleep "$POLL_INTERVAL"; continue
  fi
  unreachable=0

  # ADAPT: wire these jq paths to your health endpoint's queue-progress shape.
  prog_status=$(printf '%s' "$body" | jq -r '.progress.status // "unknown"')
  done_ct=$(printf '%s' "$body"    | jq -r '.progress.done // 0')
  total_ct=$(printf '%s' "$body"   | jq -r '.progress.total // 0')
  remaining=$(printf '%s' "$body"  | jq -r '.progress.remaining // 0')
  worker=$(printf '%s' "$body"     | jq -r '.services.worker.status // "unknown"')
  db=$(printf '%s' "$body"         | jq -r '.services.db.status // "unknown"')

  err_lines=0
  [[ -f "$LOG" ]] && err_lines=$(tail -n "+$((log_start+1))" "$LOG" 2>/dev/null | grep -aEc "$ERR_RE" || true)

  # Emit only when state changes (or every KEEPALIVE_EVERY polls for liveness) — a
  # heartbeat on every 15s poll bloats one run to hundreds of identical lines.
  sig="${prog_status}|${done_ct}/${total_ct}|${remaining}|${worker}|${db}|${err_lines}"
  polls_since_emit=$(( polls_since_emit + 1 ))
  if [[ "$sig" != "$last_sig" ]] || (( polls_since_emit >= KEEPALIVE_EVERY )); then
    emit "[drain] status=${prog_status} done=${done_ct}/${total_ct} remaining=${remaining} worker=${worker} errors_seen=${err_lines} elapsed=${elapsed}s"
    last_sig="$sig"; polls_since_emit=0
  fi

  # success takes priority over any transient service flap
  if [[ "$prog_status" == "complete" ]]; then
    emit "DRAIN_RESULT=clean done=${done_ct}/${total_ct} elapsed=${elapsed}s"; exit 0
  fi

  # sustained core-service outage (debounced — the worker flaps to unreachable while busy)
  if [[ "$db" != "ok" || "$worker" != "ok" ]]; then
    svc_fail=$(( svc_fail + 1 ))
    if (( svc_fail >= SVC_FAIL_LIMIT )); then
      emit "DRAIN_RESULT=error reason=service-down db=$db worker=$worker polls=$svc_fail"; exit 1
    fi
  else
    svc_fail=0
  fi

  # progress / stall — count advancing means healthy, no matter what the log says
  if (( done_ct != last_done )); then
    last_done=$done_ct; last_progress_at=$now
  elif [[ "$prog_status" == "in_progress" ]] && (( now - last_progress_at > STALL_LIMIT )); then
    emit "DRAIN_RESULT=error reason=stall done=${done_ct}/${total_ct} no_progress_for=$(( now - last_progress_at ))s errors_seen=${err_lines}"
    [[ -f "$LOG" && "$err_lines" -gt 0 ]] && tail -n "+$((log_start+1))" "$LOG" | grep -aE "$ERR_RE" | tail -5
    exit 1
  fi

  sleep "$POLL_INTERVAL"
done
