#!/usr/bin/env bash
set -euo pipefail

# dev.sh — the single entry point for building, testing, and inspecting this
# repo's three projects. /dev drives it; agents call it directly.
#
# WHAT THIS SCRIPT REPORTS WHEN IT IS ITSELF BROKEN:
#   - a missing toolchain (go/node) is TOOLCHAIN-MISSING and exits non-zero.
#     It is NEVER reported as a pass or a skip: "we could not look" and "there is
#     nothing wrong" must not print the same word.
#   - a project with no dependencies installed is NOT-INSTALLED, not "clean".
#   - an unknown project or command exits 2 with usage — never a silent no-op
#     that a caller could read as success.
#   - every command's own exit status propagates; nothing is swallowed with `|| true`.

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# Scratch artifacts live outside the working tree: /tmp/<project>/<purpose>,
# where <project> is the repo directory's basename with any leading dot stripped.
# A run never dirties the checkout, and every artifact path printed is absolute.
PROJECT_NAME="$(basename "$REPO_ROOT")"; PROJECT_NAME="${PROJECT_NAME#.}"
TMP_BASE="/tmp/$PROJECT_NAME"
cd "$REPO_ROOT"

PROJECTS=(templates pfm)

# project -> directory
proj_dir() {
  case "$1" in
    templates) echo "templates" ;;
    pfm)  echo "pfm" ;;
    *) return 1 ;;
  esac
}

# project -> the toolchain binaries that project's checks actually need. A tool
# absent from the SCOPE being reported is a warn, not a failure: `status pfm`
# must not fail on a missing node, and must still fail on a missing go.
proj_tools() {
  case "$1" in
    templates) echo "node go" ;;
    pfm)       echo "go" ;;
    *) return 1 ;;
  esac
}

RED=$'\033[31m'; GREEN=$'\033[32m'; YELLOW=$'\033[33m'; DIM=$'\033[2m'; OFF=$'\033[0m'
[[ -t 1 ]] || { RED=""; GREEN=""; YELLOW=""; DIM=""; OFF=""; }

ok()   { printf '%s  PASS%s  %s\n' "$GREEN" "$OFF" "$*"; }
bad()  { printf '%s  FAIL%s  %s\n' "$RED" "$OFF" "$*"; }
warn() { printf '%s  WARN%s  %s\n' "$YELLOW" "$OFF" "$*"; }
gap()  { printf '%s  GAP %s  %s\n' "$YELLOW" "$OFF" "$*"; }
info() { printf '%s        %s%s\n' "$DIM" "$*" "$OFF"; }
head_() { printf '\n%s──%s %s\n' "$DIM" "$OFF" "$*"; }

FAILURES=0
fail_step() { bad "$*"; FAILURES=$((FAILURES + 1)); }
# gap_step: a project the aggregate sweep never reached is a coverage gap, not
# an ordinary failure — "GAP" names that distinction so it cannot be misread
# as "a test broke" among a scroll of FAILs. Still non-zero: a skipped or
# filtered suite is a named gap in the report, never a pass.
gap_step() { gap "$*"; FAILURES=$((FAILURES + 1)); }

need_tool() { # need_tool <bin> <project>
  if ! command -v "$1" >/dev/null 2>&1; then
    fail_step "$2: TOOLCHAIN-MISSING — '$1' not on PATH; this project could not be checked"
    return 1
  fi
}

run() { # run <label> -- <cmd...>
  local label="$1"; shift
  [[ "${1:-}" == "--" ]] && shift
  info "\$ $*"
  if "$@"; then ok "$label"; else fail_step "$label (exit $?)"; fi
}

# repo_git: the one fence-aware git reader, shared with the arch ratchets.
# shellcheck source=../../pfm/scripts/repo-git.sh
source "$REPO_ROOT/pfm/scripts/repo-git.sh" || { echo "dev.sh: cannot source pfm/scripts/repo-git.sh" >&2; exit 2; }
# The step runner and the check catalogue: the verify, test and gate rows live
# in infra/fence/checks.sh; gate runs them as concurrent steps (steps.sh).
# shellcheck source=../../infra/fence/steps.sh
source "$REPO_ROOT/infra/fence/steps.sh" || { echo "dev.sh: cannot source infra/fence/steps.sh" >&2; exit 2; }
# shellcheck source=../../infra/fence/checks.sh
source "$REPO_ROOT/infra/fence/checks.sh" || { echo "dev.sh: cannot source infra/fence/checks.sh" >&2; exit 2; }
# The last line of every run that made a timing run dir is `RUN DIR: <absolute host path>`
# (timing_run_report in infra/fence/checks.sh) — printed on exit, after the footer and, under
# iso, after the host's own lines (the gate-history ingest).
trap timing_run_report EXIT

