#!/usr/bin/env bash
# worktree.sh — Create and manage git worktrees for parallel pipeline work
#
# Interface, fixed: pfm's git-guard keys on this path (.claude/scripts/worktree.sh) and on the verb names
# below, and lets the main chat run create, land and remove only while git tracks this file unchanged from
# HEAD. Every refusal exits non-zero with one stderr line. Renaming or dropping a verb, or exiting 0 on a
# refusal, breaks the main chat's access. This block stays in sync with
# .claude/rules/git-writes-through-gitter.md; customize everything behind the verbs.
#
#   ./.claude/scripts/worktree.sh create <pipeline>
#   ./.claude/scripts/worktree.sh land <pipeline> [-m <msg>]   # fast-forward BASE_BRANCH, then remove
#   ./.claude/scripts/worktree.sh remove <pipeline> [--delete-branch] [--discard]
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
# The branch every pipeline starts from and lands on.
BASE_BRANCH="main"

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
  if ! make -C "$(proj_path "$ROOT" "$INFRA_MAKE_DIR")" "$INFRA_PIPELINE_NUKE" PIPELINE="$1"; then
    echo "Error: pipeline stack teardown failed for $1" >&2
    return 1
  fi
}

# The marker of a worktree whose provisioning has not finished lives in its private
# Git directory: invisible to `git status`, and removed with the worktree.
provision_marker() {
  local gitdir
  if ! gitdir="$(git -C "$1" rev-parse --absolute-git-dir)"; then
    echo "Error: could not locate the Git directory of $1" >&2
    return 1
  fi
  echo "${gitdir}/provisioning-incomplete"
}

# Set PROVISIONED_ENV_FILES to every env file create provisions, relative to the worktree root:
# .env.ports, then each roster entry's env_files.
provisioned_env_paths() {
  local entry dir kind install env_files ef
  PROVISIONED_ENV_FILES=(.env.ports)
  for entry in "${PROJECTS[@]+"${PROJECTS[@]}"}"; do
    IFS='|' read -r dir kind install env_files <<< "$entry"
    [ -n "$env_files" ] && [ "$env_files" != '-' ] || continue
    for ef in $env_files; do
      if [ "$dir" = '.' ]; then PROVISIONED_ENV_FILES+=("$ef"); else PROVISIONED_ENV_FILES+=("$dir/$ef"); fi
    done
  done
}

# Snapshot final provisioned bytes in the private Git directory, not the checkout.
# Git's read-only object hash needs no extra tool and --no-filters hashes the actual bytes.
# On success PROVISIONED_ENV_FILES is the list whose snapshots were recorded or verified.
provisioned_env_hashes() {
  local mode="$1" tree="$2" marker hashes action=inspect path key digest saved
  [ "$mode" != record ] || action=record
  marker="$(provision_marker "$tree")" || return 1
  hashes="${marker%/*}/provisioned-env-hashes"
  provisioned_env_paths
  local error="Error: could not $action provisioned env hashes for $tree"
  if [[ -L "$hashes" || ( -e "$hashes" && ! -d "$hashes" ) ]]; then
    echo "$error: invalid metadata directory $hashes" >&2; return 1
  fi
  if [ "$mode" = record ]; then
    if ! mkdir -p "$hashes" || ! rm -f "$hashes/complete"; then
      echo "$error: cannot prepare $hashes; provisioning remains incomplete" >&2; return 1
    fi
  else
    if [ -e "$marker" ]; then
      echo "$error: provisioning remains incomplete; back up env files and verify worktree/submodule work is expendable, then use remove ${tree##*/} --discard (discards worktree changes and stack volumes); nothing torn down" >&2; return 1
    fi
    if [ ! -d "$hashes" ]; then
      # A manually added, artifact-free worktree needs no snapshot. Legacy artifacts
      # are unknown bytes, never silently blessed by remove or by an already-ready create.
      for path in "${PROVISIONED_ENV_FILES[@]}"; do
        if [[ -e "$tree/$path" || -L "$tree/$path" ]]; then
          echo "$error: missing metadata for $path; back up env files and verify worktree/submodule work is expendable, then use remove ${tree##*/} --discard (discards worktree changes and stack volumes); nothing torn down" >&2; return 1
        fi
      done
      PROVISIONED_ENV_FILES=(); return 0
    fi
    if [[ ! -f "$hashes/complete" || -L "$hashes/complete" ]] || ! saved="$(cat "$hashes/complete")" || [ "$saved" != v1 ]; then
      echo "$error: incomplete or invalid metadata $hashes; nothing torn down" >&2; return 1
    fi
  fi
  for path in "${PROVISIONED_ENV_FILES[@]}"; do
    if [[ "$path" = /* || "$path" = .. || "$path" = ../* || "$path" = */../* || "$path" = */.. ]]; then
      echo "$error: invalid relative env path $path" >&2; return 1
    fi
    if ! key="$(printf '%s' "$path" | git -C "$tree" hash-object --stdin)"; then
      echo "$error: cannot hash path $path" >&2; return 1
    fi
    if [[ -L "$tree/$path" || ( -e "$tree/$path" && ! -f "$tree/$path" ) ]]; then
      if [ "$mode" = record ]; then echo "$error: $path is not a regular file" >&2
      else echo "Error: dirty worktree refused; provisioned file changed type: $path; use --discard: $tree" >&2; fi
      return 1
    fi
    digest=absent
    if [ -f "$tree/$path" ] && ! digest="$(git -C "$tree" hash-object --no-filters -- "$tree/$path")"; then
      echo "$error: cannot hash file $path" >&2; return 1
    fi
    if [ "$mode" = record ]; then
      if [[ -L "$hashes/$key" ]] || ! printf '%s\n' "$digest" > "$hashes/$key"; then
        echo "$error: cannot write snapshot for $path" >&2; return 1
      fi
    else
      if [[ ! -f "$hashes/$key" || -L "$hashes/$key" ]] || ! saved="$(cat "$hashes/$key")" || [[ ! "$saved" =~ ^([0-9a-f]{40}|[0-9a-f]{64}|absent)$ ]]; then
        echo "$error: missing, unreadable or invalid snapshot for $path; nothing torn down" >&2; return 1
      fi
      if [ "$saved" != "$digest" ]; then
        echo "Error: dirty worktree refused; provisioned bytes changed: $path; use --discard: $tree" >&2; return 1
      fi
    fi
  done
  if [ "$mode" = record ] && ! printf 'v1\n' > "$hashes/complete"; then
    echo "$error: cannot finish metadata $hashes; provisioning remains incomplete" >&2; return 1
  fi
  return 0
}

