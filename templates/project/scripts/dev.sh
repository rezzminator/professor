#!/usr/bin/env bash
# dev.sh — Fast dev environment manager for {PROJECT_NAME}
#
# Usage:
#   ./dev.sh up           Start full dev environment (default)
#   ./dev.sh kill         Stop all dev servers
#   ./dev.sh restart [service]  Kill+start all, or bounce one roster server by key
#   ./dev.sh drop         Nuke Docker containers, rebuild, restart if servers were running
#   ./dev.sh fresh        Kill + drop + start — full clean slate, always starts servers
#   ./dev.sh status       Show what's running
#   ./dev.sh log [service] [N]  Show last N log lines (default: 50)
#   ./dev.sh clear-logs   Delete all logs
#   ./dev.sh export       Export DB tables to seeding/local/ (runs {DB_EXPORT_CMD})
#   ./dev.sh promote-demo Copy seeding/local/ → seeding/demo/ (review passwords.json after)
#
# This script replaces the slow Claude-interpreted /dev command with
# native bash that runs steps in parallel where possible.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# Scratch lives outside the tree: /tmp/<project>/dev, <project> = the repo
# directory name with any leading dot stripped.
DEV_PROJECT="$(basename "$ROOT")"; DEV_PROJECT="${DEV_PROJECT#.}"
DEV_DIR="/tmp/${DEV_PROJECT}/dev"
PID_FILE="${DEV_DIR}/dev-servers.pid"
ARCHIVE_DIR="${DEV_DIR}/archive"

# Dev servers run on a DEDICATED tmux socket — never the default socket, where an
# interactive Claude Code session lives. Isolating it means the dev lifecycle
# (kill / restart / fresh) can never reach the tmux server Claude runs under. The
# old shared-socket setup once let `/dev fresh` take a live Claude session down
# with it. View / attach dev sessions with:  tmux -L {PROJECT_NAME_LOWER}-dev ls
DEV_TMUX_SOCKET="{PROJECT_NAME_LOWER}-dev"

# ─── Project roster ───────────────────────────────────────────────
# SETUP fills this from the install interview — one server entry per project that
# runs a dev process. Infra-only projects (no long-running server) are omitted;
# they come up via the make targets in the Infrastructure step instead.
#
# Each entry is a pipe-delimited record:
#
#   key | label | dir | log | port_var | install_cmd | run_cmd | health
#
#   key         — short id used in the PID file, log filenames, and `log <svc>`
#                 (the roster entry's name, e.g. a, b, c). Must be unique.
#   label       — human label for report lines (the entry's {PROJECT_ROLE}).
#   dir         — project dir relative to repo root; "." for a single-project repo.
#   log         — log basename under $DEV_DIR (e.g. a.log).
#   port_var    — name of the port variable this server binds — the entry's
#                 `{PROJECT}_PORT` (A_PORT, B_PORT …), the same name alloc-ports.sh
#                 emits for it. Resolved indirectly at runtime.
#   install_cmd — dependency install command run in the project dir before start.
#                 "-" to skip.
#   run_cmd     — the dev-server command. ${PORT} expands to this server's port and
#                 ${PROFILE} to the active ISO profile (empty in main mode).
#   health      — how to probe readiness:
#                   http:/path  — HTTP 200 at http://localhost:${PORT}/path GREEN,
#                                 503 YELLOW (degraded/warming), else RED
#                   tcp         — any response on http://localhost:${PORT} = GREEN
#                   proc        — alive process is GREEN (no HTTP surface)
#
# Single-project collapse: a roster of one entry with dir "." runs at the repo root.
PROJECTS=(
  # {PROJECT_ROSTER} — SETUP expands one line per server-bearing roster entry, e.g.:
  # "{key}|{label}|{project}|{LOG_FILE}|{PORT_VAR}|{PROJECT_INSTALL_CMD}|{PROJECT_RUN_CMD}|{HEALTH_PROBE}"
)

# The roster entry whose Makefile owns infra (the docker {DATABASE} + {QUEUE} stack
# and its db-create / ready / nuke targets). SETUP pins the directory; "-" when no
# roster entry owns infra, which skips every infra step below.
INFRA_MAKE_DIR="{project}"

# Port discovery: if .dev-ports exists at project root, source it (isolated env).
# Otherwise use defaults (normal local dev).
DEV_PORTS_FILE="${ROOT}/.dev-ports"
if [ -f "$DEV_PORTS_FILE" ]; then
  # shellcheck disable=SC1090
  source "$DEV_PORTS_FILE"
  ISO_MODE=true
  # Read profile from .dev-ports comment (e.g., "# Profile: demo")
  ISO_PROFILE=$(grep "^# Profile:" "$DEV_PORTS_FILE" | sed 's/# Profile: //' | tr -d '[:space:]')
else
  # Defaults (main local dev). SETUP fills the per-project port defaults below from
  # the roster — one `: "${PORT_VAR:=default}"` per server entry.
  # {PORT_DEFAULTS} — one `: "${A_PORT:={PROJECT_PORT}}"` per server entry
  ISO_MODE=false
  ISO_PROFILE=""
fi

# Resolve a project's path. At roster size 1 the entry dir is ".", collapsing to ROOT.
proj_path() {
  local dir="$1"
  if [ "$dir" = "." ]; then echo "$ROOT"; else echo "${ROOT}/${dir}"; fi
}

# Indirect port lookup: port_of A_PORT → value of $A_PORT.
port_of() {
  echo "${!1}"
}

# Health-probe spec of a roster key (the entry's `health` field); empty when unknown.
health_of() {
  local entry key health
  for entry in "${PROJECTS[@]}"; do
    IFS='|' read -r key _ _ _ _ _ _ health <<< "$entry"
    [ "$key" = "$1" ] && { echo "$health"; return 0; }
  done
  return 0
}