# skip_gate <label> <go-test.json>: every skipped test must be named in
# pfm/scripts/known-skips.tsv (scripts/skip-check.sh). An unlisted skip is a
# GAP and non-zero; a skip list that could not be read is a FAIL, never a pass.
skip_gate() {
  local label="$1" rc=0
  bash "$REPO_ROOT/pfm/scripts/skip-check.sh" "$2" || rc=$?
  case "$rc" in
    0) ok "$label" ;;
    1) gap_step "$label — unlisted skipped test(s) named above" ;;
    *) fail_step "$label — the skips could not be read (exit $rc)" ;;
  esac
}

# timing_run_dir <base>: create <base> if absent and a fresh run.XXXXXX under
# it, mode 0755 whatever the umask — the fence writes as root under the bind
# mount and the host's non-root reader (`make -C pfm timing`, the CI artifact
# upload) must still open it. Prints the run dir's absolute path. BROKEN STATE:
# a base or run dir that cannot be made is non-zero with a stderr line naming
# the base and prints no path, so a caller never runs a suite into nowhere.
timing_run_dir() {
  local base="$1" run abs
  if [[ ! -d "$base" ]]; then
    if ! mkdir -p "$base" 2>/dev/null || ! chmod 0755 "$base" 2>/dev/null; then
      echo "timing_run_dir: the timing base $base could not be created" >&2; return 1
    fi
  fi
  if ! run="$(mktemp -d "$base/run.XXXXXX" 2>/dev/null)"; then
    echo "timing_run_dir: no run directory could be created under $base" >&2; return 1
  fi
  if ! chmod 0755 "$run" || ! abs="$(cd "$run" && pwd)"; then
    echo "timing_run_dir: the run directory under $base could not be opened to readers ($run)" >&2; return 1
  fi
  printf '%s\n' "$abs"
}

# pfm_e2e_rows <pfm-dir> <timing-run-dir>: the one definition of the tagged e2e
# rows — `test` runs them after the unit rows, `e2e` runs them alone. Tagged
# Tier A runs serially and has its own budget and artifact.
pfm_e2e_rows() {
  local d="$1" timing_run="$2"
  run "pfm: e2e (tagged)" -- bash -c '
    go -C "$1" test -tags e2e -p 1 -count=1 -timeout 25m -json ./e2e/... >"$2"
  ' _ "$d" "$timing_run/e2e.json"
  go_test_report "$timing_run/e2e.json"
  skip_gate "pfm: skipped tests are all listed (e2e)" "$timing_run/e2e.json"
  run "pfm: e2e timing (budget)" -- bash "$d/scripts/test-timing.sh" \
    --check --suite e2e --out "$timing_run/e2e.tsv" "$timing_run/e2e.json"
}

# go_test_report <go-test.json>: the failure-biased read of a `go test -json`
# stream — the whole stream is megabytes of frames a caller cannot hold, so this
# prints one block per FAILING test (package, test name, that test's own output
# capped at GO_TEST_OUTPUT_LINES lines and GO_TEST_LINE_CHARS characters each —
# a single assertion that embeds a whole captured stdout is one 6 KB line, so a
# line count alone caps nothing), then packages that failed without a failing
# test, then the artifact's ABSOLUTE path on every path, pass or fail, so the
# caller never reconstructs it. What it reports when IT is broken: no jq is
# TOOLCHAIN-MISSING, an absent or empty stream is REPORT-UNREADABLE, and a
# stream holding no test event at all is NO TEST EVENTS (crash, kill, or build
# failure) — never an empty summary that reads the same as a green run.
# trim_line <chars>: cap each line, naming the cut so a truncated assertion is
# never mistaken for the whole message.
trim_line() {
  awk -v n="$1" '{ if (length($0) > n) print substr($0, 1, n) " …[line truncated, full text in the log]"; else print }'
}