cmd_create() {
  local pipeline="$1"
  local branch="pipeline/${pipeline}"
  local worktree_dir="${ROOT}/.worktrees/${pipeline}"
  local marker

  if [ -d "$worktree_dir" ]; then
    marker="$(provision_marker "$worktree_dir")" || return 1
    if [ ! -e "$marker" ]; then
      echo "Worktree already exists: $worktree_dir"
      # Still print ports for convenience
      "$ALLOC" alloc "$pipeline"
      exit 0
    fi
    # A worktree that exists is ready only once provisioning finished: an earlier
    # failed or interrupted create resumes here and reports readiness only on success.
    echo "Resuming incomplete provisioning: $worktree_dir"
  else
    # Create branch from BASE_BRANCH if it doesn't exist
    if ! git rev-parse --verify "$branch" >/dev/null 2>&1; then
      git branch "$branch" "$BASE_BRANCH"
    fi

    # Create worktree — full repo checkout
    git worktree add "$worktree_dir" "$branch"
    marker="$(provision_marker "$worktree_dir")" || return 1
    if ! : > "$marker"; then
      echo "Error: could not mark provisioning incomplete; partial worktree: $worktree_dir" >&2
      return 1
    fi
  fi

  # A roster entry backed by a git submodule is left EMPTY by `git worktree add`.
  # Its install would then run against an empty directory and silently produce a
  # worktree with no dependencies, so populate submodules before installing. No-op
  # in a repo that has none.
  if ! git -C "$worktree_dir" submodule update --init --recursive; then
    echo "Error: submodule initialization failed; partial worktree: $worktree_dir" >&2
    return 1
  fi

  # Install dependencies for each project in the roster — a REAL install per
  # worktree, per the NO-SYMLINK LAW above.
  local entry dir install_kind install_cmd env_files src_path dst_path ef install_rc
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
          # errexit runs in a subshell outside any `if` or `||` list, where bash ignores it:
          # a multi-command install fails on its first failing command, not only its last.
          set +e
          ( set -e; cd "$dst_path"; eval "$install_cmd" )
          install_rc=$?
          set -e
          if [ "$install_rc" -ne 0 ]; then
            echo "Error: dependency installation failed for $dir; partial worktree: $worktree_dir" >&2
            return 1
          fi
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

  provisioned_env_hashes record "$worktree_dir" || return 1
  if ! rm -f "$marker"; then
    echo "Error: could not clear the provisioning marker $marker; partial worktree: $worktree_dir" >&2
    return 1
  fi
  echo "Worktree ready: $worktree_dir"
  echo "Branch: $branch"
  echo "$ports"
}

