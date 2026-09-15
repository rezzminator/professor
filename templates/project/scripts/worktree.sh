#!/usr/bin/env bash
# worktree.sh — Create and manage git worktrees for parallel pipeline work
#
# Usage:
#   ./.claude/scripts/worktree.sh create <pipeline>
#   ./.claude/scripts/worktree.sh remove <pipeline> [--delete-branch]
#   ./.claude/scripts/worktree.sh list [pipeline]
#   ./.claude/scripts/worktree.sh prune          # remove orphaned worktree dirs
#
# <pipeline> is the kebab-case pipeline name (e.g., "session-notes")
#
# Each pipeline gets a SINGLE worktree of the entire repo at .worktrees/<pipeline>/.
# One branch, one checkout, every project in the roster included. At roster size 1
# the worktree IS the repo root (no per-project subdir).

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
ALLOC="${ROOT}/.claude/scripts/alloc-ports.sh"

# The roster entry whose Makefile owns the docker test stack, and its per-pipeline
# teardown target. SETUP pins the directory; "-" when no roster entry owns infra.
INFRA_MAKE_DIR="{project}"
INFRA_PIPELINE_NUKE="nuke-test-pipeline"

# ─── Project roster ───────────────────────────────────────────────
# SETUP fills this from the install interview — one entry per project, in order.
# Each entry is a pipe-delimited record:
#
#   dir | install_kind | install_cmd | env_files
#
#   dir          — project directory relative to repo root. "." for a single-project
#                  repo (the worktree root IS the project; no subdir).
#   install_kind — how deps are provisioned in the worktree:
#                    install  — run install_cmd inside the worktree (a REAL,
#                               per-worktree dependency tree of its own)
#                    none     — nothing to install (e.g. an infra project)
#   install_cmd  — the command run in the project dir to install deps
#                  (e.g. "pnpm install --frozen-lockfile", "uv sync", "npm ci")
#   env_files    — space-separated env filenames to copy from main into the worktree
#                  with this project's port substituted (".env.local .env.test"),
#                  or "-" for none.
#
# NO-SYMLINK LAW: a worktree NEVER links its dependency directory to the main
# checkout's. A linked module dir is a live WRITE path into main — an install run
# through it empties main's tree — and any build/transform cache living under it is
# then SHARED across every pipeline worktree, silently inlining another pipeline's
# env values into compiled output (suites that fail to even RUN at GATE, which reads
# as a broken worktree rather than a test failure). Every project installs its own.
#
# Single-project collapse: a roster of one entry with dir "." targets the repo root.
PROJECTS=(
  # {PROJECT_ROSTER} — SETUP expands one line per roster entry, e.g.:
  # "{project}|{INSTALL_KIND}|{PROJECT_INSTALL_CMD}|{PROJECT_ENV_FILES}"
)

# Resolve a project's path inside a target tree. At roster size 1 the entry's dir
# is ".", so the path collapses to the tree root with no trailing subdir.
proj_path() {
  local base="$1" dir="$2"
  if [ "$dir" = "." ]; then
    echo "$base"
  else
    echo "${base}/${dir}"
  fi
}

# Tear down one pipeline's docker test stack via the infra-owning roster entry's
# Makefile. No-op when no roster entry owns infra (INFRA_MAKE_DIR = "-").
nuke_pipeline_stack() {
  [ "$INFRA_MAKE_DIR" != "-" ] || return 0
  make -C "$(proj_path "$ROOT" "$INFRA_MAKE_DIR")" "$INFRA_PIPELINE_NUKE" PIPELINE="$1" 2>/dev/null || true
}