go_test_report() {
  local json="$1" cap="${GO_TEST_OUTPUT_LINES:-25}" chars="${GO_TEST_LINE_CHARS:-400}" abs dir
  dir="$(cd "$(dirname "$json")" 2>/dev/null && pwd)" || dir="$(dirname "$json")"
  abs="$dir/$(basename "$json")"
  if ! command -v jq >/dev/null 2>&1; then
    fail_step "test report: TOOLCHAIN-MISSING — 'jq' not on PATH; the failure summary could not be built"
    info "log: $abs"; return
  fi
  if [[ ! -s "$json" ]]; then
    fail_step "test report: REPORT-UNREADABLE — $abs is absent or empty; the run left no stream to read"
    info "log: $abs"; return
  fi
  if [[ -z "$(jq -r 'select(.Test != null) | .Test' "$json" 2>/dev/null | head -1)" ]]; then
    fail_step "test report: NO TEST EVENTS — the stream holds no test event; the run crashed, was killed, or failed to build"
    jq -r 'select(.Action=="output") | .Output' "$json" 2>/dev/null \
      | grep -vE '^[[:space:]]*$' | tail -n "$cap" | trim_line "$chars" | sed 's/^/        /' || true
    info "log: $abs"; return
  fi
  local fails failed failpkgs pkg test body total n=0
  # One pass over the stream finds both kinds of failure; a green stream is read once more only by the probe above.
  fails="$(jq -r 'select(.Action=="fail") | if .Test != null then "T\t" + .Package + "\t" + .Test else "P\t" + .Package end' "$json")"
  failed="$(awk -F'\t' '$1=="T" { print $2 "\t" $3 }' <<< "$fails" | sort -u)"
  failpkgs="$(awk -F'\t' '$1=="P" { print $2 }' <<< "$fails" | sort -u)"
  if [[ -n "$failed" ]]; then
    while IFS=$'\t' read -r pkg test; do
      [[ -z "$pkg" ]] && continue
      n=$((n + 1))
      printf '  FAIL  %s %s\n' "$pkg" "$test"
      body="$(jq -r --arg p "$pkg" --arg t "$test" \
        'select(.Action=="output" and .Package==$p and .Test==$t) | .Output' "$json" \
        | grep -vE '^(=== (RUN|PAUSE|CONT)|( *)--- (PASS|FAIL|SKIP))|^[[:space:]]*$' || true)"
      if [[ -n "$body" ]]; then
        total=$(printf '%s\n' "$body" | wc -l | tr -d ' ')
        printf '%s\n' "$body" | head -n "$cap" | trim_line "$chars" | sed 's/^/        /' || true
        (( total > cap )) && printf '        (+%d more output line(s) in the log)\n' "$((total - cap))"
      fi
    done <<< "$failed"
  fi
  while read -r pkg; do
    [[ -z "$pkg" ]] && continue
    awk -F'\t' -v p="$pkg" '$1==p {found=1} END {exit !found}' <<< "$failed" && continue
    n=$((n + 1))
    printf '  FAIL  %s — the package failed with no failing test (build or setup error)\n' "$pkg"
    jq -r --arg p "$pkg" 'select(.Action=="output" and .Package==$p and .Test==null) | .Output' "$json" \
      | grep -vE '^(ok|FAIL|PASS)|^[[:space:]]*$' | head -n "$cap" | trim_line "$chars" | sed 's/^/        /' || true
  done <<< "$failpkgs"
  (( n > 0 )) && info "$n failing test(s)/package(s) summarised above, capped at $cap output line(s) each"
  info "log: $abs"
}

# ─── status ──────────────────────────────────────────────────────────────────