cmd_remove() {
  local pipeline="$1"
  shift
  local delete_branch="" discard="" status rc=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --delete-branch) delete_branch=1 ;;
      --discard) discard=1 ;;
      *) echo "Error: unknown removal option: $1" >&2; return 2 ;;
    esac
    shift
  done
  local branch="pipeline/${pipeline}"
  local worktree_dir="${ROOT}/.worktrees/${pipeline}"

  if [ -d "$worktree_dir" ]; then
    if [[ -z "$discard" ]]; then
      # Exclude only recorded bytes still matching, including gitignored env files;
      # later edits refuse removal before Git status or any teardown. Submodule edits still count.
      provisioned_env_hashes check "$worktree_dir" || return 1
      local -a provisioned=()
      local path
      for path in "${PROVISIONED_ENV_FILES[@]+"${PROVISIONED_ENV_FILES[@]}"}"; do
        provisioned+=(":(exclude,literal)$path")
      done
      if ! status="$(git -C "$worktree_dir" status --porcelain --untracked-files=all --ignore-submodules=none \
        -- . "${provisioned[@]+"${provisioned[@]}"}")"; then
        echo "Error: could not inspect worktree; nothing torn down: $worktree_dir" >&2
        return 1
      fi
      if [[ -n "$status" ]]; then
        echo "Error: dirty worktree refused; use --discard to discard its changes: $worktree_dir" >&2
        return 1
      fi
      # A submodule's objects live in this worktree's private Git directory and go with it,
      # so work a clean status cannot show is refused: a commit reachable from HEAD or a local
      # branch that no remote branch or tag holds, or a stash. A failed reading names the path.
      local unpushed
      if ! unpushed="$(git -C "$worktree_dir" submodule foreach --quiet --recursive \
        'if [ -n "$(git rev-list -n 1 HEAD --branches --not --remotes --tags 2>&1)" ] \
          || git rev-parse -q --verify refs/stash >/dev/null; then echo "$displaypath"; fi')"; then
        echo "Error: could not inspect submodule commits; nothing torn down: $worktree_dir" >&2
        return 1
      fi
      if [[ -n "$unpushed" ]]; then
        echo "Error: submodule commits or stashes on no remote branch (${unpushed//$'\n'/, }); push them or use --discard: $worktree_dir" >&2
        return 1
      fi
      # Proven clean above: --force only lets Git drop .env.ports and initialized
      # submodules, both of which a plain `worktree remove` refuses.
      if ! git worktree remove --force "$worktree_dir"; then
        echo "Error: worktree removal failed; nothing torn down: $worktree_dir" >&2
        return 1
      fi
    elif ! git worktree remove --force "$worktree_dir"; then
      echo "Error: explicit-discard removal failed; nothing torn down: $worktree_dir" >&2
      return 1
    fi
    echo "Worktree removed: $worktree_dir"
  else
    echo "No worktree found: $worktree_dir"
  fi

  # Branch survives by default — it is the merged flight's revert path and can still
  # be LIVE (a post-merge gate still running) when cleanup runs; deletion is a
  # separate, explicit act, never bundled into worktree teardown.
  # The worktree is gone by now: a refused deletion (an unmerged or squash-merged branch) is
  # reported non-zero after the ports and the stack are cleaned, never instead of cleaning them.
  if [ -n "$delete_branch" ]; then
    if git branch -d "$branch"; then
      echo "Branch deleted: $branch"
    else
      echo "Error: branch deletion failed: $branch" >&2
      rc=1
    fi
  else
    echo "Branch kept: $branch (pass --delete-branch to delete)"
  fi

  # Free port allocation
  "$ALLOC" free "$pipeline" || rc=1

  # Tear down the pipeline's docker test stack (containers + volumes).
  nuke_pipeline_stack "$pipeline" || rc=1

  if [ "$rc" -ne 0 ]; then
    echo "Error: cleanup of $pipeline finished with failures (see above)" >&2
    return "$rc"
  fi
  echo "Pipeline cleaned up: $pipeline"
}

# A worktree's uncommitted changes, submodule content included; extra arguments are pathspecs to exclude.
land_status() {
  git -C "$1" status --porcelain --untracked-files=all --ignore-submodules=none -- . "${@:2}"
}