cmd_create() {
  local pipeline="$1"
  local branch="pipeline/${pipeline}"
  local worktree_dir="${ROOT}/.worktrees/${pipeline}"

  if [ -d "$worktree_dir" ]; then
    echo "Worktree already exists: $worktree_dir"
    # Still print ports for convenience
    "$ALLOC" alloc "$pipeline"
    exit 0
  fi

  # Create branch from main if it doesn't exist
  if ! git rev-parse --verify "$branch" >/dev/null 2>&1; then
    git branch "$branch" main
  fi

  # Create worktree — full repo checkout
  git worktree add "$worktree_dir" "$branch"

  # A roster entry backed by a git submodule is left EMPTY by `git worktree add`.
  # Its install would then run against an empty directory and silently produce a
  # worktree with no dependencies, so populate submodules before installing. No-op
  # in a repo that has none.
  git -C "$worktree_dir" submodule update --init --recursive 2>/dev/null || true

  # Install dependencies for each project in the roster — a REAL install per
  # worktree, per the NO-SYMLINK LAW above.
  local entry dir install_kind install_cmd env_files src_path dst_path ef
  for entry in "${PROJECTS[@]}"; do
    IFS='|' read -r dir install_kind install_cmd env_files <<< "$entry"
    src_path="$(proj_path "$ROOT" "$dir")"
    dst_path="$(proj_path "$worktree_dir" "$dir")"
    case "$install_kind" in
      install)
        # Propagate this project's gitignored env files BEFORE the install —
        # `git worktree add` checks out tracked files only, so an env file the
        # project's own config/test bootstrap fail-fasts without is missing until
        # copied, and whole suites then cannot even RUN. No port substitution here
        # (that is the later {ENV_FILE_PROVISION} pass, which rewrites the ports).
        if [ "$env_files" != "-" ]; then
          for ef in $env_files; do
            [ -f "${src_path}/${ef}" ] && cp "${src_path}/${ef}" "${dst_path}/${ef}"
          done
        fi
        if [ -n "$install_cmd" ]; then
          (cd "$dst_path" && eval "$install_cmd" 2>/dev/null) || true
        fi
        ;;
      none) : ;;  # nothing to install
    esac
  done

  # Allocate unique ports (one allocation per pipeline). alloc-ports.sh emits one
  # `{NAME}_PORT=N` line per roster project and per adopter-declared extra port (its
  # PROJECTS / EXTRA_PORTS arrays) — the set is written verbatim to .env.ports
  # (agents source it) and sourced here so the env-file blocks below can reference
  # each port by name (e.g. ${A_PORT}).
  local ports
  if ! ports=$("$ALLOC" alloc "$pipeline"); then
    echo "Error: port allocation failed for pipeline ${pipeline} — see alloc-ports.sh output above" >&2
    exit 1
  fi
  {
    echo "# Auto-generated by worktree.sh — unique ports for pipeline: ${pipeline}"
    echo "# One {NAME}_PORT per roster project, then per extra port (alloc-ports.sh PROJECTS / EXTRA_PORTS)"
    echo "$ports"
  } > "${worktree_dir}/.env.ports"
  # shellcheck disable=SC1091
  source "${worktree_dir}/.env.ports"

  # Copy per-project env files, substituting allocated ports.
  # SETUP expands one block per roster entry that declares env_files — the port
  # substitution rules are project-specific, so they materialize per entry rather
  # than from the array. A single-project repo gets one block targeting the root.
  #
  # .env.test coherence (CRITICAL): when a project's test harness reads its
  # connection target verbatim from .env.test, the rewrite must retarget EVERY
  # test-port reference to the pipeline-isolated stack — not just the DB URL but
  # also the DB port and EVERY service endpoint the adopter declared in EXTRA_PORTS.
  # Leaving any one on the shared default stack silently pins that project (and any
  # sibling whose e2e tier reads the same file) onto the default stack, causing
  # cross-pipeline collisions. The rewrite rules:
  #   • ^-anchored — so an endpoint rewrite never touches a cross-service URL
  #     (e.g. a {project}→{project} callback URL, or a non-test HOST/comment line);
  #   • password-agnostic — match the DB URL without assuming a `user:pass@` form,
  #     since a credential-less `user@host` URL must rewrite too;
  #   • slash-agnostic — match the endpoint with or without a trailing slash,
  #     since committed values may carry none.
  # SETUP materializes one such block per roster entry with env_files, referencing
  # the ports by their emitted names — e.g. roster project `a` whose .env.test
  # carries its own PORT plus a test database declared as EXTRA_PORTS entry TEST_DB:
  #
  #   if [ -f "$(proj_path "$ROOT" "a")/.env.test" ]; then
  #     sed \
  #       -e "s/^PORT=.*/PORT=${A_PORT}/" \
  #       -e "s|@localhost:[0-9]*/{TEST_DB_NAME}|@localhost:${TEST_DB_PORT}/{TEST_DB_NAME}|" \
  #       -e "s/^DB_PORT=.*/DB_PORT=${TEST_DB_PORT}/" \
  #       "$(proj_path "$ROOT" "a")/.env.test" \
  #       > "$(proj_path "$worktree_dir" "a")/.env.test"
  #   fi
  #
  # {ENV_FILE_PROVISION} — SETUP fills this from the roster's env_files + port map.

  echo "Worktree ready: $worktree_dir"
  echo "Branch: $branch"
  echo "$ports"
}