cmd_status() { # cmd_status [project|all]
  local target="${1:-all}"
  local scope=()
  if [[ "$target" == "all" ]]; then
    scope=("${PROJECTS[@]}")
  else
    proj_dir "$target" >/dev/null 2>&1 || { echo "unknown project: $target" >&2; usage; }
    scope=("$target")
  fi

  # git is repo-level (branch, tag, dirty count below), so it is required in
  # every scope; each targeted project adds the binaries its own checks run.
  local required=" git " p
  for p in "${scope[@]}"; do required+="$(proj_tools "$p") "; done

  head_ "toolchain — scope: $target"
  for t in go node git jq; do
    if command -v "$t" >/dev/null 2>&1; then
      ok "$t — $(command -v "$t")"
    elif [[ "$required" == *" $t "* ]]; then
      fail_step "$t — MISSING (TOOLCHAIN-MISSING; '$target' cannot be checked without it)"
    else
      warn "$t — MISSING (not required by the '$target' scope)"
    fi
  done

  head_ "projects"
  for p in "${scope[@]}"; do
    local d; d="$(proj_dir "$p")"
    if [[ ! -d "$d" ]]; then fail_step "$p — directory $d/ is MISSING"; continue; fi
    case "$p" in
      templates)
        ok "$p — $d/ ($(find "$d" -type f -not -name refresh-map.json | wc -l | tr -d ' ') shipped files, no build)" ;;
      pfm)
        ok "$p — $d/ (go $(sed -n 's/^go //p' "$d/go.mod" | head -1))" ;;
    esac
  done

  head_ "git"
  local dirty; dirty=$(repo_git status --porcelain | wc -l | tr -d ' ')
  info "branch $(repo_git rev-parse --abbrev-ref HEAD) @ $(repo_git rev-parse --short HEAD) — $dirty changed file(s)"
  info "version $(cat VERSION 2>/dev/null || echo '?') — newest tag $(repo_git describe --tags --abbrev=0 2>/dev/null || echo 'none')"

  head_ "install"
  for f in .professor/VERSION .professor/manifest.json CLAUDE.md AGENTS.md .claude/settings.json; do
    [[ -e "$f" ]] && ok "$f" || warn "$f — absent"
  done
}

# ─── per-project actions ─────────────────────────────────────────────────────

# node_test_suite LABEL TAP FILE... — runs node's test runner into TAP and
# names every way it can fail to prove anything: a missing file, a red or
# unrunnable suite, zero passing tests, a skipped or todo test.
node_test_suite() {
  local label="$1" tap="$2"
  shift 2
  local file
  for file in "$@"; do
    if [[ ! -f "$file" ]]; then
      fail_step "$label tests NOT RUN — $file is missing; the suite was never executed"
      return
    fi
  done
  # Concurrent gate steps share the tap directory; no step may rely on another creating it.
  if ! mkdir -p "$(dirname "$tap")"; then
    fail_step "$label tests NOT RUN — the TAP directory for $tap could not be created"
    return
  fi
  if ! node --test --test-reporter=tap "$@" >"$tap" 2>&1; then
    cat "$tap"
    fail_step "$label tests FAILED — a test regressed, or node could not run the suite (see output)"
  elif ! awk '/^# pass /{ if ($3 > 0) found=1 } END{ exit !found }' "$tap"; then
    cat "$tap"
    fail_step "$label tests NOT RUN — the suite reported zero passing tests; a green exit with no test is not a pass"
  elif ! awk '/^# (skipped|todo) /{ if ($3 > 0) bad=1 } END{ exit bad }' "$tap"; then
    cat "$tap"
    fail_step "$label tests SKIPPED — a skipped or todo test is a named gap, never a pass"
  else
    ok "$label tests hold ($(awk '/^# pass /{print $3}' "$tap") passing)"
  fi
}

act_templates() { # the shipped product: mechanical gates, no build
  local action="$1"
  case "$action" in
    install|build|typecheck|cover|e2e) info "templates: no $action step (markdown + shell)" ;;
    verify|test|all)
      # The rows: checks_templates in infra/fence/checks.sh.
      checks_templates
      ;;
    *) return 0 ;;
  esac
}

