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
      | grep -vE '^[[:space:]]*$' | tail -n "$cap" | trim_line "$chars" | sed 's/^/        /'
    info "log: $abs"; return
  fi
  local failed failpkgs pkg test body total n=0
  failed="$(jq -r 'select(.Action=="fail" and .Test != null) | .Package + "\t" + .Test' "$json" | sort -u)"
  failpkgs="$(jq -r 'select(.Action=="fail" and .Test == null) | .Package' "$json" | sort -u)"
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
        printf '%s\n' "$body" | head -n "$cap" | trim_line "$chars" | sed 's/^/        /'
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
      | grep -vE '^(ok|FAIL|PASS)|^[[:space:]]*$' | head -n "$cap" | trim_line "$chars" | sed 's/^/        /'
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

act_templates() { # the shipped product: mechanical gates, no build
  local action="$1"
  case "$action" in
    install|build|typecheck|cover) info "templates: no $action step (markdown + shell)" ;;
    verify|test|all)
      # Clone ratchet over the shell / JS / Python surface (scripts/clone-check.sh,
      # jscpd against .jscpd-baseline.json): a NEW clone fails, named; its own
      # broken state is `CLONES ERROR` and rc 2, never a PASS.
      run "templates: clone ratchet (jscpd)" -- bash "$REPO_ROOT/scripts/clone-check.sh"
      # The lane↔landscape map gate (infra/fence/lanes/check-map.sh). --no-derive
      # skips the command/tool surface derive, which needs a built pfm; its own
      # broken state is a named red line and rc 1/2, never a silent pass.
      run "templates: lane↔landscape map (check-map)" -- bash "$REPO_ROOT/infra/fence/lanes/check-map.sh" --no-derive
      run "templates: lane library self-tests" -- bash -c 'for t in "$1"/infra/fence/lanes/tests/*_test.sh; do echo "== $t"; bash "$t" || exit 1; done' _ "$REPO_ROOT"
      head_ "templates — leak gate"
      # EVERY tracked file in this repo is published, so the changed set is the
      # whole working tree — not a `templates scripts README INSTALL CHANGELOG
      # releases` pathspec. Under that pathspec a change touching docs/,
      # .claude/, .codex/, .professor/ or infra/ printed "leak-check clean (N
      # changed file(s))" having scanned none of it, and the count made the
      # claim look earned. Deleted paths are dropped and COUNTED here rather
      # than handed to leak-check, whose --files mode correctly refuses to call
      # a list of non-files clean.
      local changed present gone
      changed=$(repo_git status --porcelain | awk '{print $NF}' | grep -v '/$' || true)
      present=()
      gone=0
      local candidate
      while IFS= read -r candidate; do
        [[ -z "$candidate" ]] && continue
        if [[ -f "$candidate" ]]; then present+=("$candidate"); else gone=$((gone + 1)); fi
      done <<<"$changed"
      if (( ${#present[@]} > 0 )); then
        if scripts/leak-check.sh --files "${present[@]}"; then
          ok "leak-check clean (${#present[@]} changed file(s) scanned, ${gone} deleted path(s) skipped)"
        else
          fail_step "leak-check FAILED — brand / PII / machine-path string in a changed public file"
        fi
      elif (( gone > 0 )); then
        ok "leak-check: every one of the ${gone} changed path(s) is a deletion — nothing to scan, nothing could leak"
      else
        info "working tree clean — scanning the whole tracked templates tree instead"
        # shellcheck disable=SC2046
        if repo_git ls-files templates README.md INSTALL.md | xargs scripts/leak-check.sh --files; then
          ok "leak-check clean (full tracked scan of templates/ + README + INSTALL — NOT the whole repo)"
        else
          fail_step "leak-check FAILED — brand / PII / machine-path string in a public file"
        fi
      fi

      head_ "templates — placeholder registry"
      # Scope: markdown templates only. Shell/JS templates use {VAR} for their own
      # runtime values, which are not install placeholders and never will be.
      # PLACEHOLDERS.md registers BOTH classes a markdown template can carry —
      # install placeholders SETUP fills, and the runtime metavariables it must
      # NOT fill (its own § Runtime metavariables). So an unregistered token is
      # genuinely unruled, not merely uncategorised, and FAILS: a warning here
      # was read by nobody and let a token sit unruled release after release.
      local used unregistered out
      used=$(grep -rhoE '\{[A-Z][A-Z0-9_]+\}' --include='*.md' templates 2>/dev/null | sort -u || true)
      if [[ -z "$used" ]]; then
        fail_step "placeholder scan produced NO tokens at all — the SCAN is broken, not the templates"
      else
        unregistered=$(comm -23 <(printf '%s\n' "$used") \
                                <(grep -ohE '\{[A-Z][A-Z0-9_]+\}' docs/PLACEHOLDERS.md | sort -u))
        if [[ -z "$unregistered" ]]; then
          ok "every markdown-template token is registered in PLACEHOLDERS.md ($(wc -l <<<"$used") tokens)"
        else
          if [[ -n "${PFM_DEV_FENCE:-}" ]]; then
            out="$(mktemp)"
          else
            out="$TMP_BASE/templates/unregistered-tokens.txt"
            mkdir -p "$TMP_BASE/templates"
          fi
          printf '%s\n' "$unregistered" > "$out"
          fail_step "$(wc -l <<<"$unregistered") of $(wc -l <<<"$used") markdown-template tokens are absent from PLACEHOLDERS.md — register each as an install placeholder or under § Runtime metavariables"
          info "most frequent 10 (full list: $out):"
          grep -rhoE '\{[A-Z][A-Z0-9_]+\}' --include='*.md' templates \
            | grep -xFf "$out" | sort | uniq -c | sort -rn | head -10 \
            | while read -r n tok; do info "  ${n}x  $tok"; done
        fi
      fi

      head_ "templates — scratch-path policy"
      # Scratch artifacts belong in /tmp/<project>/<purpose>, never in a repo-local
      # tmp/. This catches the straggler an edit pass missed, which is the whole
      # point: it enumerates tracked files rather than trusting that the sweep was
      # complete. Its own broken state is distinct — a git listing that cannot be
      # read is a FAIL naming git, never an empty sweep reported clean.
      # NUL-delimited through a file: a command substitution drops NUL bytes, so
      # capturing `ls-files -z` into a variable silently collapses the list into
      # one blob and the scan reports clean because it scanned nothing.
      mkdir -p "$TMP_BASE/templates"
      if ! repo_git ls-files -z > "$TMP_BASE/templates/tracked.z" 2>/dev/null; then
        fail_step "scratch-path policy: the tracked-file list could not be read from git — nothing was scanned"
      else
        # Excluded, and SAID so rather than filtered in silence: shipped release
        # notes, the retro ledger, generated mirrors, and the two measurement
        # records that name where a past capture actually landed — rewriting
        # those would misstate history. An exclusion that hides its own work is
        # the next bug, so the count and the list are printed on every run.
        local exclude='^(releases/|CHANGELOG\.md|\.codex/|\.opencode/|AGENTS\.md|\.professor/retro\.md$|docs/dev/testing/timing\.md$|pfm/\.testtiming\.yml$)'
        # grep needs /dev/null as a second operand: BSD xargs runs the utility
        # even on empty input, and a bare `grep PATTERN` then reads stdin and
        # hangs the gate forever instead of reporting an empty sweep.
        all_hits=$(xargs -0 grep -lE '(^|[^/[:alnum:]_.-])tmp/(timing|flights|lanes|guard|professor_)' /dev/null \
          < "$TMP_BASE/templates/tracked.z" 2>/dev/null || true)
        strays=$(printf '%s\n' "$all_hits" | grep -vE "$exclude" | grep -v '^$' || true)
        excluded=$(printf '%s\n' "$all_hits" | grep -cE "$exclude" || true)
        info "scratch-path scan: $excluded historical-record path(s) excluded by name (release notes, retro ledger, mirrors, measurement records)"
        if [[ -z "$strays" ]]; then
          ok "no tracked file writes a repo-local tmp/ (scratch lives under /tmp/<project>/)"
        else
          fail_step "$(wc -l <<<"$strays" | tr -d ' ') tracked file(s) still name a repo-local tmp/ path — repoint them at /tmp/<project>/<purpose>"
          while read -r f; do [[ -n "$f" ]] && info "  $f"; done <<< "$strays"
        fi
      fi

      head_ "templates — description registry"
      # A `description:` is the routing registry (/quality:description). An
      # unquoted `: ` in one breaks the YAML: Claude Code's lenient parser still
      # registers the entry, a stricter runtime silently drops it, and no prompt
      # rule can see it. The script distinguishes its own broken state from a
      # clean tree (exit 2 toolchain, 3 empty scan, 4 git could not list the
      # repository, 1 real failure); any other exit is the script crashing.
      if scripts/description-check.sh; then
        ok "every tracked frontmatter parses; description budget reported above"
      else
        case $? in
          1) fail_step "a tracked frontmatter does not parse as YAML — quote the value or remove the bare ': ' (see the list above)" ;;
          2) fail_step "description-check could not run (python3/PyYAML absent) — NO frontmatter was parsed" ;;
          3) fail_step "description-check scanned nothing — the SCAN is broken, not the tree" ;;
          4) fail_step "description-check could not locate or list the repository through git — NO frontmatter was parsed" ;;
          *) fail_step "description-check crashed (exit $?) — NOT a verdict on the tree" ;;
        esac
      fi

      head_ "templates — generate the engine mirrors"
      # The mirrors (AGENTS.md, .codex/**, .opencode/**) are untracked: a fresh
      # clone holds none, so verify generates them from the Claude sources
      # before any gate reads them. Current mirrors are left alone (the fence
      # mounts the tree read-only and CI generates on the host first); the
      # tree's own compiler runs, never a host pfm binary (a stale host build
      # rewrites what it does not understand).
      if ! need_tool go templates || ! need_tool node templates; then
        fail_step "mirror generation could not run — no mirror gate below is a verdict on the tree"
      elif (cd "$REPO_ROOT/pfm" && go run ./cmd/pfm codex check "$REPO_ROOT") >/dev/null 2>&1 \
        && (cd "$REPO_ROOT/pfm" && go run ./cmd/pfm opencode check "$REPO_ROOT") >/dev/null 2>&1; then
        ok "engine mirrors current — nothing generated"
      elif (cd "$REPO_ROOT/pfm" && go run ./cmd/pfm codex build "$REPO_ROOT") \
        && (cd "$REPO_ROOT/pfm" && go run ./cmd/pfm opencode build "$REPO_ROOT"); then
        ok "engine mirrors generated from the Claude sources"
      else
        fail_step "mirror generation FAILED — no mirror gate below is a verdict on the tree (see output)"
      fi

      head_ "templates — codex generated-marker claim"
      # The templates dir's shipped JS compiler and this repo's `pfm codex build`
      # write the same $HOME/.codex outputs on adopter hosts. A copy that stops
      # claiming the other's marker reports its files STALE forever; the gate
      # also reconciles every marked file's declared source against disk, so a
      # fossil generated from a deleted file fails BY NAME.
      if node scripts/check-codex-markers.mjs; then
        ok "compiler marker claims hold; every marked file has a live source"
      else
        fail_step "codex marker claim FAILED — a stranded marker or an orphaned generated file (see output)"
      fi

      head_ "templates — generated agent rosters"
      if node scripts/check-agent-roster.mjs; then
        ok "Codex and OpenCode agent rosters match their Claude sources"
      else
        fail_step "agent roster FAILED — a source role is missing or cannot perform its protocol"
      fi

      head_ "templates — token-audit pricing"
      if node scripts/check-token-pricing.mjs; then
        ok "every published model id resolves to its intended rate"
      else
        fail_step "token pricing FAILED — a published model id resolves to the wrong rate, or the PRICING table could not be read (see output)"
      fi

      head_ "templates — token-audit tests"
      if node --test templates/global/commands/tokens/; then
        ok "token-audit reads Claude and Codex transcripts and selects a flight's agents"
      else
        fail_step "token-audit tests FAILED — a measure, the flight selection, or an error path regressed, or node could not run the suite (see output)"
      fi

      head_ "templates — codex-sync missing compiler"
      if bash "$REPO_ROOT/scripts/test-codex-sync.sh" "$REPO_ROOT/templates/project/scripts/codex-sync.sh"; then
        ok "codex-sync names unavailable compiler and retains dirty flag"
      else
        fail_step "codex-sync regression FAILED — unavailable compiler must be named and dirty flag retained"
      fi

      head_ "templates — native opencode mirror"
      # Build the source-under-test inside the fence; verification must never
      # depend on or install a host binary. The ignored artifact also gives this
      # repo's Stop hook a current compiler while develop remains uninstalled.
      local opencode_bin="/pfm-timing/pfm-dev-bin"
      if need_tool go templates && go -C pfm build -o "$opencode_bin" ./cmd/pfm \
        && "$opencode_bin" opencode check "$REPO_ROOT" --home "/pfm-timing/opencode-verify-home" \
        && "$opencode_bin" opencode doctor "$REPO_ROOT" --home "/pfm-timing/opencode-verify-home"; then
        ok "opencode mirror current and parseable"
      else
        fail_step "opencode mirror FAILED — run: pfm opencode build $REPO_ROOT"
      fi

      head_ "templates — OpenCode writer references"
      if node "$REPO_ROOT/scripts/check-opencode-writer.mjs"; then
        ok "live surfaces use native pfm opencode"
      else
        fail_step "OpenCode writer reference FAILED — use native pfm opencode on every named surface"
      fi
      head_ "templates — self-hosted manifest"
      if bash infra/check-self-hosted-manifest.sh "$REPO_ROOT" templates pfm; then
        ok "self-hosted manifest version, roster, and hashes match the repository"
      else
        fail_step "self-hosted manifest FAILED — its install ledger is stale or unreadable"
      fi

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
    build)   run "pfm: go build" -- go -C "$d" build ./... ;;
    typecheck) run "pfm: go vet" -- go -C "$d" vet ./... ;;
    verify)
      run "pfm: go vet" -- go -C "$d" vet ./...
      # Formatting and lint through the pinned golangci-lint (infra/fence/tools.env):
      # the Makefile names TOOLCHAIN-MISSING when the tool is absent — `make
      # tools` on the host; the fence image bakes it in. lint-new judges only
      # lines changed since origin/develop; `make lint` is the full backlog.
      run "pfm: fmt-check (gofumpt + gci + golines)" -- make -C "$d" --no-print-directory fmt-check
      run "pfm: lint-new (golangci-lint, changed lines)" -- make -C "$d" --no-print-directory lint-new
      # The architecture ratchet (C1–C21 vs pfm/.arch/). Its own broken state
      # is rc 2 (an enumerator or grep that could not run), never a PASS.
      run "pfm: architecture ratchet" -- bash "$d/scripts/arch-check.sh"
      # The gate scripts' own fixture suites: a ratchet nobody tests is trusted
      # on faith. Each prints "N passed, M failed" and is non-zero on any FAIL.
      run "pfm: gate-script self-tests" -- bash -c 'for t in "$1"/scripts/*_test.sh; do echo "== $t"; bash "$t" || exit 1; done' _ "$d" ;;
    # -count=1 is not optional: without it a package whose inputs are unchanged
    # reports `ok  (cached)`, and this gate would call a run it never watched a
    # pass. -timeout is measured, not guessed — internal/index's OpenCode WAL
    # stress test alone takes ~4.5 minutes (268s watched), so the 10m default
    # turns an ordinary loaded host into a red suite that names the wrong cause.
    test)
      local flags_text timing_base timing_run
      local testflags=()
      if ! flags_text="$(make -s -C "$d" --no-print-directory testflags)"; then
        fail_step "pfm: TESTFLAGS could not be read from Makefile"; return
      fi
      read -r -a testflags <<< "$flags_text"
      timing_base="${PFM_TEST_TIMING_DIR:-$TMP_BASE/timing}"
      mkdir -p "$timing_base"
      timing_run="$(mktemp -d "$timing_base/run.XXXXXX")"
      # Positional arguments keep flags and output paths out of shell code.
      # The JSON is retained even on failure; timing is a separate verdict.
      run "pfm: go test" -- bash -c '
        go -C "$1" test "${@:3}" -count=1 -timeout 25m -json ./... >"$2"
      ' _ "$d" "$timing_run/unit.json" "${testflags[@]}"
      go_test_report "$timing_run/unit.json"
      skip_gate "pfm: skipped tests are all listed (unit)" "$timing_run/unit.json"
      run "pfm: test timing (budget)" -- bash "$d/scripts/test-timing.sh" \
        --check --suite unit --out "$timing_run/unit.tsv" "$timing_run/unit.json"
      # Tagged Tier A runs serially and has its own budget and artifact.
      run "pfm: e2e (tagged)" -- bash -c '
        go -C "$1" test -tags e2e -p 1 -count=1 -timeout 25m -json ./e2e/... >"$2"
      ' _ "$d" "$timing_run/e2e.json"
      go_test_report "$timing_run/e2e.json"
      skip_gate "pfm: skipped tests are all listed (e2e)" "$timing_run/e2e.json"
      run "pfm: e2e timing (budget)" -- bash "$d/scripts/test-timing.sh" \
        --check --suite e2e --out "$timing_run/e2e.tsv" "$timing_run/e2e.json" ;;
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
# THIS checkout — the worktree this script belongs to — mounted at /work: a
# fresh machine per run (own HOME, own tmux, no published ports). Files are
# edited on the host; the container only builds and tests.
# First output line is the fence proof (container hostname + HOME + /work).
# BROKEN STATE: docker missing, its DAEMON unreachable, or the compose file
# missing = TOOLCHAIN-MISSING and a non-zero exit — never a host fallback; a run
# that cannot print its fence proof did not run inside the fence. The daemon is
# probed explicitly: an installed `docker` binary with nothing behind it is the
# common failure, and it must be named as TOOLCHAIN-MISSING here rather than
# surfacing later as an opaque compose connect error.
cmd_iso() { # cmd_iso <action> [project]
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
  mkdir -p "$TMP_BASE/timing"
  extra+=(-v "$TMP_BASE/timing:/pfm-timing" -e PFM_TEST_TIMING_DIR=/pfm-timing)
  if [[ -n "${TESTFLAGS+x}" ]]; then extra+=(-e "TESTFLAGS=$TESTFLAGS"); fi
  local proof='echo "fence: container=$(hostname) HOME=$HOME work=$(pwd)"'
  case "$action" in
    shell)
      docker compose -f "$compose" run --rm --build ${extra[@]+"${extra[@]}"} pfm-dev zsh -c "$proof; exec zsh -i" ;;
    e2e)
      docker compose -f "$compose" run --rm --build ${extra[@]+"${extra[@]}"} pfm-dev bash -c "$proof; go -C pfm test -count=1 -tags e2e -p 1 ./e2e/..." ;;
    install|build|typecheck|verify|test|cover|all|status)
      docker compose -f "$compose" run --rm --build ${extra[@]+"${extra[@]}"} pfm-dev bash -c "$proof; ./.claude/scripts/dev.sh $action $target" ;;
    run)
      # An arbitrary command inside the fence, from the worktree root — for the
      # probes the fixed rows do not cover (`go test -json ./cmd/pfm`, a single
      # package, `make -C pfm lint`). Exit status is the command's own.
      local cmd="${*:2}"
      [[ -z "$cmd" ]] && { echo "usage: dev.sh iso run <command…>" >&2; exit 2; }
      docker compose -f "$compose" run --rm --build ${extra[@]+"${extra[@]}"} pfm-dev bash -c "$proof; $cmd" ;;
    *)
      echo "usage: dev.sh iso {install|build|typecheck|verify|test|cover|all|status|e2e|shell} [project] | iso run <command…>" >&2; exit 2 ;;
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
                         templates: clone ratchet, leak + token gates)
  test                   run the test suite
  cover                  pfm coverage: unit + e2e profiles merged, thresholded (.testcoverage.yml)
  all                    verify + build + test for the project
  iso <cmd> [project]    run any command above — plus e2e | shell — inside the
                         pfm-dev container fence (infra/), worktree mounted

