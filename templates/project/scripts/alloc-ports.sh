#!/usr/bin/env bash
# alloc-ports.sh — Allocate unique ports for a worktree pipeline
#
# Usage:
#   ./.claude/scripts/alloc-ports.sh alloc <worktree-id>   → prints one {NAME}_PORT=N line per port name
#   ./.claude/scripts/alloc-ports.sh free  <worktree-id>   → releases the allocation
#   ./.claude/scripts/alloc-ports.sh list                   → shows all allocations
#
# Port names — one port per roster project, plus any service port the adopter declares:
#   PROJECTS=(a b c)             the roster, in order (SETUP fills it; one entry is a valid roster)
#   EXTRA_PORTS=(TEST_DB …)      optional service ports beyond the roster (a test database,
#                                a queue emulator …) — declared here, never assumed
# Emitted contract: for every name — roster first, then extras — uppercased and
# shell-var-safe, one line `{NAME}_PORT=N`, e.g.
#   A_PORT=3001
#   B_PORT=3101
#   TEST_DB_PORT=3201
#
# Port scheme: name i (0-based) at slot s binds  PORT_BASE + i*PORT_STRIDE + s
# (slots 0..MAX_SLOTS-1, MAX_SLOTS <= PORT_STRIDE), so every name owns a disjoint
# range of PORT_STRIDE ports. SETUP pins PORT_BASE / PORT_STRIDE to a free region of
# the dev machine; main's own dev ports ({PORT_DEFAULTS} in dev.sh) stay outside it.
#
# Registry: .worktrees/.ports — one line per allocation: `id` then one port per name, in order.
#
# Broken states this script reports (stderr, exit 1): an empty PROJECTS roster; two
# names that collide once uppercased; MAX_SLOTS wider than PORT_STRIDE; a registry
# line whose port count no longer matches the declared names; no free slot; a port
# whose host occupancy could not be inspected (neither ss nor lsof usable).

set -euo pipefail

# ROOT resolves to the ONE canonical checkout — the shared common repo, never a worktree's
# own physical location — because ports are a machine-global resource: several pipeline
# lanes on one devbox all allocate from this SAME file, and each git worktree carries its
# own copy of this script. `git rev-parse --path-format=absolute --git-common-dir` returns
# the shared main checkout's .git dir from ANY worktree (same idiom the Makefile's
# PORTS_REGISTRY already uses); the physical
# dirname($0)-based resolution this used to use instead gave each worktree its OWN nested
# .worktrees/.ports — invisible to Make's canonical resolution and to every other worktree —
# BUG-PIPELINE-PORTS-REGISTRY-MISMATCH.
SCRIPT_CHECKOUT_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
GIT_COMMON_DIR="$(git -C "$SCRIPT_CHECKOUT_ROOT" rev-parse --path-format=absolute --git-common-dir 2>/dev/null || echo "$SCRIPT_CHECKOUT_ROOT/.git")"
ROOT="$(dirname "$GIT_COMMON_DIR")"
REGISTRY="${ROOT}/.worktrees/.ports"
LOCKFILE="${REGISTRY}.lock"

# ─── Port names ───────────────────────────────────────────────────
PROJECTS=(
  # {PROJECT_ROSTER} — SETUP expands one name per roster entry, in order, e.g.:
  # a b c
)
EXTRA_PORTS=(
  # Adopter-declared service ports beyond the roster, e.g.:
  # TEST_DB TEST_QUEUE
)
PORT_BASE=3001
PORT_STRIDE=100
MAX_SLOTS=99

# Shell-var-safe uppercase form of a name: a → A, my-svc → MY_SVC.
var_name() {
  printf '%s' "$1" | tr '[:lower:]' '[:upper:]' | sed 's/[^A-Z0-9_]/_/g'
}

# Resolve PROJECTS + EXTRA_PORTS into PORT_NAMES / NAME_COUNT / NCOLS; every broken
# declaration exits 1 here, before any registry read or write.
PORT_NAMES=()
NAME_COUNT=0
NCOLS=1
load_names() {
  local name dupes
  if [ "${#PROJECTS[@]}" -eq 0 ]; then
    echo "Error: PROJECTS roster is empty — SETUP fills it with one name per roster project" >&2
    exit 1
  fi
  if [ "$MAX_SLOTS" -gt "$PORT_STRIDE" ]; then
    echo "Error: MAX_SLOTS ($MAX_SLOTS) exceeds PORT_STRIDE ($PORT_STRIDE) — adjacent names would overlap" >&2
    exit 1
  fi
  for name in "${PROJECTS[@]}" ${EXTRA_PORTS[@]+"${EXTRA_PORTS[@]}"}; do
    PORT_NAMES+=("$(var_name "$name")")
  done
  NAME_COUNT="${#PORT_NAMES[@]}"
  NCOLS=$((NAME_COUNT + 1))
  dupes=$(printf '%s\n' "${PORT_NAMES[@]}" | sort | uniq -d | tr '\n' ' ')
  if [ -n "$dupes" ]; then
    echo "Error: port names collide once uppercased: ${dupes}" >&2
    exit 1
  fi
}

mkdir -p "$(dirname "$REGISTRY")"
touch "$REGISTRY"

acquire_lock() {
  local tries=0
  while ! mkdir "$LOCKFILE" 2>/dev/null; do
    tries=$((tries + 1))
    if [ "$tries" -gt 50 ]; then
      echo "Error: could not acquire port lock after 5s" >&2
      exit 1
    fi
    sleep 0.1
  done
  trap 'rmdir "$LOCKFILE" 2>/dev/null' EXIT
}