act_pfm() {
  local action="$1" d; d="$(proj_dir pfm)"
  need_tool go pfm || return 0
  # fmt-check, lint-new and cover run through pfm/Makefile.
  need_tool make pfm || return 0
  case "$action" in
    install) run "pfm: go mod download" -- go -C "$d" mod download ;;
    build)
      run "pfm: make prompts" -- make -C "$d" prompts
      run "pfm: go build" -- go -C "$d" build ./...
      # Reproducible build: ./cmd/pfm compiled twice for the environment's
      # GOOS/GOARCH with every input pinned (no cgo, no GOFLAGS, trimmed paths,
      # no VCS stamp — the fence's linked-worktree .git names a host path) must
      # hash identically. BROKEN STATE: no sha256 tool is TOOLCHAIN-MISSING and
      # red, never a pass; a failed build or scratch dir is red naming the label.
      local goos goarch label build_dir h1 h2 n
      local sum_cmd=()
      goos="$(go env GOOS)"; goarch="$(go env GOARCH)"
      label="pfm: reproducible build ($goos/$goarch)"
      if command -v sha256sum >/dev/null 2>&1; then sum_cmd=(sha256sum)
      elif command -v shasum >/dev/null 2>&1; then sum_cmd=(shasum -a 256)
      else fail_step "$label — TOOLCHAIN-MISSING: neither sha256sum nor shasum on PATH; the builds could not be compared"; return; fi
      if ! mkdir -p "$TMP_BASE/build" || ! build_dir="$(mktemp -d "$TMP_BASE/build/repro.XXXXXX")"; then
        fail_step "$label — no scratch directory could be created under $TMP_BASE/build"; return
      fi
      info "\$ CGO_ENABLED=0 go -C $d build -trimpath -buildvcs=false -ldflags \"-X main.version=verify\" -o $build_dir/pfm.{1,2} ./cmd/pfm"
      for n in 1 2; do
        env -u GOFLAGS CGO_ENABLED=0 go -C "$d" build -trimpath -buildvcs=false \
          -ldflags "-X main.version=verify" -o "$build_dir/pfm.$n" ./cmd/pfm || break
      done
      if [[ ! -f "$build_dir/pfm.1" || ! -f "$build_dir/pfm.2" ]]; then
        fail_step "$label — go build ./cmd/pfm failed (see output)"
      else
        h1="$("${sum_cmd[@]}" "$build_dir/pfm.1" | awk '{print $1}')"
        h2="$("${sum_cmd[@]}" "$build_dir/pfm.2" | awk '{print $1}')"
        if [[ -n "$h1" && "$h1" == "$h2" ]]; then
          ok "$label — sha256 = $h1"
        else
          fail_step "$label — the two builds differ: sha256 ${h1:-<unreadable>} vs ${h2:-<unreadable>}"
        fi
      fi
      rm -rf "$build_dir" ;;
    typecheck) run "pfm: go vet" -- go -C "$d" vet ./... ;;
    verify) checks_pfm_verify "$d" ;;
    # The rows (unit sharded by pfm/scripts/test-shard.sh, then the e2e rows):
    # checks_pfm_test in infra/fence/checks.sh.
    test) checks_pfm_test "$d" ;;
    # The tagged e2e suite alone, in its own timing run dir — the same rows
    # `test` runs after the unit suite (pfm_e2e_rows).
    e2e)
      local timing_base timing_run
      timing_base="${PFM_TEST_TIMING_DIR:-$TMP_BASE/timing}"
      if ! timing_run="$(timing_run_dir "$timing_base")"; then
        fail_step "pfm: timing run directory could not be created under $timing_base"; return
      fi
      timing_run_note "$timing_run"
      pfm_e2e_rows "$d" "$timing_run" ;;
    # Cross-package unit coverage merged with any e2e GOCOVERDIR run, thresholded
    # by pfm/.testcoverage.yml (a ratchet: measured, raised, never lowered).
    # COVER_DIR is where the profiles land — the fence sets it to container HOME
    # because the worktree mount is read-only.
    cover)   run "pfm: coverage (go-test-coverage)" -- make -C "$d" --no-print-directory cover ;;
    all)     act_pfm build; act_pfm verify; act_pfm test ;;
  esac
}

dispatch() { # dispatch <project> <action>
  case "$1" in
    templates) act_templates "$2" ;;
    pfm)  act_pfm "$2" ;;
  esac
}