# Land a pipeline: commit its leftover work when -m names a message, fast-forward BASE_BRANCH in the
# main checkout to its branch, then remove the worktree and delete the branch. Anything but a
# fast-forward is refused, the branch keeping its commits: gitter syncs it (Phase MERGE).
cmd_land() {
  local pipeline="$1"
  shift
  local msg=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -m)
        if [[ $# -lt 2 ]]; then echo "Error: -m needs a message; nothing landed" >&2; return 2; fi
        msg="$2"; shift ;;
      *) echo "Error: unknown land option: $1; nothing landed" >&2; return 2 ;;
    esac
    shift
  done
  local branch="pipeline/${pipeline}"
  local worktree_dir="${ROOT}/.worktrees/${pipeline}"
  local top head status cur gitdir path rc=0
  if [ ! -d "$worktree_dir" ] || ! top="$(git -C "$worktree_dir" rev-parse --show-toplevel 2>/dev/null)" \
    || [ "$top" != "$(cd "$worktree_dir" && pwd -P)" ]; then
    echo "Error: no worktree at $worktree_dir; nothing landed" >&2; return 1
  fi
  if ! head="$(git -C "$worktree_dir" symbolic-ref -q --short HEAD)" || [ "$head" != "$branch" ]; then
    echo "Error: $worktree_dir is on ${head:-a detached HEAD}, not its own branch $branch; nothing landed" >&2; return 1
  fi
  if git -C "$worktree_dir" rev-parse -q --verify MERGE_HEAD >/dev/null; then
    echo "Error: a merge is in progress in $worktree_dir (MERGE_HEAD); gitter concludes it (Phase MERGE); nothing landed" >&2
    return 1
  fi
  # The env files create provisioned hold this worktree's ports: never committed, never dirt.
  provisioned_env_paths
  local -a provisioned=()
  for path in "${PROVISIONED_ENV_FILES[@]}"; do
    provisioned+=(":(exclude,literal)$path")
  done
  if ! status="$(land_status "$worktree_dir" "${provisioned[@]}")"; then
    echo "Error: could not inspect $worktree_dir; nothing landed" >&2; return 1
  fi
  if [[ -n "$status" ]]; then
    if [[ -z "$msg" ]]; then
      echo "Error: $worktree_dir has uncommitted changes; pass -m <message> to commit them; nothing landed" >&2; return 1
    fi
    if ! git -C "$worktree_dir" add -A -- . "${provisioned[@]}"; then
      echo "Error: could not stage the changes in $worktree_dir; nothing landed" >&2; return 1
    fi
    if ! git -C "$worktree_dir" diff --cached --quiet && ! git -C "$worktree_dir" commit -q -m "$msg"; then
      echo "Error: commit failed in $worktree_dir (its changes stay staged); nothing landed" >&2; return 1
    fi
    # git add stages no edit inside a submodule: what is left is work the commit could not take.
    if ! status="$(land_status "$worktree_dir" "${provisioned[@]}")"; then
      echo "Error: could not inspect $worktree_dir after the commit; nothing landed" >&2; return 1
    fi
    if [[ -n "$status" ]]; then
      echo "Error: $worktree_dir is still dirty after the commit (work inside a submodule?): ${status//$'\n'/, }; nothing landed" >&2
      return 1
    fi
  fi
  if ! cur="$(git -C "$ROOT" symbolic-ref -q --short HEAD)" || [ "$cur" != "$BASE_BRANCH" ]; then
    echo "Error: the main checkout is on ${cur:-a detached HEAD}, not $BASE_BRANCH; gitter syncs it (Phase MERGE); nothing landed" >&2
    return 1
  fi
  git -C "$ROOT" merge-base --is-ancestor "$BASE_BRANCH" "$branch" || rc=$?
  case "$rc" in
    0) ;;
    1) echo "Error: $BASE_BRANCH has commits $branch lacks; gitter syncs it (Phase MERGE); $branch keeps its commits; nothing landed" >&2
       return 1 ;;
    *) echo "Error: could not compare $BASE_BRANCH with $branch; nothing landed" >&2; return 1 ;;
  esac
  if ! git -C "$ROOT" merge --ff-only -q "$branch"; then
    echo "Error: fast-forward of $BASE_BRANCH to $branch refused (see git above); nothing landed" >&2; return 1
  fi
  echo "Landed: $branch on $BASE_BRANCH"
  if ! gitdir="$(git -C "$worktree_dir" rev-parse --absolute-git-dir)"; then
    echo "Error: $branch landed on $BASE_BRANCH, but its worktree's Git directory is unreadable; worktree kept: $worktree_dir" >&2
    return 1
  fi
  if [ -e "$gitdir/locked" ]; then
    echo "Worktree kept (locked): $worktree_dir — $(cat "$gitdir/locked")"
    echo "Branch kept: $branch"
    return 0
  fi
  if ! cmd_remove "$pipeline" --delete-branch; then
    echo "Error: $branch landed on $BASE_BRANCH, but closing its worktree failed (see above)" >&2; return 1
  fi
}