projects: ${PROJECTS[*]} | all (default)

Every project that cannot be checked reports TOOLCHAIN-MISSING or NOT-INSTALLED
and exits non-zero. A skipped check is never a pass.
EOF
  exit 2
}

CMD="${1:-status}"
TARGET="${2:-all}"

case "$CMD" in
  status) cmd_status "$TARGET" ;;
  install|build|test|typecheck|verify|cover|all)
    if [[ "$TARGET" == "all" ]]; then
      for p in "${PROJECTS[@]}"; do head_ "$p :: $CMD"; dispatch "$p" "$CMD"; done
    else
      proj_dir "$TARGET" >/dev/null 2>&1 || { echo "unknown project: $TARGET" >&2; usage; }
      head_ "$TARGET :: $CMD"
      dispatch "$TARGET" "$CMD"
    fi ;;
  iso) cmd_iso "${2:-}" "${3:-pfm}" ;;
  -h|--help|help) usage ;;
  *) echo "unknown command: $CMD" >&2; usage ;;
esac

if (( FAILURES > 0 )); then
  printf '\n%s%d step(s) failed or could not run.%s\n' "$RED" "$FAILURES" "$OFF"
  exit 1
fi
printf '\n%sall steps passed.%s\n' "$GREEN" "$OFF"