# ─── iso — the container fence ───────────────────────────────────────────────
# Runs a command inside the pfm-dev container (infra/fence/docker-compose.yml) with
# THIS checkout — the worktree this script belongs to — mounted at /worktree: a
# fresh machine per run (own HOME, own tmux, no published ports). Files are
# edited on the host; the container only builds and tests.
# First output line is the fence proof (container hostname + HOME + /worktree).
# BROKEN STATE: docker missing, its DAEMON unreachable, or the compose file
# missing = TOOLCHAIN-MISSING and a non-zero exit — never a host fallback; a run
# that cannot print its fence proof did not run inside the fence. The daemon is
# probed explicitly: an installed `docker` binary with nothing behind it is the
# common failure, and it must be named as TOOLCHAIN-MISSING here rather than
# surfacing later as an opaque compose connect error.
# sim_volume — this worktree's harvester volume for `iso sim`, named by
# infra/fence/housekeeping.sh's fence_sim_volume (sourced in cmd_iso), whose
# checkouts step removes it once the worktree is gone.
sim_volume() { fence_sim_volume "$REPO_ROOT"; }

cmd_iso() { # cmd_iso <action> [project | command…]
  local action="${1:-}" target="${2:-pfm}"
  need_tool docker iso || exit 1
  need_tool git iso || exit 1
  if ! docker info >/dev/null 2>&1; then
    fail_step "iso: TOOLCHAIN-MISSING — the docker daemon is not reachable ('docker info' failed); start Docker and retry"
    exit 1
  fi
  local compose="$REPO_ROOT/infra/fence/docker-compose.yml"
  if [[ ! -f "$compose" ]]; then
    fail_step "iso: TOOLCHAIN-MISSING — $compose not found"; exit 1
  fi
  # infra/fence/housekeeping.sh: the image-building gate actions clear stale
  # fence containers, images and caches first (status, run, shell and sim pay
  # nothing); every action finds the cache volumes housekeeping.sh lists
  # (FENCE_CACHE_VOLUMES). Neither call ever fails this script.
  . "$REPO_ROOT/infra/fence/housekeeping.sh"
  case "$action" in
    install|build|typecheck|verify|test|e2e|cover|all|gate) fence_housekeeping ;;
  esac
  fence_volumes_ensure

  # The fence mount contract (PFM_DEV_WORKTREE / PFM_DEV_GIT_COMMON /
  # PFM_DEV_GIT_DIR_REL) is resolved once, in infra/fence/fence-env.sh — the demo
  # fence sources the same file, so the two never drift.
  local git_common
  ROOT="$REPO_ROOT" FENCE_CALLER="iso" . "$REPO_ROOT/infra/fence/fence-env.sh"
  git_common="$PFM_DEV_GIT_COMMON"
  # The leak denylist is untracked and lives only in the main checkout, so a
  # linked worktree's mount never carries it; hand it in read-only (LEAK_TERMS
  # wins). Without one the in-fence leak gate fails loudly — never a fake pass.
  local terms="${LEAK_TERMS:-$(dirname "$git_common")/scripts/leak-terms.txt}"
  local extra=()
  [[ -f "$terms" ]] && extra=(-v "$terms:/pfm-leak-terms.txt:ro" -e LEAK_TERMS=/pfm-leak-terms.txt)
  # The worktree mount is read-only; coverage profiles land in container HOME.
  extra+=(-e COVER_DIR=/root/cover)
  # Only generated timing artifacts are writable; the source mount stays read-only.
  # PFM_TEST_TIMING_HOST maps a fence run dir back to its host path; the fence writes that path
  # to the note PFM_TEST_RUN_NOTE, and this script's EXIT trap prints it as the last line.
  mkdir -p "$TMP_BASE/timing"
  TIMING_RUN_NOTE="$(mktemp "$TMP_BASE/timing/.run-note.XXXXXX")" \
    || { fail_step "iso: the run-dir note could not be created under $TMP_BASE/timing"; exit 1; }
  extra+=(-v "$TMP_BASE/timing:/pfm-timing" -e PFM_TEST_TIMING_DIR=/pfm-timing
    -e "PFM_TEST_TIMING_HOST=$TMP_BASE/timing" -e "PFM_TEST_RUN_NOTE=/pfm-timing/${TIMING_RUN_NOTE##*/}")
  if [[ -n "${TESTFLAGS+x}" ]]; then extra+=(-e "TESTFLAGS=$TESTFLAGS"); fi
  # Profiling and step-scheduling knobs reach the fence when the caller set them.
  local knob
  for knob in STEPS_JOBS STEPS_HEAVY_JOBS STEPS_BOUND_S STEPPROF_TRACE STEPPROF_GRACE_TICKS PFM_TEST_PROFILE PFM_GATE_FIXTURES; do
    if [[ -n "${!knob+x}" ]]; then extra+=(-e "$knob=${!knob}"); fi
  done
  local proof='echo "fence: container=$(hostname) HOME=$HOME work=$(pwd)"'
  # infra/fence/image-key.sh: a service image is built only when the key of its
  # build inputs differs from the pfm.fence.inputs label the image carries, so a
  # current image starts with no build and no registry round trip.
  case "$action" in
    shell|install|build|typecheck|verify|test|e2e|cover|all|status|gate|run|sim)
      local service=pfm-dev
      [[ "$action" != sim ]] || service=pfm-sim
      . "$REPO_ROOT/infra/fence/image-key.sh"
      fence_image_prepare "$compose" "$service" || { fail_step "iso: the $service image could not be keyed — see the line above"; exit 1; } ;;
  esac
  case "$action" in
    shell)
      # Interactive: housekeeping's age limit never ends a shell someone is in.
      docker compose -f "$compose" run --rm ${FENCE_IMAGE_BUILD[@]+"${FENCE_IMAGE_BUILD[@]}"} --label pfm.fence.long-lived=1 \
        ${extra[@]+"${extra[@]}"} pfm-dev zsh -c "$proof; exec zsh -i" ;;
    install|build|typecheck|verify|test|e2e|cover|all|status)
      docker compose -f "$compose" run --rm ${FENCE_IMAGE_BUILD[@]+"${FENCE_IMAGE_BUILD[@]}"} ${extra[@]+"${extra[@]}"} pfm-dev bash -c "$proof; ./.claude/scripts/dev.sh $action $target" ;;
    gate)
      # The flight gate: pfm and templates rows as concurrent steps in ONE
      # container, the per-step table in the run dir under /pfm-timing. The
      # whole gate runs under the egress recorder; its verdict is the last line.
      local gate_rc=0
      docker compose -f "$compose" run --rm ${FENCE_IMAGE_BUILD[@]+"${FENCE_IMAGE_BUILD[@]}"} ${extra[@]+"${extra[@]}"} pfm-dev bash -c "$proof; bash infra/fence/egress.sh run ./.claude/scripts/dev.sh gate ${2:-all}" || gate_rc=$?
      # The permanent ledger lives on the host: append this run (and any real
      # gate run not yet recorded) after the container is gone.
      bash "$REPO_ROOT/infra/fence/gate-history.sh" ingest "$TMP_BASE/timing" --host-load-now \
        || echo "gate-history: LEDGER-NOT-WRITTEN — ingest exited $? (the gate verdict above stands)" >&2
      return "$gate_rc" ;;
    run)
      # An arbitrary command inside the fence, from the worktree root — for the
      # probes the fixed rows do not cover (`go test -json ./cmd/pfm`, a single
      # package, `make -C pfm lint`). Exit status is the command's own.
      local cmd="${*:2}"
      [[ -z "$cmd" ]] && { echo "usage: dev.sh iso run <command…>" >&2; exit 2; }
      docker compose -f "$compose" run --rm ${FENCE_IMAGE_BUILD[@]+"${FENCE_IMAGE_BUILD[@]}"} ${extra[@]+"${extra[@]}"} pfm-dev bash -c "$proof; $cmd" ;;
    sim)
      # The real-simulation fence: `run` on the pfm-sim service — Google Chrome
      # (headless only), and pfm built + installed from this worktree with the
      # harvester's browser rung on (infra/fence/sim-entry.sh prints its own
      # `sim:` proof line or BOOTSTRAP-FAILED). The harvester state persists in
      # a volume keyed by this worktree's path, so a second run skips
      # provisioning and two worktrees never share a sidecar.
      local cmd="${*:2}"
      [[ -z "$cmd" ]] && { echo "usage: dev.sh iso sim <command…>" >&2; exit 2; }
      extra+=(-v "$(sim_volume):/root/.local/state/pfm/harvest-python")
      docker compose -f "$compose" run --rm ${FENCE_IMAGE_BUILD[@]+"${FENCE_IMAGE_BUILD[@]}"} ${extra[@]+"${extra[@]}"} pfm-sim bash -c "$proof; $cmd" ;;
    sim-reset)
      # Drops this worktree's harvester volume (several GB of provisioned
      # sidecars); the next `iso sim` provisions from scratch. An absent volume
      # is reported as absent, a failed removal fails.
      local volume; volume="$(sim_volume)"
      if ! docker volume inspect "$volume" >/dev/null 2>&1; then
        info "iso sim-reset: $volume does not exist — nothing to drop"; return 0
      fi
      docker volume rm "$volume" >/dev/null || { fail_step "iso sim-reset: could not drop $volume (in use by a running sim?)"; exit 1; }
      ok "iso sim-reset: dropped $volume" ;;
    *)
      echo "usage: dev.sh iso {install|build|typecheck|verify|test|cover|all|status|e2e|shell} [project] | iso gate [pfm|templates] | iso {run|sim} <command…> | iso sim-reset" >&2; exit 2 ;;
  esac
}

