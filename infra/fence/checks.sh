#!/usr/bin/env bash
# Check catalogue sourced by dev.sh. The serial blocks retain their original rows.

checks_templates_clone() {
  # Clone ratchet over the shell / JS / Python surface (scripts/clone-check.sh,
  # jscpd against .jscpd-baseline.json): a NEW clone fails, named; its own
  # broken state is `CLONES ERROR` and rc 2, never a PASS.
  run "templates: clone-check self-test" -- bash "$REPO_ROOT/scripts/test-clone-check.sh"
  run "templates: clone ratchet (jscpd)" -- bash "$REPO_ROOT/scripts/clone-check.sh"
}

checks_templates_check_map_serial() {
  # The lane↔command map gate (infra/fence/lanes/check-map.sh). --no-derive
  # skips the command/tool surface derive, which needs a built pfm; its own
  # broken state is a named red line and rc 1/2, never a silent pass.
  run "templates: lane↔command map (check-map)" -- bash "$REPO_ROOT/infra/fence/lanes/check-map.sh" --no-derive
}

checks_templates_lanes_serial() {
  run "templates: lane library self-tests" -- bash -c 'for t in "$1"/infra/fence/lanes/tests/*_test.sh; do echo "== $t"; bash "$t" || exit 1; done' _ "$REPO_ROOT"
}

checks_templates_demo_serial() {
  run "templates: demo fence self-tests" -- bash -c 'for t in "$1"/infra/demo/tests/*_test.sh; do echo "== $t"; bash "$t" || exit 1; done' _ "$REPO_ROOT"
}

checks_templates_leak() {
  head_ "templates — leak gate"
  run "templates: leak-check self-test" -- bash "$REPO_ROOT/scripts/test-leak-check.sh"
  # EVERY tracked file in this repo is published, so the gate scans the
  # whole tracked tree — the same set CI scans — plus every changed or
  # untracked non-ignored file, whatever the tree's state. A pathspec here
  # once printed "clean" having scanned none of docs/, .claude/ or infra/,
  # and the count made the claim look earned. Deleted paths are dropped and
  # COUNTED rather than handed to leak-check, whose --files mode correctly
  # refuses to call a list of non-files clean. BROKEN STATE: a git listing
  # that could not be read is a red row, never a scan of nothing.
  local tracked porcelain changed candidate leak_list
  local present=0 changed_n=0 gone=0
  if ! tracked="$(repo_git ls-files)" || ! porcelain="$(repo_git status --porcelain --untracked-files=all)"; then
    fail_step "leak-check: the tracked or changed file list could not be read from git — nothing was scanned"
  else
    changed=$(awk '{print $NF}' <<<"$porcelain" | grep -v '/$' || true)
    mkdir -p "$TMP_BASE/templates"
    leak_list="$TMP_BASE/templates/leak-candidates.z"
    : > "$leak_list"
    while IFS= read -r candidate; do
      [[ -z "$candidate" ]] && continue
      if [[ -f "$candidate" ]]; then
        printf '%s\0' "$candidate" >> "$leak_list"; present=$((present + 1))
      else
        gone=$((gone + 1))
      fi
    done < <(printf '%s\n%s\n' "$tracked" "$changed" | sort -u)
    while IFS= read -r candidate; do
      [[ -n "$candidate" && -f "$candidate" ]] && changed_n=$((changed_n + 1))
    done <<<"$changed"
    if (( present == 0 )); then
      fail_step "leak-check: the candidate list holds no file present on disk — the LIST is broken, nothing was scanned"
    # xargs batches the list so no argument list overflows; any red batch
    # makes xargs non-zero, and a non-empty list never yields an empty batch.
    elif xargs -0 scripts/leak-check.sh --files < "$leak_list"; then
      ok "leak-check clean (${present} file(s): the whole tracked tree plus ${changed_n} changed, ${gone} deleted path(s) skipped)"
    else
      fail_step "leak-check FAILED — brand / PII / machine-path string in a public file, or a batch could not be scanned"
    fi
  fi

}

checks_templates_placeholders() {
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

}