# emit_ports p1 p2 … → one {NAME}_PORT=N line per name, in order.
emit_ports() {
  local i=0
  for port in "$@"; do
    echo "${PORT_NAMES[$i]}_PORT=${port}"
    i=$((i + 1))
  done
}

cmd_alloc() {
  local id="$1"
  load_names
  acquire_lock

  # Already allocated? (this script is the sole registry writer)
  local existing
  existing=$(awk -v id="$id" '$1 == id { print $0 }' "$REGISTRY")
  if [ -n "$existing" ]; then
    local existing_cols
    existing_cols=$(echo "$existing" | awk '{print NF}')
    if [ "$existing_cols" -ne "$NCOLS" ]; then
      echo "Error: registry line for '${id}' carries $((existing_cols - 1)) ports but ${NAME_COUNT} names are declared — the roster changed after allocation; run '$0 free ${id}' and re-allocate" >&2
      exit 1
    fi
    # shellcheck disable=SC2046
    emit_ports $(echo "$existing" | cut -d' ' -f2-)
    return 0
  fi

  # An existing registry entry is an idempotent reservation, not a liveness probe.
  # A later host bind may still make its consumer fail, but reallocation would break callers.

  host_tcp_port_state() {
    local port="$1"
    local output status

    if command -v ss >/dev/null 2>&1; then
      if output=$(ss -H -ltn "sport = :${port}" 2>&1); then
        [ -n "$output" ] && return 1
        return 0
      fi
      echo "Error: could not inspect TCP listener occupancy for port ${port} with ss: ${output}" >&2
      return 2
    fi

    if command -v lsof >/dev/null 2>&1; then
      if output=$(lsof -nP -iTCP:"${port}" -sTCP:LISTEN 2>&1); then
        return 1
      else
        status=$?
      fi
      if [ "$status" -eq 1 ] && [ -z "$output" ]; then
        return 0
      fi
      echo "Error: could not inspect TCP listener occupancy for port ${port} with lsof: ${output}" >&2
      return 2
    fi

    echo "Error: cannot inspect TCP listener occupancy: neither ss nor lsof is available" >&2
    return 2
  }

  tuple_is_host_free() {
    local port state
    for port in "$@"; do
      if host_tcp_port_state "$port"; then
        continue
      else
        state=$?
      fi
      case "$state" in
        1) return 1 ;;
        2) return 2 ;;
        *)
          echo "Error: unexpected TCP listener inspection state for port ${port}: ${state}" >&2
          return 2
          ;;
      esac
    done
  }

  # A candidate tuple is reserved if ANY of its ports appears in ANY registry column.
  tuple_is_unreserved() {
    awk -v ports="$*" '
      BEGIN { n = split(ports, P, " ") }
      { for (c = 2; c <= NF; c++) for (k = 1; k <= n; k++) if ($c == P[k]) found = 1 }
      END { exit found }
    ' "$REGISTRY"
  }

  # Pin: an adversarial test starts an unregistered host listener and proves its tuple is
  # rejected. This remains a TOCTOU snapshot: the registry lock serializes allocators, not
  # later OS binds.
  local slot=0 inspection_state i
  while [ "$slot" -lt "$MAX_SLOTS" ]; do
    local tuple=()
    i=0
    while [ "$i" -lt "$NAME_COUNT" ]; do
      tuple+=($((PORT_BASE + i * PORT_STRIDE + slot)))
      i=$((i + 1))
    done
    if tuple_is_unreserved "${tuple[@]}"; then
      if tuple_is_host_free "${tuple[@]}"; then
        echo "${id} ${tuple[*]}" >> "$REGISTRY"
        emit_ports "${tuple[@]}"
        return 0
      else
        inspection_state=$?
      fi
      if [ "$inspection_state" -eq 2 ]; then
        exit 1
      fi
    fi
    slot=$((slot + 1))
  done

  echo "Error: no free port slots (all $MAX_SLOTS in use)" >&2
  exit 1
}

cmd_free() {
  local id="$1"
  acquire_lock

  if ! grep -q "^${id} " "$REGISTRY"; then
    echo "No allocation for: $id"
    return 0
  fi
  # Use grep -v instead of sed to avoid delimiter issues with / in ids
  grep -v "^${id} " "$REGISTRY" > "${REGISTRY}.tmp" || true
  mv "${REGISTRY}.tmp" "$REGISTRY"
  echo "Freed ports for: $id"
}

cmd_list() {
  load_names
  if [ ! -s "$REGISTRY" ]; then
    echo "No port allocations."
    return 0
  fi
  local name
  printf "%-40s" "WORKTREE"
  for name in "${PORT_NAMES[@]}"; do printf " %-12s" "${name}_PORT"; done
  printf "\n"
  while IFS= read -r line; do
    local cols
    cols=$(echo "$line" | awk '{print NF}')
    if [ "$cols" -ne "$NCOLS" ]; then
      printf "%-40s %s\n" "$(echo "$line" | awk '{print $1}')" "MISMATCH: $((cols - 1)) ports recorded, ${NAME_COUNT} names declared"
      continue
    fi
    printf "%-40s" "$(echo "$line" | awk '{print $1}')"
    for port in $(echo "$line" | cut -d' ' -f2-); do printf " %-12s" "$port"; done
    printf "\n"
  done < "$REGISTRY"
}

case "${1:-help}" in
  alloc) cmd_alloc "${2:?worktree-id required}" ;;
  free)  cmd_free  "${2:?worktree-id required}" ;;
  list)  cmd_list ;;
  *)
    echo "Usage: $0 {alloc|free|list} [worktree-id]" >&2
    exit 1
    ;;
esac