cmd_remove() {
  local pipeline="$1"
  local delete_branch=""
  [ "${2:-}" = "--delete-branch" ] && delete_branch=1
  local branch="pipeline/${pipeline}"
  local worktree_dir="${ROOT}/.worktrees/${pipeline}"

  if [ -d "$worktree_dir" ]; then
    git worktree remove "$worktree_dir" --force 2>/dev/null || true
    echo "Worktree removed: $worktree_dir"
  else
    echo "No worktree found: $worktree_dir"
  fi

  # Branch survives by default — it is the merged wave's revert path and can still
  # be LIVE (boundary GATE-2) when cleanup runs; deletion is a separate, explicit
  # act, never bundled into worktree teardown.
  if [ -n "$delete_branch" ]; then
    git branch -d "$branch" 2>/dev/null || true
    echo "Branch deleted: $branch"
  else
    echo "Branch kept: $branch (pass --delete-branch to delete)"
  fi

  # Free port allocation
  "$ALLOC" free "$pipeline"

  # Tear down the pipeline's docker test stack (containers + volumes).
  nuke_pipeline_stack "$pipeline"

  echo "Pipeline cleaned up: $pipeline"
}

cmd_prune() {
  # Remove ORPHANED worktree directories: present on disk but not a registered
  # git worktree AND with no active pipeline docs. Registered-but-inactive
  # worktrees are reported, never auto-removed (may hold uncommitted work).
  git -C "$ROOT" worktree prune 2>/dev/null || true  # drop git records for vanished dirs

  # Registered worktree BASENAMES — matched by name, not full path, so a symlinked or
  # canonicalized repo path can't misclassify a live worktree as orphaned.
  # Fail-safe: if git can't list worktrees, prune nothing rather than risk a live one.
  local registered
  if ! registered="$(git -C "$ROOT" worktree list --porcelain 2>/dev/null | awk '/^worktree /{n=split($2,a,"/"); print a[n]}')"; then
    echo "Prune skipped: could not list git worktrees."
    return 0
  fi

  local pruned=0
  for d in "${ROOT}/.worktrees"/*/; do
    [ -d "$d" ] || continue
    local name
    name="$(basename "$d")"
    [[ "$name" == .* ]] && continue

    if printf '%s\n' "$registered" | grep -qx "$name"; then
      # Registered worktree with no active pipeline → possible abandon. Report only.
      [ -d "${ROOT}/docs/dev/builds/${name}" ] || echo "REGISTERED-NO-DOCS: $name (registered worktree, no active pipeline — inspect; not auto-removed)"
      continue
    fi

    # Unregistered leftover directory
    if [ -d "${ROOT}/docs/dev/builds/${name}" ]; then
      echo "SKIP: $name (unregistered but has pipeline docs — inspect manually)"
      continue
    fi

    rm -rf "$d"
    "$ALLOC" free "$name" 2>/dev/null || true
    nuke_pipeline_stack "$name"
    echo "PRUNED: $name (orphaned worktree dir)"
    pruned=$((pruned + 1))
  done
  echo "Prune complete: $pruned orphaned worktree(s) removed."
}

cmd_list() {
  local pipeline="${1:-}"

  if [ -n "$pipeline" ]; then
    local worktree_dir="${ROOT}/.worktrees/${pipeline}"
    if [ ! -d "$worktree_dir" ]; then
      echo "No worktree for pipeline: $pipeline"
      return 0
    fi
    echo "=== Pipeline: $pipeline ==="
    echo "  Branch: pipeline/$pipeline"
    echo "  Path: $worktree_dir"
    if [ -f "$worktree_dir/.env.ports" ]; then
      echo "  Ports:"
      grep -E '^[A-Z][A-Z0-9_]*_PORT=' "$worktree_dir/.env.ports" | sed 's/^/    /'
    fi
  else
    echo "=== Active Pipelines ==="
    for d in "${ROOT}/.worktrees"/*/; do
      local name
      name="$(basename "$d")"
      # Skip hidden dirs (.ports, .merge-lock, etc.)
      [[ "$name" == .* ]] && continue
      [ -d "$d" ] || continue
      echo "  $name/ → pipeline/$name"
    done
  fi

  echo ""
  echo "=== Port Allocations ==="
  "$ALLOC" list
}

case "${1:-help}" in
  create)  cmd_create "${2:?pipeline required}" ;;
  remove)  cmd_remove "${2:?pipeline required}" "${3:-}" ;;
  list)    cmd_list   "${2:-}" ;;
  prune)   cmd_prune ;;
  *)
    echo "Usage: $0 {create|remove|list|prune} <pipeline>" >&2
    exit 1
    ;;
esac