checks_templates_scratch_paths() {
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
      ok "no tracked file names a migrated scratch purpose under a repo-local tmp/"
    else
      fail_step "$(wc -l <<<"$strays" | tr -d ' ') tracked file(s) still name a repo-local tmp/ path — repoint them at /tmp/<project>/<purpose>"
      while read -r f; do [[ -n "$f" ]] && info "  $f"; done <<< "$strays"
    fi

    # The sweep above only knows the purposes one migration moved. The policy
    # itself is checked on CODE (prose that merely mentions a path is not a
    # write): (A) a repo-rooted tmp/ — `$ROOT/tmp/`, `path.join(repoRoot, 'tmp')`,
    # Go's cwd-relative `filepath.Join("tmp", …)`; (B) a fixed-name bare
    # `/tmp/<name>` a host run shares with every other checkout. Allowed: the
    # derived `/tmp/$PROJECT/…` forms, anonymous `mktemp` templates (XXXXXX),
    # and tmux's own `/tmp/tmux-<uid>` socket dir. Container-only code (the
    # fence and demo lanes, the e2e docker run) and test fixtures are excluded
    # BY NAME and counted, never filtered in silence; zero code files scanned
    # is a broken scan, not a clean tree. A comment line names a path, it does
    # not write one, so hits whose text opens with #, // or * are dropped.
    local code_ext='\.(sh|bash|mjs|js|ts|py|go)$'
    local code_skip='^(infra/(fence|demo)/|scripts/e2e-linux\.sh$)|(_test\.go|\.test\.(mjs|js|ts))$|/testdata/'
    local code_z="$TMP_BASE/templates/tracked-code.z" code_n code_skipped repo_local bare
    grep -zE "$code_ext" < "$TMP_BASE/templates/tracked.z" | grep -zvE "$code_skip" > "$code_z" || true
    code_n=$(tr -cd '\0' < "$code_z" | wc -c | tr -d ' ')
    code_skipped=$(grep -zE "$code_ext" < "$TMP_BASE/templates/tracked.z" | grep -zcE "$code_skip" || true)
    if [[ "$code_n" -eq 0 ]]; then
      fail_step "scratch-path policy: NO tracked code file was scanned — the SCAN is broken, not the tree"
    else
      repo_local=$(xargs -0 grep -nE '(\$\{?(ROOT|REPO_ROOT|repo_root|repoRoot|WORKTREE)\}?|\{repo-root\})/tmp/|path\.join\([A-Za-z_]+, *['"'"'"]tmp['"'"'"]|filepath\.Join\("tmp"' \
        /dev/null < "$code_z" 2>/dev/null | grep -vE '^[^:]+:[0-9]+:[[:space:]]*(#|//|\*)' || true)
      bare=$(xargs -0 grep -nE '(^|[^A-Za-z0-9_}.-])/tmp/[A-Za-z0-9_.-]' /dev/null < "$code_z" 2>/dev/null \
        | grep -vE '^[^:]+:[0-9]+:[[:space:]]*(#|//|\*)' | grep -vE 'XXXXXX|/tmp/tmux-' || true)
      info "scratch-path policy: $code_n code file(s) scanned; $code_skipped excluded by name (container-only lanes, the e2e docker run, test fixtures)"
      if [[ -z "$repo_local$bare" ]]; then
        ok "no tracked code writes a repo-local tmp/ or a fixed-name bare /tmp path"
      else
        fail_step "$(printf '%s\n%s\n' "$repo_local" "$bare" | grep -c . | tr -d ' ') scratch write(s) outside /tmp/<project>/<purpose> — derive the project dir, or use an anonymous mktemp"
        while read -r hit; do info "  $hit"; done < <(printf '%s\n%s\n' "$repo_local" "$bare" | grep .)
      fi
    fi
  fi

}

checks_templates_descriptions() {
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

}

checks_templates_mirrors_generate() {
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

}

checks_templates_mirrors_marker() {
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

}

checks_templates_mirrors_roster() {
  head_ "templates — generated agent rosters"
  if node scripts/check-agent-roster.mjs; then
    ok "Codex and OpenCode agent rosters match their Claude sources"
  else
    fail_step "agent roster FAILED — a source role is missing or cannot perform its protocol"
  fi

}