# All bound ports across the roster — used by clean_ports / kill verification.
all_ports() {
  local entry port_var
  for entry in "${PROJECTS[@]}"; do
    IFS='|' read -r _ _ _ _ port_var _ _ _ <<< "$entry"
    port_of "$port_var"
  done
}

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()  { echo -e "${CYAN}[INFO]${NC}  $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
fail()  { echo -e "${RED}[FAIL]${NC}  $*"; }
header(){ echo -e "\n${BOLD}=== $* ===${NC}"; }

# Match severity fields and standalone failures, not words inside INFO payloads.
log_errors() {
  awk '
    {
      line = $0
      gsub(/\033\[[0-9;]*m/, "", line)
      lower = tolower(line)
      if (lower ~ /"level"[[:space:]]*:/) {
        if (lower ~ /"level"[[:space:]]*:[[:space:]]*("(error|fatal)"|50|60)([,}[:space:]])/) print
        next
      }
      if (match(line, /(^|[][[:space:]])(ERROR|FATAL|ERR!|INFO|DEBUG|TRACE|WARN|WARNING)([][[:space:]:]|$)/) ||
          match(line, /\[(error|fatal|info|debug|trace|warn|warning)[[:space:]]*\]/)) {
        severity = tolower(substr(line, RSTART, RLENGTH))
        if (severity ~ /(error|fatal|err!)/) print
        next
      }
      if (line ~ /^(Traceback \(most recent call last\):|([[:alnum:]_.]*(Error|Exception))(:|$)|ECONNREFUSED|EADDRINUSE|Cannot find module)/) print
    }
  ' "$1"
}

# ─── Helpers ──────────────────────────────────────────────────────

ensure_dirs() {
  mkdir -p "$DEV_DIR" "$ARCHIVE_DIR"
}

archive_logs() {
  local next_seq=1
  for f in "$ARCHIVE_DIR"/*.log; do
    [ -f "$f" ] || continue
    local num
    num=$(echo "$f" | sed 's/.*\.\([0-9]*\)\.log/\1/' | sed 's/^0*//')
    [ -z "$num" ] && num=0
    if [ "$num" -ge "$next_seq" ]; then
      next_seq=$((num + 1))
    fi
  done
  local seq_pad
  seq_pad=$(printf "%03d" "$next_seq")
  local entry log_name
  for entry in "${PROJECTS[@]}"; do
    IFS='|' read -r _ _ _ log_name _ _ _ _ <<< "$entry"
    local log="$DEV_DIR/$log_name"
    if [ -f "$log" ] && [ -s "$log" ]; then
      local base
      base=$(basename "$log" .log)
      mv "$log" "${ARCHIVE_DIR}/${base}.${seq_pad}.log"
    fi
  done
}

check_prereqs() {
  local missing=0
  # SETUP fills the prereq list from the roster's toolchains (node, package
  # managers, language runtimes, docker).
  for cmd in {DEV_PREREQS}; do
    if ! command -v "$cmd" &>/dev/null; then
      fail "Missing: $cmd"
      missing=1
    fi
  done
  if [ "$missing" -eq 1 ]; then
    echo "Install missing tools and retry."
    exit 1
  fi
}

wait_for() {
  local label="$1" check_cmd="$2" max_wait="${3:-15}"
  local elapsed=0
  while [ "$elapsed" -lt "$max_wait" ]; do
    if eval "$check_cmd" &>/dev/null; then
      ok "$label ready (${elapsed}s)"
      return 0
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  fail "$label not ready after ${max_wait}s"
  return 1
}

kill_tree() {
  local pid="$1"
  # Guard against invalid PIDs — kill 0 would signal the entire process group
  # (including this shell's parent), and kill 1 would target init. Both disasters.
  if [[ ! "$pid" =~ ^[0-9]+$ ]] || [ "$pid" -le 1 ]; then
    return 0
  fi
  if kill -0 "$pid" 2>/dev/null; then
    pkill -TERM -P "$pid" 2>/dev/null || true
    kill -TERM "$pid" 2>/dev/null || true
  fi
}

force_kill_tree() {
  local pid="$1"
  if [[ ! "$pid" =~ ^[0-9]+$ ]] || [ "$pid" -le 1 ]; then
    return 0
  fi
  pkill -9 -P "$pid" 2>/dev/null || true
  kill -9 "$pid" 2>/dev/null || true
}

# Recycling guard. macOS reuses PIDs aggressively, so a stale PID_FILE entry can
# point at an unrelated process by the time we go to kill it. We must NEVER signal
# our own ancestry — the Claude Code session, its tmux server, and this script's
# shell are all ancestors of $$. A real dev server is always a detached child of
# the dev tmux server (or a nohup orphan), never an ancestor, so this skips them
# correctly while making it impossible to kill the session that launched us.
is_ancestor() {
  local target="$1" p="$$"
  [[ "$target" =~ ^[0-9]+$ ]] || return 1
  while [ "$p" -gt 1 ]; do
    [ "$p" = "$target" ] && return 0
    p=$(ps -p "$p" -o ppid= 2>/dev/null | tr -d ' ')
    [[ "$p" =~ ^[0-9]+$ ]] || return 1
  done
  return 1
}

# A port answering a probe is not proof OUR process is alive — a stale listener
# from a prior run (not tracked in $PID_FILE) can answer the same probe. Health
# checks call this to require BOTH the port responding AND the PID this run
# recorded for the service being alive before reporting GREEN.
service_pid_alive() {
  local key="$1" pid
  pid=$(awk -v k="$key" '$1==k {print $2}' "$PID_FILE" 2>/dev/null | tail -1)
  [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null
}

# Seconds a live PID may keep its port silent before it counts as HUNG.
# Covers the slowest honest boot (a warm-up-heavy worker, first-run migrations).
HUNG_PORT_GRACE_SECS=180

pid_age_secs() {
  local pid="$1" raw d=0 h=0 m s a b c
  raw=$(ps -o etimes= -p "$pid" 2>/dev/null | tr -d ' ')
  if [[ "$raw" =~ ^[0-9]+$ ]]; then echo "$raw"; return 0; fi
  # BSD ps (macOS) has no etimes — parse etime's [[dd-]hh:]mm:ss instead.
  raw=$(ps -o etime= -p "$pid" 2>/dev/null | tr -d ' ')
  [[ "$raw" == *-* ]] && { d=${raw%%-*}; raw=${raw#*-}; }
  IFS=: read -r a b c <<< "$raw"
  if [ -n "$c" ]; then h=$a; m=$b; s=$c; else m=$a; s=$b; fi
  for a in "$d" "$h" "$m" "$s"; do [[ "$a" =~ ^[0-9]+$ ]] || return 1; done
  echo $(( (10#$d * 24 + 10#$h) * 3600 + 10#$m * 60 + 10#$s ))
}

# A live PID whose port is still silent past the boot horizon is HUNG — the
# process survived (a wedged bundler, a listener that died under a living
# wrapper) but nothing will ever answer. Age is the distinguishing signal: a
# younger silent PID is still starting and is left alone. An unreadable age
# reports NOT hung — the check never claims what it cannot measure — and a
# `proc` probe has no port to judge, so it never counts as hung.
service_hung() {
  local key="$1" pid="$2" port="$3" age code
  [ "$(health_of "$key")" != "proc" ] || return 1
  age=$(pid_age_secs "$pid") || return 1
  [ "$age" -ge "$HUNG_PORT_GRACE_SECS" ] || return 1
  # curl prints 000 itself on a refused port — a `|| echo 000` would double it.
  code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 3 "http://localhost:$port" 2>/dev/null) || code="000"
  [ "$code" = "000" ]
}

clean_ports() {
  local port
  for port in $(all_ports); do
    [ -n "$port" ] || continue
    lsof -ti :"$port" 2>/dev/null | xargs kill -9 2>/dev/null || true
  done
  # Kill {PROJECT_NAME_LOWER} processes scoped to THIS environment's directory.
  # Uses $ROOT to ensure main's kill doesn't murder ISO processes and vice versa.
  # IMPORTANT: main's ROOT is a prefix of ISO's ROOT (.worktrees/local/),
  # so we must EXCLUDE .worktrees/ matches when running from main.
  # {DEV_PROCESS_PATTERN} — SETUP fills the pgrep pattern from the roster's run commands.
  local pids
  pids=$(pgrep -f "{DEV_PROCESS_PATTERN}" 2>/dev/null || true)
  for pid in $pids; do
    local cmdline
    cmdline=$(ps -p "$pid" -o args= 2>/dev/null || true)
    if $ISO_MODE; then
      # ISO mode: only kill processes from THIS worktree (exact ROOT match)
      if echo "$cmdline" | grep -qF "$ROOT"; then
        kill -9 "$pid" 2>/dev/null || true
      fi
    else
      # Main mode: kill processes from main repo BUT NOT from any worktree
      if echo "$cmdline" | grep -qF "$ROOT" && ! echo "$cmdline" | grep -qF ".worktrees/"; then
        kill -9 "$pid" 2>/dev/null || true
      fi
    fi
  done
}

start_detached() {
  local name="$1" port="$2" workdir="$3" logfile="$4" cmd="$5" verb="${6:-starting}" label="${7:-$1}"
  local pid
  if command -v tmux &>/dev/null; then
    local session="{PROJECT_NAME_LOWER}-dev-${name}-${port}"
    local launch_cmd
    tmux -L "$DEV_TMUX_SOCKET" kill-session -t "$session" 2>/dev/null || true
    printf -v launch_cmd 'cd %q && exec %s >> %q 2>&1' "$workdir" "$cmd" "$logfile"
    tmux -L "$DEV_TMUX_SOCKET" new-session -d -s "$session" "$launch_cmd"
    pid=$(tmux -L "$DEV_TMUX_SOCKET" display-message -p -t "$session" '#{pane_pid}')
  else
    nohup bash -lc "cd \"$workdir\" && exec $cmd" > "$logfile" 2>&1 &
    pid=$!
  fi
  echo "$name $pid $port" >> "$PID_FILE"
  info "$label $verb (PID $pid, port $port)"
}

# Expand ${PORT} and ${PROFILE} in a roster run_cmd for a given server.
build_run_cmd() {
  local raw="$1" port="$2"
  local profile="${ISO_PROFILE:-}"
  raw="${raw//\$\{PORT\}/$port}"
  raw="${raw//\$\{PROFILE\}/$profile}"
  echo "$raw"
}

# Start one roster server by key. Looks the entry up, installs if needed, launches.
start_server() {
  local want="$1" verb="${2:-starting}"
  local entry key label dir log_name port_var install_cmd run_cmd health
  for entry in "${PROJECTS[@]}"; do
    IFS='|' read -r key label dir log_name port_var install_cmd run_cmd health <<< "$entry"
    [ "$key" = "$want" ] || continue
    local port workdir logfile cmd
    port="$(port_of "$port_var")"
    workdir="$(proj_path "$dir")"
    logfile="$DEV_DIR/$log_name"
    cmd="$(build_run_cmd "$run_cmd" "$port")"
    start_detached "$key" "$port" "$workdir" "$logfile" "$cmd" "$verb" "$label"
    return 0
  done
}

# Single source of truth for per-server kill — port-scoped (safe for ISO
# coexistence) plus a $ROOT-scoped sweep of any matching dev process the port
# kill missed (e.g. a queue consumer with no bound port). Used by the
# partial-failure resurrection block and cmd_restart_service so the kill logic
# lives in exactly one place. Takes a roster key; unknown key → fail.
kill_server() {
  local want="$1"
  local entry key port_var found=false
  for entry in "${PROJECTS[@]}"; do
    IFS='|' read -r key _ _ _ port_var _ _ _ <<< "$entry"
    [ "$key" = "$want" ] || continue
    found=true
    local port
    port="$(port_of "$port_var")"
    [ -n "$port" ] && lsof -ti :"$port" 2>/dev/null | xargs kill -9 2>/dev/null || true
    break
  done
  if ! $found; then
    fail "Unknown service: $want"
    return 1
  fi
  # Sweep any remaining matching processes scoped to this ROOT (ISO-vs-main
  # .worktrees/ exclusion mirrors clean_ports).
  local zpids zpid zcmd
  zpids=$(pgrep -f "{DEV_PROCESS_PATTERN}" 2>/dev/null || true)
  for zpid in $zpids; do
    zcmd=$(ps -p "$zpid" -o args= 2>/dev/null || true)
    if $ISO_MODE; then
      echo "$zcmd" | grep -qF "$ROOT" && kill -9 "$zpid" 2>/dev/null || true
    else
      echo "$zcmd" | grep -qF "$ROOT" && ! echo "$zcmd" | grep -qF ".worktrees/" && kill -9 "$zpid" 2>/dev/null || true
    fi
  done
}

# Health-probe one roster server by key. Echoes GREEN | YELLOW | RED and logs.
probe_health() {
  local want="$1"
  local entry key label dir log_name port_var install_cmd run_cmd health
  for entry in "${PROJECTS[@]}"; do
    IFS='|' read -r key label dir log_name port_var install_cmd run_cmd health <<< "$entry"
    [ "$key" = "$want" ] || continue
    local port
    port="$(port_of "$port_var")"
    case "$health" in
      http:*)
        local path="${health#http:}"
        local code
        code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:${port}${path}" 2>/dev/null || echo "000")
        if service_pid_alive "$key"; then
          if [ "$code" = "200" ]; then
            ok "$label healthy"; echo "GREEN"
          elif [ "$code" = "503" ]; then
            warn "$label degraded (warming up — self-heals in ~30s)"; echo "YELLOW"
          else
            warn "$label process alive but HTTP not yet responding (port $port)"; echo "YELLOW"
          fi
        elif [ "$code" = "200" ] || [ "$code" = "503" ]; then
          # A port answering while the PID this run recorded is dead is a
          # DIFFERENT process (a stale listener from a prior run) — never GREEN.
          fail "$label process we started is dead — port $port is answering for a DIFFERENT process"; echo "RED"
        else
          fail "$label health check failed (HTTP $code)"; echo "RED"
        fi
        ;;
      tcp)
        local responds=false
        if curl -sf "http://localhost:$port" -o /dev/null 2>/dev/null; then
          responds=true
        else
          sleep 3
          curl -sf "http://localhost:$port" -o /dev/null 2>/dev/null && responds=true
        fi
        if service_pid_alive "$key"; then
          if $responds; then
            ok "$label responding"; echo "GREEN"
          else
            warn "$label not yet responding (may still be bundling)"; echo "YELLOW"
          fi
        elif $responds; then
          fail "$label process we started is dead — port $port is answering for a DIFFERENT process"; echo "RED"
        else
          fail "$label not responding (process is dead, port is silent)"; echo "RED"
        fi
        ;;
      proc|*)
        if service_pid_alive "$key"; then
          ok "$label alive"; echo "GREEN"
        else
          fail "$label process dead"; echo "RED"
        fi
        ;;
    esac
    return 0
  done
}

# Emit per-server STATUS lines into the ---REPORT--- block.
report_statuses() {
  local entry key label dir log_name port_var rest
  for entry in "${PROJECTS[@]}"; do
    IFS='|' read -r key label dir log_name port_var rest <<< "$entry"
    local status
    status="$(probe_health "$key" | tail -1)"
    echo "$(echo "$key" | tr '[:lower:]' '[:upper:]')_STATUS=$status"
  done
}

# ─── MODE: KILL ───────────────────────────────────────────────────

cmd_kill() {
  header "Stopping dev servers"

  # Logs stay in $DEV_DIR after kill — user may want to inspect them.
  # Archival happens at next server start (cmd_up) so fresh logs begin clean.

  if [ -f "$PID_FILE" ]; then
    while read -r name pid port; do
      [ -z "$name" ] && continue
      if kill -0 "$pid" 2>/dev/null && ! is_ancestor "$pid"; then
        kill_tree "$pid"
        info "SIGTERM → $name (PID $pid, port $port)"
      elif is_ancestor "$pid"; then
        warn "$name (PID $pid) recycled onto this session's own process tree — refusing to signal it"
      else
        info "$name (PID $pid) already stopped"
      fi
    done < "$PID_FILE"

    sleep 2

    while read -r name pid port; do
      [ -z "$name" ] && continue
      is_ancestor "$pid" || force_kill_tree "$pid"
    done < "$PID_FILE"
  fi

  clean_ports
  rm -f "$PID_FILE"

  # Verify
  local all_clean=true
  local port
  for port in $(all_ports); do
    [ -n "$port" ] || continue
    if lsof -ti :"$port" &>/dev/null; then
      warn "Port $port still occupied"
      all_clean=false
    fi
  done

  if $all_clean; then
    ok "All ports clean"
  fi

  echo ""
  echo "KILL_RESULT=success"
}

# ─── MODE: UP ────────────────────────────────────────────────────

cmd_up() {
  ensure_dirs

  # Check for already running servers — every roster service must be
  # verified, not just the ones with a pid-file entry: a service absent from
  # $PID_FILE entirely (never started, or pruned by an earlier partial run)
  # is exactly as unstarted as a dead PID and must not be silently skipped by
  # a loop that only ever saw the lines that exist.
  if [ -f "$PID_FILE" ]; then
    local alive_count=0 dead_count=0
    local dead_names="" alive_entries=""
    while read -r name pid port; do
      [ -z "$name" ] && continue
      if kill -0 "$pid" 2>/dev/null && ! service_hung "$name" "$pid" "$port"; then
        alive_count=$((alive_count + 1))
        alive_entries="${alive_entries}${name} ${pid} ${port}\n"
      else
        # Dead, or alive-but-deaf past the boot horizon. A hung tree is killed
        # HERE — the resurrect pass below is port-scoped and would otherwise
        # start a second instance beside the deaf one.
        if kill -0 "$pid" 2>/dev/null && ! is_ancestor "$pid"; then
          warn "$name (PID $pid) alive but port $port silent past ${HUNG_PORT_GRACE_SECS}s — treating as hung"
          force_kill_tree "$pid"
        fi
        dead_count=$((dead_count + 1))
        dead_names="${dead_names} ${name}"
      fi
    done < "$PID_FILE"

    # Fold in roster keys with no line at all in $PID_FILE — same bucket as a
    # dead entry: both need the resurrect treatment below.
    local entry key
    for entry in "${PROJECTS[@]}"; do
      IFS='|' read -r key _ _ _ _ _ _ _ <<< "$entry"
      if ! grep -q "^${key} " "$PID_FILE"; then
        dead_count=$((dead_count + 1))
        dead_names="${dead_names} ${key}"
      fi
    done

    # ALREADY_RUNNING may only be claimed once every roster server has BOTH a
    # live recorded PID AND a responding probe — the same bar probe_health()
    # enforces everywhere else. A live PID whose port hasn't opened yet is
    # still starting, not proof there is nothing to do.
    local fully_verified=false
    if [ "$alive_count" -gt 0 ] && [ "$dead_count" -eq 0 ]; then
      fully_verified=true
      for entry in "${PROJECTS[@]}"; do
        IFS='|' read -r key _ _ _ _ _ _ _ <<< "$entry"
        [ "$(probe_health "$key" | tail -1)" = "GREEN" ] || { fully_verified=false; break; }
      done
    fi

    if $fully_verified; then
      # Every roster server verified up — nothing to do.
      echo "ALREADY_RUNNING=true"
      return 0
    elif [ "$alive_count" -gt 0 ]; then
      # Partial failure — some alive, some dead. Resurrect the fallen.
      # Deduplicate dead names (e.g., two stale entries → one resurrection)
      dead_names=$(echo "$dead_names" | tr ' ' '\n' | sort -u | tr '\n' ' ')
      warn "Partial failure detected: dead:${dead_names}"

      # Kill zombies for dead services via the single-source per-server kill
      # (port-scoped + $ROOT process sweep, safe for ISO coexistence).
      for dead_name in $dead_names; do
        kill_server "$dead_name"
      done
      sleep 1

      # Rewrite PID file with only alive entries
      echo -ne "$alive_entries" > "$PID_FILE"

      # Archive the dead service's log before overwriting
      for dead_name in $dead_names; do
        local dead_log
        dead_log="$(server_log "$dead_name")"
        if [ -n "$dead_log" ] && [ -f "$dead_log" ] && [ -s "$dead_log" ]; then
          local seq_pad
          seq_pad=$(printf "%03d" "$(( $(ls "$ARCHIVE_DIR"/*.log 2>/dev/null | wc -l | tr -d ' ') + 1 ))")
          local base
          base=$(basename "$dead_log" .log)
          mv "$dead_log" "${ARCHIVE_DIR}/${base}.${seq_pad}.log"
        fi
      done

      # Restart only dead services
      for dead_name in $dead_names; do
        start_server "$dead_name" "resurrected"
      done

      echo "PARTIAL_RESURRECT=true"
      echo "RESURRECTED=${dead_names}"
      # Fall through to health checks below
      sleep 4

      # Run health checks on all services (same as fresh start)
      echo ""
      echo "---REPORT---"
      local _statuses
      _statuses="$(report_statuses)"
      echo "$_statuses"
      emit_credentials
      echo "ERRORS=none"
      echo "---END---"

      # D2: a report is not a success — exit non-zero when any roster service
      # is RED, so a caller keying on exit code never reads a dead service as
      # a clean start.
      echo "$_statuses" | grep -q '_STATUS=RED' && return 1
      return 0
    else
      # All dead — clean up and proceed with full startup
      rm -f "$PID_FILE"
    fi
  fi

  archive_logs
  check_prereqs

  # ── Step 1: Infrastructure ──
  # No-op when no roster entry owns infra (INFRA_MAKE_DIR = "-").
  if [ "$INFRA_MAKE_DIR" != "-" ]; then
    header "Infrastructure"
    if $ISO_MODE; then
      info "Isolated mode — checking existing containers..."
      local infra_ok=true
      wait_for "{DATABASE} (${PG_PORT:-{DB_PORT}})" "docker exec ${DOCKER_PG_CONTAINER:-{PROJECT_NAME_LOWER}-postgres} pg_isready -U ${DB_USER:-postgres} -d ${DB_NAME:-{PROJECT_NAME_LOWER}}" 10 || infra_ok=false
      # Ready means the init scripts finished, not merely health-green — mirrors this
      # project's own ls-ready-local/ls-ready-test make targets (the health endpoint
      # goes green BEFORE the queue/bucket init scripts finish; a consumer that starts
      # on health-green alone can poll a queue that doesn't exist yet). ISO's {QUEUE}
      # runs on an arbitrary per-profile port those fixed-port make targets can't
      # address, so the same two-stage check is mirrored inline here.
      wait_for "{QUEUE} (${LS_PORT:-{QUEUE_PORT}})" "curl -sf http://localhost:${LS_PORT:-{QUEUE_PORT}}/_localstack/health >/dev/null && curl -sf http://localhost:${LS_PORT:-{QUEUE_PORT}}/_localstack/init | grep -Eq '\"READY\"[[:space:]]*:[[:space:]]*true'" 20 || infra_ok=false
      if ! $infra_ok; then
        fail "Isolated infrastructure not running — run '/dev iso init' first"
        exit 1
      fi
    else
      info "Starting {DATABASE} + {QUEUE}..."
      make -C "$(proj_path "$INFRA_MAKE_DIR")" up-local 2>&1 | tail -3

      local infra_ok=true
      wait_for "{DATABASE} ({DB_PORT})" "make -C '$(proj_path "$INFRA_MAKE_DIR")' pg-ready-local" 20 || infra_ok=false
      wait_for "{QUEUE} ({QUEUE_PORT})" "make -C '$(proj_path "$INFRA_MAKE_DIR")' ls-ready-local" 20 || infra_ok=false

      if ! $infra_ok; then
        fail "Infrastructure not ready — aborting"
        make -C "$(proj_path "$INFRA_MAKE_DIR")" ps-local
        exit 1
      fi
    fi
  fi

  # ── Step 2: Dependencies (parallel) ──
  header "Dependencies"
  local dep_pids=()
  local entry key dir install_cmd
  for entry in "${PROJECTS[@]}"; do
    IFS='|' read -r key _ dir _ _ install_cmd _ _ <<< "$entry"
    [ "$install_cmd" = "-" ] && continue
    local workdir
    workdir="$(proj_path "$dir")"
    (cd "$workdir" && eval "$install_cmd" 2>&1 | tail -1 && echo "${key}_DEPS=ok") &
    dep_pids+=($!)
  done

  local deps_ok=true
  for pid in "${dep_pids[@]}"; do
    if ! wait "$pid"; then
      deps_ok=false
    fi
  done

  if $deps_ok; then
    ok "All dependencies installed"
  else
    warn "Some dependency installs had issues — continuing"
  fi

  # ── Step 2b: Post-install integrity hooks ──
  # SETUP fills any project-specific post-install fixups here (e.g. a project's
  # node_modules spot-check + clean reinstall, a file-watcher reset). "-" / empty if none.
  # {POST_INSTALL_HOOKS}

  # ── Step 3: Per-project env bootstrap ──
  # SETUP fills any project-specific default-env-file creation here (e.g. writing a
  # project's default .env.local with DB URL + port). "-" / empty if none.
  # {ENV_BOOTSTRAP}

  # ── Step 4: Database ──
  # No-op when no roster entry owns infra (INFRA_MAKE_DIR = "-").
  if [ "$INFRA_MAKE_DIR" != "-" ]; then
    if $ISO_MODE; then
      header "Database"
      ok "ISO mode — schema applied during init (skipping migrations)"
    else
      header "Database"
      info "Checking database..."

      # Create the DB if needed (ignore error if it exists). Migrations run on the
      # app's own boot, not here — its boot-time migrator (advisory-lock +
      # checksum-guarded) is the single migration authority, writing an
      # applied_migrations ledger. A redundant pre-migration here would apply the
      # schema without ever writing that ledger, so the app's migrator would see an
      # empty ledger and re-run every file from 0001 on its own boot — safe only by
      # idempotency luck, and fatal the moment a later migration assumes a column an
      # earlier one already dropped.
      make -C "$(proj_path "$INFRA_MAKE_DIR")" db-create-local
      ok "Database created (migrations + seeding by the app on boot)"
    fi
  fi

  # ── Step 5: Start servers ──
  header "Starting servers"

  # Write PID file atomically — truncate first to prevent stale entries
  : > "$PID_FILE"

  for entry in "${PROJECTS[@]}"; do
    IFS='|' read -r key _ _ _ _ _ _ _ <<< "$entry"
    start_server "$key"
    # Boot-order fence — SETUP inserts a wait here, keyed to a specific roster entry,
    # when a LATER server in this loop would race THIS one to bootstrap shared-database
    # tables at boot (the service holding migration authority must answer healthy
    # before a racing consumer starts: the loser hits a "relation already exists"
    # error and aborts with an unwritten migration ledger — a half-migrated corpse,
    # safe only by idempotency luck). "-" / empty if the roster has no such race.
    # {BOOT_ORDER_FENCE}
  done

  # ── Step 6: Health checks ──
  header "Health checks"
  sleep 4

  echo ""
  echo "---REPORT---"
  local _statuses
  _statuses="$(report_statuses)"
  echo "$_statuses"
  emit_credentials

  # ── Step 7: Scan logs for errors ──
  local errors="" entry2 log_name2
  for entry2 in "${PROJECTS[@]}"; do
    IFS='|' read -r key2 _ _ log_name2 _ _ _ _ <<< "$entry2"
    local logfile="$DEV_DIR/$log_name2"
    if [ -f "$logfile" ]; then
      local svc_errors
      if ! svc_errors=$(log_errors "$logfile"); then
        svc_errors="Log scan failed: $logfile"
      fi
      if [ -n "$svc_errors" ]; then
        errors="${errors}\n  ${key2}: $(echo "$svc_errors" | head -1)"
      fi
    fi
  done

  if [ -n "$errors" ]; then
    echo -e "ERRORS=$errors"
  else
    echo "ERRORS=none"
  fi
  echo "---END---"

  # D2: a report is not a success — exit non-zero when any roster service is
  # RED, so a caller keying on exit code never reads a dead service as a
  # clean start.
  echo "$_statuses" | grep -q '_STATUS=RED' && return 1
  return 0
}

# Resolve a server's port / log by key (used by partial-resurrect path).
server_port() {
  local want="$1" entry key port_var
  for entry in "${PROJECTS[@]}"; do
    IFS='|' read -r key _ _ _ port_var _ _ _ <<< "$entry"
    [ "$key" = "$want" ] && { port_of "$port_var"; return 0; }
  done
}
server_log() {
  local want="$1" entry key log_name
  for entry in "${PROJECTS[@]}"; do
    IFS='|' read -r key _ _ log_name _ _ _ _ <<< "$entry"
    [ "$key" = "$want" ] && { echo "$DEV_DIR/$log_name"; return 0; }
  done
}

# Emit CREDENTIALS_FILE line if the seeding passwords file exists.
# {SEED_PROJECT} = the project dir holding seeding/ (or "-" if none).
emit_credentials() {
  [ "{SEED_PROJECT}" = "-" ] && { echo "CREDENTIALS_FILE=N/A"; return 0; }
  local _seed_dir="${ISO_PROFILE:-local}"
  local pw="$ROOT/{SEED_PROJECT}/seeding/$_seed_dir/passwords.json"
  if [ -f "$pw" ]; then
    echo "CREDENTIALS_FILE=$pw"
  else
    echo "CREDENTIALS_FILE=MISSING"
  fi
}

# ─── MODE: RESTART (single service) ──────────────────────────────
# Bounce ONE roster server by key — kill it (single-source kill_server), strip
# its stale PID line, relaunch it (single-source start_server), brief health.
# Roster-driven: works for any key in the PROJECTS array at roster size 1..N.

cmd_restart_service() {
  local svc="$1"
  ensure_dirs

  # Validate the key against the roster before doing anything.
  local entry key found=false
  for entry in "${PROJECTS[@]}"; do
    IFS='|' read -r key _ _ _ _ _ _ _ <<< "$entry"
    [ "$key" = "$svc" ] && { found=true; break; }
  done
  if ! $found; then
    fail "Unknown service: $svc"
    echo "RESTART_RESULT=fail"
    return 1
  fi

  header "Restarting $svc"

  if ! kill_server "$svc"; then
    echo "RESTART_RESULT=fail"
    return 1
  fi
  sleep 1

  # Strip this service's stale line(s) from the PID file — start_server
  # re-appends the fresh PID. The PID-file name is the roster key itself.
  if [ -f "$PID_FILE" ]; then
    grep -v "^${svc} " "$PID_FILE" > "$PID_FILE.tmp" 2>/dev/null || true
    mv "$PID_FILE.tmp" "$PID_FILE"
  fi

  start_server "$svc" "restarted"
  sleep 3

  echo "RESTART_RESULT=success"
  echo "RESTARTED=$svc"
}

# ─── MODE: DROP ──────────────────────────────────────────────────

cmd_drop() {
  ensure_dirs

  # Check if servers were running before we nuke everything
  local were_running=false
  if [ -f "$PID_FILE" ]; then
    while read -r name pid port; do
      [ -z "$name" ] && continue
      if kill -0 "$pid" 2>/dev/null; then
        were_running=true
        break
      fi
    done < "$PID_FILE"
  fi

  # Step 1: Kill servers if running
  if $were_running; then
    header "Killing servers before drop"
    cmd_kill
    echo ""
  fi

  # Step 2: Nuke Docker containers + volumes (skip when no roster entry owns infra)
  if [ "$INFRA_MAKE_DIR" = "-" ]; then
    echo "---REPORT---"
    echo "WERE_RUNNING=$were_running"
    echo "NUKE_RESULT=skipped (no roster entry owns infra)"
    echo "---END---"
    $were_running && { header "Restarting servers"; cmd_up; }
    return 0
  fi

  header "Nuking Docker containers"
  if make -C "$(proj_path "$INFRA_MAKE_DIR")" nuke-local 2>&1 | tail -5; then
    ok "Docker containers nuked"
  else
    fail "Failed to nuke Docker containers"
    echo "---REPORT---"
    echo "WERE_RUNNING=$were_running"
    echo "NUKE_RESULT=fail"
    echo "INFRA_RESULT=fail"
    echo "---END---"
    return 1
  fi

  # Step 3: Bring fresh containers up
  header "Rebuilding infrastructure"
  make -C "$(proj_path "$INFRA_MAKE_DIR")" up-local 2>&1 | tail -3

  local infra_ok=true
  wait_for "{DATABASE} ({DB_PORT})" "make -C '$(proj_path "$INFRA_MAKE_DIR")' pg-ready-local" 30 || infra_ok=false
  wait_for "{QUEUE} ({QUEUE_PORT})" "make -C '$(proj_path "$INFRA_MAKE_DIR")' ls-ready-local" 30 || infra_ok=false

  if ! $infra_ok; then
    fail "Infrastructure not ready after rebuild"
    echo "---REPORT---"
    echo "WERE_RUNNING=$were_running"
    echo "NUKE_RESULT=success"
    echo "INFRA_RESULT=fail"
    echo "---END---"
    return 1
  fi

  ok "Infrastructure rebuilt"

  # Step 4: Recreate database (the app migrates on boot — cmd_up starts it next)
  header "Database"
  make -C "$(proj_path "$INFRA_MAKE_DIR")" db-create-local
  ok "Database created (migrations + seeding by the app on boot)"

  # Step 5: Restart servers if they were running before
  if $were_running; then
    header "Restarting servers (were running before drop)"
    cmd_up
  else
    echo ""
    echo "---REPORT---"
    echo "WERE_RUNNING=false"
    echo "NUKE_RESULT=success"
    echo "INFRA_RESULT=success"
    echo "SERVERS_SKIPPED=true"
    echo "---END---"
  fi
}

# ─── MODE: FRESH ──────────────────────────────────────────────────

cmd_fresh() {
  ensure_dirs

  # Step 1: Kill servers unconditionally
  header "Killing servers"
  cmd_kill
  echo ""

  # Step 2: Nuke Docker containers + volumes (skip when no roster entry owns infra)
  if [ "$INFRA_MAKE_DIR" = "-" ]; then
    header "Starting servers"
    cmd_up
    return 0
  fi

  header "Nuking Docker containers"
  if make -C "$(proj_path "$INFRA_MAKE_DIR")" nuke-local 2>&1 | tail -5; then
    ok "Docker containers nuked"
  else
    fail "Failed to nuke Docker containers"
    echo "---REPORT---"
    echo "NUKE_RESULT=fail"
    echo "INFRA_RESULT=fail"
    echo "---END---"
    return 1
  fi

  # Step 3: Bring fresh containers up
  header "Rebuilding infrastructure"
  make -C "$(proj_path "$INFRA_MAKE_DIR")" up-local 2>&1 | tail -3

  local infra_ok=true
  wait_for "{DATABASE} ({DB_PORT})" "make -C '$(proj_path "$INFRA_MAKE_DIR")' pg-ready-local" 30 || infra_ok=false
  wait_for "{QUEUE} ({QUEUE_PORT})" "make -C '$(proj_path "$INFRA_MAKE_DIR")' ls-ready-local" 30 || infra_ok=false

  if ! $infra_ok; then
    fail "Infrastructure not ready after rebuild"
    echo "---REPORT---"
    echo "NUKE_RESULT=success"
    echo "INFRA_RESULT=fail"
    echo "---END---"
    return 1
  fi

  ok "Infrastructure rebuilt"

  # Step 4: Recreate database (the app migrates on boot — cmd_up starts it next)
  header "Database"
  make -C "$(proj_path "$INFRA_MAKE_DIR")" db-create-local
  ok "Database created (migrations + seeding by the app on boot)"

  # Step 5: Always start servers
  header "Starting servers"
  cmd_up
}

# ─── MODE: STATUS ────────────────────────────────────────────────

cmd_status() {
  header "Dev server status"

  if [ ! -f "$PID_FILE" ] || [ ! -s "$PID_FILE" ]; then
    echo "NO_SERVERS=true"
    return 0
  fi

  echo "---REPORT---"
  # Deduplicate PID entries — keep last entry per service name (handles stale duplicates)
  # Reverse lines (portable, no tac on macOS) so newest entry wins, awk deduplicates by name
  local _deduped
  _deduped=$(awk '{lines[NR]=$0} END {for(i=NR;i>=1;i--) print lines[i]}' "$PID_FILE" | awk 'NF && !seen[$1]++ {print}')
  while read -r name pid port; do
    [ -z "$name" ] && continue
    local alive="dead" responding="no"
    if kill -0 "$pid" 2>/dev/null; then alive="running"; fi
    if curl -sf "http://localhost:$port" -o /dev/null 2>/dev/null; then responding="yes"; fi
    echo "SVC=${name}|PID=${pid}|PORT=${port}|ALIVE=${alive}|RESPONDING=${responding}"
  done <<< "$_deduped"

  # Health checks — per roster server via its declared probe.
  local entry key
  for entry in "${PROJECTS[@]}"; do
    IFS='|' read -r key _ _ _ _ _ _ _ <<< "$entry"
    local h
    h="$(probe_health "$key" | tail -1)"
    echo "$(echo "$key" | tr '[:lower:]' '[:upper:]')_HEALTH=$h"
  done

  # API-surface probe + seed progress are project-specific. SETUP fills any extra
  # health probes (e.g. a GraphQL __typename ping, a seed-progress JSON parse) here.
  # {STATUS_EXTRA_PROBES}

  emit_credentials
  echo "---END---"
}

# ─── MODE: LOG ────────────────────────────────────────────────────

cmd_log() {
  local service="${1:-all}"
  local lines="${2:-50}"

  # Handle numeric first arg (e.g., /dev log 100)
  if [[ "$service" =~ ^[0-9]+$ ]]; then
    lines="$service"
    service="all"
  fi

  # Handle second arg as lines count
  if [ -n "${2:-}" ] && [[ "${2}" =~ ^[0-9]+$ ]]; then
    lines="$2"
  fi

  # Build the list of service keys to show.
  local keys=()
  if [ "$service" = "all" ]; then
    local entry key
    for entry in "${PROJECTS[@]}"; do
      IFS='|' read -r key _ _ _ _ _ _ _ <<< "$entry"
      keys+=("$key")
    done
  else
    keys=("$service")
  fi

  # Map a key → label/logfile from the roster.
  log_label() { local w="$1" e k l; for e in "${PROJECTS[@]}"; do IFS='|' read -r k l _ _ _ _ _ _ <<< "$e"; [ "$k" = "$w" ] && { echo "$l"; return; }; done; echo "$w"; }
  log_file()  { local w="$1" e k ln; for e in "${PROJECTS[@]}"; do IFS='|' read -r k _ _ ln _ _ _ _ <<< "$e"; [ "$k" = "$w" ] && { echo "$DEV_DIR/$ln"; return; }; done; echo "$DEV_DIR/$w.log"; }

  for svc in "${keys[@]}"; do
    local logfile label
    logfile="$(log_file "$svc")"
    label="$(log_label "$svc")"
    echo "━━━ $label ($logfile) ━━━ [last $lines lines]"
    if [ -f "$logfile" ] && [ -s "$logfile" ]; then
      tail -n "$lines" "$logfile"
    else
      echo "(no output yet)"
    fi
    echo ""
  done

  # Error summary
  echo "Error summary:"
  for svc in "${keys[@]}"; do
    local logfile label
    logfile="$(log_file "$svc")"
    label="$(log_label "$svc")"
    if [ -f "$logfile" ]; then
      local count matched
      if ! matched=$(log_errors "$logfile"); then
        echo "  $label: log scan failed ($logfile)"
        return 1
      fi
      count=$(printf '%s\n' "$matched" | awk 'NF { n++ } END { print n+0 }')
      if [ "$count" -gt 0 ]; then
        local first_err
        first_err=${matched%%$'\n'*}
        echo "  $label: $count error(s) — $first_err"
      else
        echo "  $label: no errors"
      fi
    else
      echo "  $label: (no log file)"
    fi
  done
}

# ─── MODE: LOG-CLEAR ─────────────────────────────────────────────

cmd_log_clear() {
  local cleared=0 entry log_name
  for entry in "${PROJECTS[@]}"; do
    IFS='|' read -r _ _ _ log_name _ _ _ _ <<< "$entry"
    local f="$DEV_DIR/$log_name"
    [ -f "$f" ] && rm -f "$f" && cleared=$((cleared + 1))
  done
  local archived
  archived=$(find "$ARCHIVE_DIR" -name "*.log" 2>/dev/null | wc -l | tr -d ' ')
  rm -f "$ARCHIVE_DIR"/*.log 2>/dev/null || true

  echo "CLEARED_CURRENT=$cleared"
  echo "CLEARED_ARCHIVE=$archived"
}

# ─── MODE: EXPORT ──────────────────────────────────────────────
# Seed export is project-specific (needs the seeding-owning project + its export
# command). No-op when the roster has no seed project ({SEED_PROJECT} = "-").

cmd_export() {
  if [ "{SEED_PROJECT}" = "-" ]; then
    echo "No seed project configured — nothing to export."
    return 0
  fi
  header "Exporting seed data"

  local env_file="$ROOT/{SEED_PROJECT}/.env.local"
  local seeding_name
  seeding_name=$(grep '^SEEDING_NAME=' "$env_file" 2>/dev/null | cut -d= -f2 | tr -d '"'"'" | head -1)
  seeding_name="${seeding_name:-local}"

  SEEDING_NAME="$seeding_name" cd "$ROOT/{SEED_PROJECT}" && SEEDING_NAME="$seeding_name" {DB_EXPORT_CMD}
}

# ─── MODE: PROMOTE-DEMO ──────────────────────────────────────────────

cmd_promote_demo() {
  if [ "{SEED_PROJECT}" = "-" ]; then
    echo "No seed project configured — nothing to promote."
    return 0
  fi
  header "Promoting local → demo dataset"
  local src="$ROOT/{SEED_PROJECT}/seeding/local"
  local dest="$ROOT/{SEED_PROJECT}/seeding/demo"

  # Copy all JSON files (overwrites demo — developer reviews passwords.json after)
  cp "$src"/*.json "$dest/"

  # Copy audio WAVs
  mkdir -p "$dest/audio"
  if [ -n "$(ls -A "$src/audio"/*.wav 2>/dev/null)" ]; then
    cp "$src/audio"/*.wav "$dest/audio/"
  fi

  local json_count audio_count
  json_count=$(ls -1 "$dest"/*.json 2>/dev/null | wc -l | tr -d ' ')
  audio_count=$(ls -1 "$dest/audio"/*.wav 2>/dev/null | wc -l | tr -d ' ')

  ok "Promoted: $json_count JSON files, $audio_count WAV files"
  warn "IMPORTANT: Review seeding/demo/passwords.json before committing."
  warn "Ensure the magic-login demo account is present."
}

# ─── MAIN ─────────────────────────────────────────────────────────

if [ "${#PROJECTS[@]}" -eq 0 ]; then
  fail "PROJECTS roster is empty — SETUP fills one entry per server-bearing roster project"
  exit 1
fi
ensure_dirs

case "${1:-up}" in
  up|start)        cmd_up ;;
  kill|stop|down)  cmd_kill ;;
  restart)         shift; if [ -n "${1:-}" ]; then cmd_restart_service "$1"; else cmd_kill; cmd_up; fi ;;
  drop)            cmd_drop ;;
  fresh)           cmd_fresh ;;
  status)          cmd_status ;;
  log|logs)        shift; cmd_log "$@" ;;
  clear-logs|cl)   cmd_log_clear ;;
  export)          cmd_export ;;
  promote-demo)    cmd_promote_demo ;;
  *)
    echo "Usage: $0 {up|kill|restart [service]|drop|fresh|status|log|clear-logs|export|promote-demo}"
    exit 1
    ;;
esac