usage() {
  cat >&2 <<EOF
usage: dev.sh <command> [project]

commands:
  status [project]       toolchain, projects, git, install state (default);
                         a project narrows the scope to that project's toolchain
  install                fetch dependencies
  build                  compile
  typecheck              vet / tsc --noEmit
  verify                 pre-test gates (pfm: go vet, fmt-check, lint-new, architecture ratchet;
                         templates: clone ratchet, leak + token gates) — fence only
  test                   run the test suite (pfm: unit rows, then the e2e rows) — fence only
  cover                  pfm coverage: unit + e2e profiles merged, thresholded (.testcoverage.yml) — fence only
  all                    verify + build + test for the project — fence only
  gate [project]         verify + test rows as concurrent steps in one run, a per-step
                         table (step · verdict · seconds) in the timing dir — fence only
  iso <cmd> [project]    run any command above — plus e2e | shell — inside the
                         pfm-dev container fence (infra/), worktree mounted
  iso gate [pfm|templates]
                         the flight gate: both projects (or one) in ONE container,
                         concurrent steps, the per-step table, the gate wall budget
  iso sim <command…>     run a command in the real-simulation fence: Google Chrome,
                         an X display, pfm installed from the worktree, browser rung on
  iso sim-reset          drop this worktree's sim harvester volume

projects: ${PROJECTS[*]} | all (default)

Every project that cannot be checked reports TOOLCHAIN-MISSING or NOT-INSTALLED
and exits non-zero. A skipped check is never a pass.
EOF
  exit 2
}