checks_templates_token_pricing() {
  head_ "templates — token-audit pricing"
  if node scripts/check-token-pricing.mjs; then
    ok "every published model id resolves to its intended rate"
  else
    fail_step "token pricing FAILED — a published model id resolves to the wrong rate, or the PRICING table could not be read (see output)"
  fi

}

checks_templates_token_audit() {
  head_ "templates — token-audit tests"
  # node --test exits 0 when it finds no tests, so a moved or renamed suite
  # would read as a pass: enumerate the files and count the passes instead.
  local token_tests=(templates/global/commands/tokens/*.test.mjs)
  local token_out="$TMP_BASE/templates/token-audit.tap"
  mkdir -p "$TMP_BASE/templates"
  if [[ ! -f "${token_tests[0]}" ]]; then
    fail_step "token-audit tests NOT RUN — templates/global/commands/tokens/*.test.mjs matched no file; the suite was never executed"
  elif ! node --test --test-reporter=tap "${token_tests[@]}" >"$token_out" 2>&1; then
    cat "$token_out"
    fail_step "token-audit tests FAILED — a measure, the flight selection, or an error path regressed, or node could not run the suite (see output)"
  elif ! awk '/^# pass /{ if ($3 > 0) found=1 } END{ exit !found }' "$token_out"; then
    cat "$token_out"
    fail_step "token-audit tests NOT RUN — the suite reported zero passing tests; a green exit with no test is not a pass"
  else
    ok "token-audit reads Claude and Codex transcripts and selects a flight's agents ($(awk '/^# pass /{print $3}' "$token_out") passing)"
  fi

}

checks_templates_release_check() {
  head_ "templates — release-check tests and the notes grammar"
  node_test_suite "release-check" "$TMP_BASE/templates/release-check.tap" scripts/release-check.test.mjs
  if node scripts/release-check.mjs notes --all releases >"$TMP_BASE/templates/release-notes.txt" 2>&1; then
    ok "release notes from v0.78.0 on follow docs/RELEASE.md § Release notes ($(grep -m1 '^CHECKED' "$TMP_BASE/templates/release-notes.txt" || echo 'CHECKED line MISSING'))"
  else
    cat "$TMP_BASE/templates/release-notes.txt"
    fail_step "release notes grammar FAILED — a note breaks docs/RELEASE.md § Release notes, or release-check could not run (exit 2 is an ERROR, see output)"
  fi

}

checks_templates_codex_sync() {
  head_ "templates — codex-sync missing compiler"
  local cs_copy
  for cs_copy in templates/project/scripts/codex-sync.sh .claude/scripts/codex-sync.sh; do
    if bash "$REPO_ROOT/scripts/test-codex-sync.sh" "$REPO_ROOT/$cs_copy"; then
      ok "codex-sync ($cs_copy) names unavailable compiler and retains dirty flag"
    else
      fail_step "codex-sync regression FAILED ($cs_copy) — unavailable compiler must be named and dirty flag retained"
    fi
  done

}

checks_templates_refresh_scope() {
  head_ "templates — refresh-scope fixtures"
  run "templates: refresh-scope fixtures" -- python3 "$REPO_ROOT/scripts/test-refresh-scope.py"

}

checks_templates_pfm_guard() {
  head_ "templates — pfm-guard across repositories"
  if bash "$REPO_ROOT/scripts/test-pfm-guard.sh" "$REPO_ROOT/templates/project/scripts/pfm-guard.sh" && bash "$REPO_ROOT/scripts/test-pfm-guard.sh" "$REPO_ROOT/.claude/scripts/pfm-guard.sh"; then
    ok "pfm-guard finds the session's law stamp for another repo's edit and denies an unread law"
  else
    fail_step "pfm-guard regression FAILED — a cross-repo edit must open on the session's law stamp and stay shut without one"
  fi

}

checks_templates_dev_report() {
  head_ "templates — go test report under pipefail"
  if bash "$REPO_ROOT/scripts/test-dev-report.sh" "$REPO_ROOT/.claude/scripts/dev.sh"; then
    ok "go_test_report reaches its log line on filtered and over-cap failure output"
  else
    fail_step "go_test_report regression FAILED — a failing stream aborted the report before its verdict (see output)"
  fi

}

checks_templates_mirrors_opencode() {
  head_ "templates — native opencode mirror"
  # Build the source-under-test inside the fence; verification must never
  # depend on or install a host binary. The ignored artifact also gives this
  # repo's Stop hook a current compiler while develop remains uninstalled.
  # /pfm-timing exists only as the fence's bind mount; on the host the same
  # scratch is $TMP_BASE/timing, so resolve it the way the timing ledger does.
  local opencode_scratch="${PFM_TEST_TIMING_DIR:-$TMP_BASE/timing}"
  local opencode_bin="$opencode_scratch/pfm-dev-bin"
  local opencode_home="$opencode_scratch/opencode-verify-home"
  if need_tool go templates && mkdir -p "$opencode_scratch" \
    && go -C pfm build -o "$opencode_bin" ./cmd/pfm \
    && "$opencode_bin" opencode check "$REPO_ROOT" --home "$opencode_home" \
    && "$opencode_bin" opencode doctor "$REPO_ROOT" --home "$opencode_home"; then
    ok "opencode mirror current and parseable"
  else
    fail_step "opencode mirror FAILED — run: pfm opencode build $REPO_ROOT"
  fi

}

checks_templates_opencode_writer_tests() {
  head_ "templates — OpenCode writer check tests"
  node_test_suite "opencode-writer check" "$TMP_BASE/templates/opencode-writer.tap" scripts/check-opencode-writer.test.mjs

}

checks_templates_codeprobe() {
  head_ "templates — codeprobe skill tests"
  local cp_out="$TMP_BASE/templates/codeprobe.txt"
  if [[ ! -f templates/global/skills/codeprobe/codeprobe_test.py ]]; then
    fail_step "codeprobe tests NOT RUN — templates/global/skills/codeprobe/codeprobe_test.py is missing"
  elif ! python3 -m unittest templates/global/skills/codeprobe/codeprobe_test.py >"$cp_out" 2>&1; then
    cat "$cp_out"
    fail_step "codeprobe tests FAILED — a verb or probe command regressed, or python3 could not run the suite (see output)"
  elif ! grep -Eq '^Ran [1-9][0-9]* tests?' "$cp_out"; then
    cat "$cp_out"
    fail_step "codeprobe tests NOT RUN — unittest ran zero tests; a green exit with no test is not a pass"
  elif grep -Eq 'skipped=[1-9]' "$cp_out"; then
    cat "$cp_out"
    fail_step "codeprobe tests SKIPPED — a skipped test is a named gap, never a pass"
  else
    ok "codeprobe verbs and probe commands hold ($(grep -Eo '^Ran [0-9]+ tests?' "$cp_out"))"
  fi

}

checks_templates_opencode_writer_refs() {
  head_ "templates — OpenCode writer references"
  if node "$REPO_ROOT/scripts/check-opencode-writer.mjs"; then
    ok "live surfaces use native pfm opencode"
  else
    fail_step "OpenCode writer reference FAILED — use native pfm opencode on every named surface"
  fi
}

checks_templates_mirrors_manifest() {
  head_ "templates — self-hosted manifest"
  if bash infra/check-self-hosted-manifest.sh "$REPO_ROOT" templates pfm; then
    ok "self-hosted manifest version, roster, and hashes match the repository"
  else
    fail_step "self-hosted manifest FAILED — its install ledger is stale or unreadable"
  fi
}

checks_templates() {
  checks_templates_clone
  checks_templates_check_map_serial
  checks_templates_lanes_serial
  checks_templates_demo_serial
  checks_templates_leak
  checks_templates_placeholders
  checks_templates_scratch_paths
  checks_templates_descriptions
  checks_templates_mirrors_generate
  checks_templates_mirrors_marker
  checks_templates_mirrors_roster
  checks_templates_token_pricing
  checks_templates_token_audit
  checks_templates_release_check
  checks_templates_codex_sync
  checks_templates_refresh_scope
  checks_templates_pfm_guard
  checks_templates_dev_report
  checks_templates_mirrors_opencode
  checks_templates_opencode_writer_tests
  checks_templates_codeprobe
  checks_templates_opencode_writer_refs
  checks_templates_mirrors_manifest
}

checks_pfm_verify() {
  local d="$1"
  run "pfm: go vet" -- go -C "$d" vet ./...
  # The release binaries ship for macOS: vet the darwin build tags too, so a
  # darwin-only file is judged here and not first by CI.
  run "pfm: go vet (darwin/arm64)" -- env GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go -C "$d" vet ./...
  # Formatting and lint through the pinned golangci-lint (infra/fence/tools.env):
  # the Makefile names TOOLCHAIN-MISSING when the tool is absent — `make
  # tools` on the host; the fence image bakes it in. lint-new judges only
  # lines changed since origin/main, the base CI uses; `make lint` is the full backlog.
  run "pfm: fmt-check (gofumpt + gci + golines)" -- make -C "$d" --no-print-directory fmt-check
  run "pfm: lint-new (golangci-lint, changed lines)" -- make -C "$d" --no-print-directory lint-new
  # The architecture ratchet (C1–C21 vs pfm/.arch/). Its own broken state
  # is rc 2 (an enumerator or grep that could not run), never a PASS.
  run "pfm: architecture ratchet" -- bash "$d/scripts/arch-check.sh"
  # The gate scripts' own fixture suites: a ratchet nobody tests is trusted
  # on faith. Each prints "N passed, M failed" and is non-zero on any FAIL.
  run "pfm: gate-script self-tests" -- bash -c 'for t in "$1"/scripts/*_test.sh; do echo "== $t"; bash "$t" || exit 1; done' _ "$d"
}

checks_pfm_history() { # timing base, current run; print newest usable unit stream
  local base="$1" current="$2" candidate
  [[ -d "$base" ]] || return 0
  while IFS= read -r candidate; do
    [[ "$candidate" == "$current/unit.json" ]] && continue
    if jq -s -e 'any(.[]; .Test != null)' "$candidate" >/dev/null 2>&1; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done < <(find "$base" -maxdepth 2 -type f -path '*/run.*/unit.json' -printf '%T@ %p\n' | sort -nr | cut -d' ' -f2-)
}

checks_pfm_unit() { # pfm dir, run dir, optional already-read TESTFLAGS
  local d="$1" run_dir="$2" flags_text="${3:-}" timing_base history
  local testflags=() shard_args=()
  if [[ -z "$flags_text" ]] && ! flags_text="$(make -s -C "$d" --no-print-directory testflags)"; then
    fail_step "pfm: TESTFLAGS could not be read from Makefile"; return
  fi
  read -r -a testflags <<< "$flags_text"
  timing_base="${PFM_TEST_TIMING_DIR:-$TMP_BASE/timing}"
  history="$(checks_pfm_history "$timing_base" "$run_dir")"
  [[ -z "$history" ]] || shard_args=(--history "$history")
  # Positional arguments keep flags and output paths out of shell code.
  # The JSON is retained even on failure; timing is a separate verdict.
  run "pfm: go test" -- bash "$d/scripts/test-shard.sh" run --out "$run_dir/unit.json" "${shard_args[@]}" -- "${testflags[@]}"
  go_test_report "$run_dir/unit.json"
  skip_gate "pfm: skipped tests are all listed (unit)" "$run_dir/unit.json"
  run "pfm: test timing (budget)" -- bash "$d/scripts/test-timing.sh" \
    --check --suite unit --out "$run_dir/unit.tsv" "$run_dir/unit.json"
}

checks_pfm_test() {
  local d="$1" flags_text timing_base timing_run
  # -count=1 is not optional: without it a package whose inputs are unchanged
  # reports `ok  (cached)`, and this gate would call a run it never watched a
  # pass. -timeout 25m bounds a hang while letting a loaded host finish: the
  # longest package (cmd/pfm) measured 133.5s in the fence, and the 10m
  # default leaves too little headroom under host contention.
  if ! flags_text="$(make -s -C "$d" --no-print-directory testflags)"; then
    fail_step "pfm: TESTFLAGS could not be read from Makefile"; return
  fi
  timing_base="${PFM_TEST_TIMING_DIR:-$TMP_BASE/timing}"
  if ! timing_run="$(timing_run_dir "$timing_base")"; then
    fail_step "pfm: timing run directory could not be created under $timing_base"; return
  fi
  checks_pfm_unit "$d" "$timing_run" "$flags_text"
  pfm_e2e_rows "$d" "$timing_run"
}

gate_budget_verdict() { # target, wall seconds, optional budget file
  local target="$1" wall="$2" budgets="${3:-$REPO_ROOT/infra/fence/gate-budget.yml}"
  local tolerance value limit name="gate($target)"
  if [[ ! -r "$budgets" ]]; then
    printf 'budget: ✗ %s — budget file unreadable: %s\n' "$name" "$budgets"
    return 1
  fi
  tolerance="$(awk '$1=="tolerance:" {print $2; exit}' "$budgets")"
  value="$(awk -v want="$target" '$1=="gate:" {inside=1; next} /^[a-z]/ {inside=0} inside && $1==want ":" {print $2; exit}' "$budgets")"
  if [[ -z "$tolerance" ]]; then
    printf 'budget: ✗ %s — no tolerance in %s\n' "$name" "$budgets"
    return 1
  fi
  if [[ ! "$tolerance" =~ ^[0-9]+(\.[0-9]+)?$ ]]; then
    printf 'budget: ✗ %s — invalid tolerance %s in %s\n' "$name" "$tolerance" "$budgets"
    return 1
  fi
  if [[ ! "$wall" =~ ^[0-9]+(\.[0-9]+)?$ ]]; then
    printf 'budget: ✗ %s — no wall time read (got %s); the gate table is missing or has no WALL row\n' "$name" "${wall:-nothing}"
    return 1
  fi
  wall="$(awk -v w="$wall" 'BEGIN { printf "%d", (w==int(w) ? w : int(w)+1) }')"
  if [[ -z "$value" ]]; then
    printf 'budget: ✗ %s — UNBUDGETED: no row in %s (add one, `unpinned` until three green runs)\n' "$name" "$(basename "$budgets")"
    return 1
  fi
  if [[ "$value" == unpinned ]]; then
    printf 'budget: %s unpinned — recorded, not judged: %ss\n' "$name" "$wall"
    return 0
  fi
  if [[ ! "$value" =~ ^[0-9]+$ ]]; then
    printf 'budget: ✗ %s — invalid budget %s in %s\n' "$name" "$value" "$budgets"
    return 1
  fi
  limit="$(awk -v v="$value" -v t="$tolerance" 'BEGIN { printf "%d", v * t }')"
  if (( wall > limit )); then
    printf 'budget: ✗ %s — %ss over budget %ss ×%s = %ss\n' "$name" "$wall" "$value" "$tolerance" "$limit"
    return 1
  fi
  printf 'budget: ✓ %s — %ss within %ss (budget %ss ×%s)\n' "$name" "$wall" "$limit" "$value" "$tolerance"
}

checks_templates_mirrors() {
  checks_templates_mirrors_generate
  checks_templates_mirrors_marker
  checks_templates_mirrors_roster
  checks_templates_mirrors_opencode
  checks_templates_mirrors_manifest
}

checks_gate_check_map() { # run dir
  local run_dir="$1"
  if go -C pfm build -o "$run_dir/check-map-pfm" ./cmd/pfm; then
    run "templates: lane↔command map (check-map)" -- bash infra/fence/lanes/check-map.sh --pfm "$run_dir/check-map-pfm"
  else
    fail_step "templates: check-map pfm build FAILED"
  fi
}

gate_run() { # pfm, templates, or all
  local target="${1:-all}" d="$REPO_ROOT/pfm" base run_dir row stem f step_rc=0 budget_rc=0
  case "$target" in pfm|templates|all) ;; *) printf 'gate_run: unknown target: %s\n' "$target" >&2; return 2 ;; esac
  if [[ "$target" == pfm || "$target" == all ]]; then
    need_tool go pfm || return 1
    need_tool make pfm || return 1
    need_tool jq pfm || return 1
  fi
  if [[ "$target" == templates || "$target" == all ]]; then
    need_tool go templates || return 1
    need_tool node templates || return 1
  fi
  base="${PFM_TEST_TIMING_DIR:-$TMP_BASE/timing}"
  if ! run_dir="$(timing_run_dir "$base")"; then
    fail_step "gate: timing run directory could not be created under $base"
    return 1
  fi
  steps_reset
  if [[ "$target" == pfm || "$target" == all ]]; then
    steps_add_heavy pfm.unit checks_pfm_unit "$d" "$run_dir"
    steps_add_heavy pfm.e2e pfm_e2e_rows "$d" "$run_dir"
    for f in "$d"/scripts/*_test.sh; do
      [[ -f "$f" ]] || { fail_step 'pfm: gate-script self-tests NOT RUN — no fixture suites found'; return 1; }
      stem="${f##*/}"; stem="${stem%_test.sh}"
      steps_add "pfm.self.$stem" bash "$f"
    done
  fi
  if [[ "$target" == templates || "$target" == all ]]; then
    for f in "$REPO_ROOT"/infra/fence/lanes/tests/*_test.sh; do
      [[ -f "$f" ]] || { fail_step 'templates: lane self-tests NOT RUN — no fixture suites found'; return 1; }
      stem="${f##*/}"; stem="${stem%_test.sh}"
      steps_add "templates.lanes.$stem" bash "$f"
    done
    for f in "$REPO_ROOT"/infra/demo/tests/*_test.sh; do
      [[ -f "$f" ]] || { fail_step 'templates: demo self-tests NOT RUN — no fixture suites found'; return 1; }
      stem="${f##*/}"; stem="${stem%_test.sh}"
      steps_add "templates.demo.$stem" bash "$f"
    done
  fi
  steps_barrier
  if [[ "$target" == pfm || "$target" == all ]]; then
    steps_add_heavy pfm.lint-new run 'pfm: lint-new (golangci-lint, changed lines)' -- make -C "$d" --no-print-directory lint-new
    steps_add_heavy pfm.vet run 'pfm: go vet' -- go -C "$d" vet ./...
    steps_add_heavy pfm.vet-darwin run 'pfm: go vet (darwin/arm64)' -- env GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go -C "$d" vet ./...
    steps_add_heavy pfm.fmt-check run 'pfm: fmt-check (gofumpt + gci + golines)' -- make -C "$d" --no-print-directory fmt-check
    steps_add pfm.arch run 'pfm: architecture ratchet' -- bash "$d/scripts/arch-check.sh"
  fi
  if [[ "$target" == templates || "$target" == all ]]; then
    steps_add_heavy templates.check-map checks_gate_check_map "$run_dir"
    steps_add templates.clone checks_templates_clone
    steps_add templates.leak checks_templates_leak
    steps_add templates.placeholders checks_templates_placeholders
    steps_add templates.scratch-paths checks_templates_scratch_paths
    steps_add templates.descriptions checks_templates_descriptions
    steps_add templates.mirrors checks_templates_mirrors
    steps_add templates.token-pricing checks_templates_token_pricing
    steps_add templates.token-audit checks_templates_token_audit
    steps_add templates.release-check checks_templates_release_check
    steps_add templates.codex-sync checks_templates_codex_sync
    steps_add templates.refresh-scope checks_templates_refresh_scope
    steps_add templates.pfm-guard checks_templates_pfm_guard
    steps_add templates.dev-report checks_templates_dev_report
    steps_add templates.opencode-writer-tests checks_templates_opencode_writer_tests
    steps_add templates.codeprobe checks_templates_codeprobe
    steps_add templates.opencode-writer-refs checks_templates_opencode_writer_refs
  fi
  if steps_run "$run_dir"; then step_rc=0; else step_rc=$?; fi
  row="$(awk -F '\t' '$1=="WALL" {print $3}' "$run_dir/gate.tsv")"
  if gate_budget_verdict "$target" "$row"; then budget_rc=0; else budget_rc=$?; fi
  (( step_rc == 0 && budget_rc == 0 ))
}