cmd_prune() {
  # Remove ORPHANED worktree directories: present on disk but not a registered
  # git worktree AND with no active pipeline docs. Registered-but-inactive
  # worktrees are reported, never auto-removed (may hold uncommitted work).
  if ! git -C "$ROOT" worktree prune; then
    echo "Error: worktree record pruning failed; no directories removed" >&2
    return 1
  fi

  # NUL-delimited porcelain preserves complete paths, including spaces/newlines.
  # A failed or empty enumeration is unknown registration, never an orphan list.
  local registered_file record path canonical
  local -a registered=()
  registered_file="$(mktemp)" || return 1
  if ! git -C "$ROOT" worktree list --porcelain -z > "$registered_file"; then
    rm -f "$registered_file"
    echo "Error: could not enumerate worktree registration; no directories removed" >&2
    return 1
  fi
  while IFS= read -r -d '' record; do
    case "$record" in
      'worktree '*)
        path="${record#worktree }"
        if [[ "$path" != /* ]] || ! canonical="$(cd "$path" && pwd -P)"; then
          rm -f "$registered_file"
          echo "Error: unresolved worktree registration: $path; no directories removed" >&2
          return 1
        fi
        registered+=("$canonical") ;;
    esac
  done < "$registered_file"
  rm -f "$registered_file"
  if [[ ${#registered[@]} -eq 0 ]]; then
    echo "Error: empty worktree registration enumeration; no directories removed" >&2
    return 1
  fi

  local pruned=0 failed=0
  local -a nuke_failed=()
  for d in "${ROOT}/.worktrees"/*/; do
    [ -d "$d" ] || continue
    local name
    name="$(basename "$d")"
    [[ "$name" == .* ]] && continue

    if [[ -L "${d%/}" ]]; then
      echo "SKIP: $name (symlink; registration unknown)"
      continue
    fi
    canonical="$(cd "$d" && pwd -P)" || return 1
    local is_registered=0
    for path in "${registered[@]}"; do
      [[ "$canonical" != "$path" ]] || is_registered=1
    done
    if (( is_registered )); then
      # Registered worktree with no active pipeline → possible abandon. Report only.
      [ -d "${ROOT}/docs/dev/builds/${name}" ] || echo "REGISTERED-NO-DOCS: $name (registered worktree, no active pipeline — inspect; not auto-removed)"
      continue
    fi

    # A foreign/stale Git checkout is not proven safe to recursively delete.
    if [[ -e "$d/.git" || -L "$d/.git" ]]; then
      echo "SKIP: $name (Git metadata present; registration unknown)"
      continue
    fi
    # Unregistered leftover directory
    if [ -d "${ROOT}/docs/dev/builds/${name}" ]; then
      echo "SKIP: $name (unregistered but has pipeline docs — inspect manually)"
      continue
    fi

    # Release the allocation BEFORE deleting: a failed release keeps the directory,
    # so the next prune retries it instead of stranding the allocation.
    if ! "$ALLOC" free "$name"; then
      echo "Error: port allocation release failed for $name; directory kept: $d" >&2
      failed=$((failed + 1))
      continue
    fi
    rm -rf "$d"
    # The directory is gone, so a failed teardown is named at the end, never allowed to end the loop.
    nuke_pipeline_stack "$name" || nuke_failed+=("$name")
    echo "PRUNED: $name (orphaned worktree dir)"
    pruned=$((pruned + 1))
  done
  echo "Prune complete: $pruned orphaned worktree(s) removed."
  local rc=0
  if (( failed )); then
    echo "Error: $failed orphaned worktree(s) kept: port allocation release failed" >&2
    rc=1
  fi
  if (( ${#nuke_failed[@]} )); then
    local names
    printf -v names '%s, ' "${nuke_failed[@]}"
    echo "Error: stack teardown failed for ${#nuke_failed[@]} pruned worktree(s): ${names%, }" >&2
    rc=1
  fi
  return "$rc"
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
  land)    shift; cmd_land "${1:?pipeline required}" "${@:2}" ;;
  remove)  shift; cmd_remove "${1:?pipeline required}" "${@:2}" ;;
  list)    cmd_list   "${2:-}" ;;
  prune)   cmd_prune ;;
  *)
    echo "Usage: $0 {create|land|remove|list|prune} <pipeline>" >&2
    exit 1
    ;;
esac