CMD="${1:-status}"
TARGET="${2:-all}"

# A suite never runs on the host: its tests spawn tmux sessions, git repos and
# processes against whatever machine they run on. The fence sets
# PFM_DEV_FENCE=1 (infra/fence/docker-compose.yml, lanes/container.sh).
case "$CMD" in
  test|cover|all|verify|gate)
    if [[ -z "${PFM_DEV_FENCE:-}" ]]; then
      echo "FENCE-ONLY: dev.sh $CMD runs test suites and never on the host — run: .claude/scripts/dev.sh iso $CMD ${2:-}" >&2
      exit 2
    fi ;;
esac

case "$CMD" in
  status) cmd_status "$TARGET" ;;
  install|build|test|e2e|typecheck|verify|cover|all)
    if [[ "$TARGET" == "all" ]]; then
      for p in "${PROJECTS[@]}"; do head_ "$p :: $CMD"; dispatch "$p" "$CMD"; done
    else
      proj_dir "$TARGET" >/dev/null 2>&1 || { echo "unknown project: $TARGET" >&2; usage; }
      head_ "$TARGET :: $CMD"
      dispatch "$TARGET" "$CMD"
    fi ;;
  iso) cmd_iso "${@:2}" ;;
  gate)
    case "$TARGET" in
      pfm|templates|all) gate_run "$TARGET" || FAILURES=$((FAILURES + 1)) ;;
      *) echo "unknown gate target: $TARGET" >&2; usage ;;
    esac ;;
  -h|--help|help) usage ;;
  *) echo "unknown command: $CMD" >&2; usage ;;
esac

if (( FAILURES > 0 )); then
  printf '\n%s%d step(s) failed or could not run.%s\n' "$RED" "$FAILURES" "$OFF"
  exit 1
fi
printf '\n%sall steps passed.%s\n' "$GREEN" "$OFF"
