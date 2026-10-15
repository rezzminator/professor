#!/usr/bin/env bash
# Check catalogue sourced by dev.sh. The serial blocks retain their original rows.

# engine_sync_build: the fence's one way to get the engine-sync binary (infra/fence/engine-sync.sh).
# shellcheck source=engine-sync.sh
. "${BASH_SOURCE[0]%/*}/engine-sync.sh"

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

checks_templates_plugin_logs() {
  run "templates: plugin-logs self-test" -- bash "$REPO_ROOT/scripts/test-plugin-logs.sh"
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
  # refuses to call a list of non-files clean. A submodule (a gitlink, mode
  # 160000) is another repository, scanned by that repository's own gate: it
  # is left out by name and COUNTED, never read as a deleted path. BROKEN
  # STATE: a git listing that could not be read is a red row, never a scan of
  # nothing.
  local tracked stage gitlinks porcelain changed candidate leak_list
  local present=0 changed_n=0 gone=0 submodules=0 leak_out leak_rc
  if ! tracked="$(repo_git ls-files)" || ! stage="$(repo_git ls-files --stage)" ||
    ! porcelain="$(repo_git status --porcelain --untracked-files=all)"; then
    fail_step "leak-check: the tracked or changed file list could not be read from git — nothing was scanned"
  else
    gitlinks=$(awk -F '\t' '$1 ~ /^160000 / { print $2 }' <<<"$stage")
    submodules=$(grep -c . <<<"$gitlinks" || true)
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
    done < <(printf '%s\n%s\n' "$tracked" "$changed" | sort -u | grep -vxF -f <(printf '%s\n' "$gitlinks"))
    while IFS= read -r candidate; do
      [[ -n "$candidate" && -f "$candidate" ]] && changed_n=$((changed_n + 1))
    done <<<"$changed"
    if (( present == 0 )); then
      fail_step "leak-check: the candidate list holds no file present on disk — the LIST is broken, nothing was scanned"
    else
      # xargs batches the list so no argument list overflows; any red batch
      # makes xargs non-zero, and a non-empty list never yields an empty batch.
      # The output is kept: a scan under the explicit `!structural-only` policy
      # applied no private denylist, prints PRIVATE COVERAGE SKIPPED, and its
      # row must say so instead of reading as the full scan.
      leak_out="$TMP_BASE/templates/leak-out.txt"
      leak_rc=0
      xargs -0 scripts/leak-check.sh --files < "$leak_list" > "$leak_out" 2>&1 || leak_rc=$?
      cat "$leak_out"
      if (( leak_rc != 0 )); then
        fail_step "leak-check FAILED — brand / PII / machine-path string in a public file, or a batch could not be scanned"
      elif grep -q 'PRIVATE COVERAGE SKIPPED' "$leak_out"; then
        ok "leak-check clean (STRUCTURAL-ONLY: the private denylist was NOT applied — ${present} file(s): the whole tracked tree plus ${changed_n} changed, ${gone} deleted path(s) skipped, ${submodules} submodule(s) left to their own repository's gate)"
      else
        ok "leak-check clean (${present} file(s): the whole tracked tree plus ${changed_n} changed, ${gone} deleted path(s) skipped, ${submodules} submodule(s) left to their own repository's gate)"
      fi
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
    local exclude='^(releases/|CHANGELOG\.md|\.codex/|\.opencode/|AGENTS\.md|docs/retro/|docs/dev/testing/timing\.md$|pfm/\.testtiming\.yml$)'
    # grep needs /dev/null as a second operand: BSD xargs runs the utility
    # even on empty input, and a bare `grep PATTERN` then reads stdin and
    # hangs the gate forever instead of reporting an empty sweep.
    all_hits=$(xargs -0 grep -lE '(^|[^/[:alnum:]_.-])tmp/(timing|flights|lanes|professor_)' /dev/null \
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

checks_engine_sync_policy() { # <config-home>: stage the shipped machine policy for a compiler's jail
  local policy="$REPO_ROOT/templates/global/engine-sync/config.json" destination="$1/engine-sync"
  if ! mkdir -p "$destination" || ! cp "$policy" "$destination/config.json"; then
    echo "engine-sync: machine policy could not be staged from $policy into $destination/config.json" >&2
    return 1
  fi
}

checks_mirror_tree() { # the scratch tree every mirror gate reads: the tree under test plus the mirrors built from it
  echo "$TMP_BASE/engine-sync/mirror-tree"
}

checks_mirror_tree_ready() { # the generation step built the mirrors there and said so
  [[ -f "$(checks_mirror_tree).built" ]]
}

checks_repo_git() { # git over the tree under test: the fence hands the mounted tree's git dir in the environment
  if [[ -n "${PFM_DEV_REPO_GIT_DIR:-}" && -n "${PFM_DEV_REPO_WORK_TREE:-}" ]]; then
    git -C "$REPO_ROOT" --git-dir="$PFM_DEV_REPO_GIT_DIR" --work-tree="$PFM_DEV_REPO_WORK_TREE" \
      -c safe.directory="$PFM_DEV_REPO_WORK_TREE" "$@"
  else
    git -C "$REPO_ROOT" "$@"
  fi
}

checks_mirror_tree_stage() { # <dest>: the tree under test as a scratch git repo — every tracked and unignored file, no generated mirror
  local dest="$1" list rc=0
  rm -rf "$dest" && mkdir -p "$dest" || { echo "mirror tree: cannot create $dest" >&2; return 1; }
  list="$(mktemp "$dest.list.XXXXXX")" || { echo "mirror tree: cannot create a file list beside $dest" >&2; return 1; }
  if ! checks_repo_git ls-files -z --cached --others --exclude-standard >"$list"; then
    echo "mirror tree: git could not list the tree under test at $REPO_ROOT" >&2
    rm -f "$list"
    return 1
  fi
  # A submodule is a gitlink the list names as one path: its checkout belongs to
  # its own repository's gate, never the mirrors'. A path tracked but deleted in
  # the tree under test has nothing to copy.
  checks_repo_git ls-files -s -z | tr '\0' '\n' | awk -F'\t' '$1 ~ /^160000 / {print $2}' >"$list.submodules" || rc=$?
  if [[ "$rc" -eq 0 ]]; then
    # grep exits 1 when the list is empty after the filter: a tree of submodules only.
    if grep -zvxFf "$list.submodules" "$list" >"$list.files"; then :; elif [[ $? -ne 1 ]]; then rc=2; fi
  fi
  if [[ "$rc" -eq 0 ]]; then
    # Only the deleted paths drop out here; any other create- or extract-side failure fails the copy.
    while IFS= read -r -d '' path; do
      if [[ -e "$REPO_ROOT/$path" || -L "$REPO_ROOT/$path" ]]; then printf '%s\0' "$path"; fi
    done <"$list.files" >"$list.present" || rc=$?
  fi
  if [[ "$rc" -eq 0 ]]; then
    (set -o pipefail; tar -C "$REPO_ROOT" --null -T "$list.present" -cf - | tar -C "$dest" -xf -) || rc=$?
  fi
  rm -f "$list" "$list.submodules" "$list.files" "$list.present"
  if [[ "$rc" -ne 0 ]]; then
    echo "mirror tree: copying the tree under test into $dest failed (exit $rc)" >&2
    return 1
  fi
  if ! git -C "$dest" init -q || ! git -C "$dest" add -A; then
    echo "mirror tree: git could not index the copy at $dest" >&2
    return 1
  fi
}

checks_templates_mirrors_generate() {
  local es_bin="$TMP_BASE/engine-sync/engine-sync" config_home="$TMP_BASE/engine-sync/project-config"
  local tree home="$TMP_BASE/engine-sync/mirror-home"
  tree="$(checks_mirror_tree)"
  head_ "templates — generate the engine mirrors"
  # The mirrors (AGENTS.md, .codex/**, .opencode/**) are untracked, so the tree
  # under test holds whatever the last host build left in them, built from the
  # host's own engine-sync and policy: a fresh worktree holds none, and one
  # built before a rename holds the old roster. The gate never reads them.
  # It stages a scratch copy of the tree under test and builds the mirrors
  # there with engine-sync built from its mounted source (never a host
  # binary) under the tree's own machine policy, in an empty home; every
  # mirror gate below reads that copy.
  rm -f "$tree.built"
  if ! need_tool go templates || ! need_tool node templates || ! need_tool git templates || ! need_tool tar templates; then
    fail_step "mirror generation could not run — no mirror gate below is a verdict on the tree"
  elif ! engine_sync_build "$es_bin"; then
    fail_step "mirror generation could not run — the engine-sync binary could not be built (see above); no mirror gate below is a verdict on the tree"
  elif ! checks_engine_sync_policy "$config_home"; then
    fail_step "mirror generation could not run — the machine policy could not be staged (see above); no mirror gate below is a verdict on the tree"
  elif ! mkdir -p "$home" || ! checks_mirror_tree_stage "$tree"; then
    fail_step "mirror generation could not run — the tree under test could not be staged (see above); no mirror gate below is a verdict on the tree"
  else
    local report="$TMP_BASE/engine-sync/mirror-build.log" rc=0
    HOME="$home" XDG_CONFIG_HOME="$config_home" "$es_bin" build "$tree" >"$report" 2>&1 || rc=$?
    if [ "$rc" -eq 0 ] && : >"$tree.built"; then
      ok "engine mirrors generated from the tree under test ($(tail -n 1 "$report"))"
    else
      cat "$report"
      fail_step "mirror generation FAILED (engine-sync build exit $rc) — the tree under test does not compile to mirrors (see above); no mirror gate below is a verdict on the tree"
    fi
  fi

}

checks_templates_global_variants() {
  local es_bin="$TMP_BASE/engine-sync/engine-sync" sources="$REPO_ROOT/templates/global/agents"
  local jail home src names declared name rendered=0 missing="" out rc=0 named="" stale unknown="" unmapped=""
  local tracked="$REPO_ROOT/templates/generated/agents"
  head_ "templates — global agent variants render"
  # Machine agent .md files with from: declare agents re-emitted from a
  # source agent with swapped body text; shared files below a subdirectory are layers, not agents. A swap whose text drifted from its
  # source fails only when engine-sync renders it, which an adopter's install
  # does: this step renders every declared variant with the real binary, built
  # from the mounted source, into a jail home and Codex home (the real $HOME is
  # never read or written). Its own broken state is a red line: a declaration
  # that cannot be read, one that declares nothing, a render error, or a
  # rendered count that differs from the declared count (a lookup that found
  # nothing would otherwise read as clean). The jail declares the gpt-agents
  # capability, so the opt-in variants render too; the render without it is
  # scripts/check-agent-roster.test.mjs's. The policy renders declarations into
  # the clone's templates/generated/agents, which is tracked: the render runs
  # on a copy of templates/, and any difference from the tracked directory (a
  # stale, missing or orphan file) is a red line naming the files. The same run
  # also renders every OpenCode twin: an `unknown MCP server` warning means an
  # agent's tools line names a server the twin cannot grant, so the twin loses
  # the tool, and the warning is a red line naming each server; an `unmapped
  # OpenCode model alias` warning means a twin lost its model pin, and is a red
  # line naming each alias.
  if ! need_tool go templates || ! need_tool python3 templates; then
    fail_step "global agent variants NOT RENDERED — the toolchain is missing"
    return
  elif ! engine_sync_build "$es_bin"; then
    fail_step "global agent variants NOT RENDERED — the engine-sync binary could not be built (see above)"
    return
  elif [[ ! -d "$sources" ]]; then
    fail_step "global agent variants NOT RENDERED — $sources is missing, so nothing was looked up"
    return
  elif ! names="$(python3 -I "$REPO_ROOT/scripts/agent-source-inventory.py" "$sources" --declarations 2>&1)"; then
    # The inventory's exit 3 is a missing PyYAML, a toolchain gap, never a parse failure.
    if [[ "$names" == "GAP install"* ]]; then
      fail_step "global agent variants NOT RENDERED — $names"
    else
      fail_step "global agent variants NOT RENDERED — agent sources do not parse: $names"
    fi
    return
  fi
  declared=$(grep -c . <<<"$names" || true)
  if (( declared == 0 )); then
    fail_step "global agent variants NOT RENDERED — agent sources declare no variant, so nothing could render"
    return
  fi
  mkdir -p "$TMP_BASE/engine-sync"
  if ! jail="$(mktemp -d "$TMP_BASE/engine-sync/global-variants.XXXXXX")"; then
    fail_step "global agent variants NOT RENDERED — the jail home could not be created under $TMP_BASE/engine-sync"
    return
  fi
  home="$jail/home"
  src="$jail/src"
  mkdir -p "$home/.claude" "$home/.codex" "$src"
  # The copy drops the tracked render so an orphan or a stale file shows.
  if ! cp -a "$REPO_ROOT/templates" "$src/templates" || ! rm -rf "$src/templates/generated"; then
    fail_step "global agent variants NOT RENDERED — templates/ could not be copied into the jail"
    rm -rf "$jail"
    return
  fi
  out="$jail/engine-sync-global.out"
  if ! checks_engine_sync_policy "$home/.config"; then
    fail_step "global agent variants NOT RENDERED — the machine policy could not be staged in the jail"
    rm -rf "$jail"
    return
  fi
  env -u XDG_CONFIG_HOME -u XDG_DATA_HOME -u XDG_STATE_HOME -u ENGINE_SYNC_HOME -u ENGINE_SYNC_STATE -u CODEX_HOME \
    HOME="$home" "$es_bin" global --home "$home" --source-repo "$src" \
    --claude-config-dir "$home/.claude" --codex-home "$home/.codex" --capability gpt-agents >"$out" 2>&1 || rc=$?
  if (( rc == 0 )) && grep -Eq 'SECTION-DRIFT|SECTION-MISSING|RENAME-UNUSED|UNSET-MISSING' "$out"; then
    rc=1 # a section override whose original moved on is a named drift, never a clean render
  fi
  if (( rc == 0 )); then
    unknown="$(grep -E 'unknown MCP server' "$out" | sort | uniq -c || true)"
    unmapped="$(grep -E 'unmapped OpenCode model alias' "$out" | sort | uniq -c || true)"
  fi
  if (( rc != 0 )); then
    cat "$out"
    while IFS= read -r name; do
      if grep -qE "variant \"$name\"|(SECTION-DRIFT|SECTION-MISSING|RENAME-UNUSED|UNSET-MISSING) $name:" "$out"; then named="${named:+$named, }$name"; fi
    done <<<"$names"
    fail_step "global agent variants FAILED to render (engine-sync global exit $rc; variant named in the error: ${named:-none — read the output above})"
  else
    while IFS= read -r name; do
      # A variant whose rendered engines block turns Codex off (`codex: false`) has no Codex role by design.
      if [[ -L "$home/.claude/agents/$name.md" && -s "$home/.claude/agents/$name.md" ]] &&
        { [[ -f "$home/.codex/agents/$name.toml" ]] || grep -Eq '^  codex: false$' "$home/.claude/agents/$name.md"; }; then
        rendered=$((rendered + 1))
      else
        missing="${missing:+$missing, }$name"
      fi
    done <<<"$names"
    # A policy without a source-repository generated directory renders into
    # the home store: neither side exists and there is nothing to compare.
    stale=""
    if [[ -e "$src/templates/generated/agents" || -e "$tracked" ]]; then
      mkdir -p "$src/templates/generated/agents" # a render of nothing is an empty directory: each tracked file is named
      stale="$(diff -rq "$src/templates/generated/agents" "$tracked" 2>&1 || true)"
    fi
    if (( rendered != declared )); then
      cat "$out"
      fail_step "global agent variants: $rendered of $declared declared variant(s) rendered — missing: $missing"
    elif [[ -n "$unknown" ]]; then
      printf '%s\n' "$unknown"
      fail_step "global agent variants: engine-sync dropped MCP tools from OpenCode twins ($(grep -c 'unknown MCP server' <<<"$unknown") distinct 'unknown MCP server' warning(s), above) — list the server in templates/global/engine-sync/config.json mcp.globalServers, or fix the engine-sync machine path that ignores it"
    elif [[ -n "$unmapped" ]]; then
      printf '%s\n' "$unmapped"
      fail_step "global agent variants: engine-sync omitted the model of an OpenCode twin ($(grep -c 'unmapped OpenCode model alias' <<<"$unmapped") distinct 'unmapped OpenCode model alias' warning(s), above) — add the alias to templates/global/engine-sync/config.json opencode.modelMap, or give the agent an engines.opencode.model alias it maps"
    elif [[ -n "$stale" ]]; then
      printf '%s\n' "$stale"
      fail_step "global agent variants: tracked templates/generated/agents differs from a fresh render — run pfm install and commit templates/generated/agents"
    else
      ok "all $declared global agent variant(s) render through engine-sync into a jail home ($(paste -sd, - <<<"$names" | sed 's/,/, /g'))"
    fi
  fi
  rm -rf "$jail"

}

checks_templates_mirrors_marker() {
  head_ "templates — codex generated-marker claim"
  # engine-sync never touches a file it does not recognize as generated, so a
  # marked file whose source is gone is a fossil no build can heal: the gate
  # reconciles every marked file's declared source, and every tracked symlink's
  # target, against disk, and fails a fossil BY NAME.
  # The row carries the script's own count line: `0 marked file(s)` reads as the
  # vacuous run it is, instead of the claim that every marked file was examined.
  # It reads the scratch tree the generation step built, never the tree under
  # test's own mirrors.
  local marker_out marker_rc=0
  if ! checks_mirror_tree_ready; then
    fail_step "codex marker claim NOT CHECKED — the mirrors were not generated from the tree under test (see the generation step above)"
  else
    marker_out="$(cd "$(checks_mirror_tree)" && env -u PFM_DEV_REPO_GIT_DIR -u PFM_DEV_REPO_WORK_TREE node scripts/check-codex-markers.mjs 2>&1)" || marker_rc=$?
    printf '%s\n' "$marker_out"
    if [ "$marker_rc" -eq 0 ]; then
      ok "every marked file has a live source; every tracked symlink a tracked target — $(tail -n 1 <<<"$marker_out")"
    else
      fail_step "codex marker claim FAILED — a stranded marker or an orphaned generated file (see output)"
    fi
  fi
  node_test_suite "generated marker claims" "$TMP_BASE/templates/codex-markers.tap" scripts/check-codex-markers.test.mjs

}

checks_templates_mirrors_roster() {
  local es_bin="$TMP_BASE/engine-sync/engine-sync" config_home="$TMP_BASE/engine-sync/project-config" rc
  head_ "templates — generated agent rosters"
  if ! checks_mirror_tree_ready; then
    fail_step "agent roster NOT GENERATED — the mirrors were not generated from the tree under test, so no roster was compared (see the generation step above)"
  elif ! checks_engine_sync_policy "$config_home"; then
    fail_step "agent roster ERROR — the machine policy could not be staged (see above)"
  elif (cd "$(checks_mirror_tree)" && ENGINE_SYNC_BIN="$es_bin" XDG_CONFIG_HOME="$config_home" HOME="$TMP_BASE/engine-sync/mirror-home" node scripts/check-agent-roster.mjs); then
    ok "Codex and OpenCode agent rosters match their Claude sources"
  else
    rc=$?
    if [ "$rc" -eq 2 ]; then
      fail_step "agent roster ERROR — effective configuration could not be read or the check crashed (see the error above)"
    elif [ "$rc" -eq 3 ]; then
      fail_step "agent roster NOT GENERATED — the engine mirrors are absent, so no roster was compared (see above)"
    elif [ "$rc" -eq 4 ]; then
      fail_step "agent roster GAP install — the inventory toolchain is missing, so sources were not scanned (see above)"
    elif [ "$rc" -eq 5 ]; then
      fail_step "agent roster SOURCE REJECTED — the inventory found an invalid agent source (see above)"
    else
      fail_step "agent roster FAILED — a source role is missing or cannot perform its protocol"
    fi
  fi
  ENGINE_SYNC_BIN="$es_bin" node_test_suite "generated agent rosters" "$TMP_BASE/templates/agent-roster.tap" scripts/check-agent-roster.test.mjs
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

checks_templates_refresh_scope() {
  head_ "templates — refresh-scope and verify leak policy fixtures"
  run "templates: refresh-scope fixtures" -- python3 "$REPO_ROOT/scripts/test-refresh-scope.py"
  run "templates: verify leak policy fixtures" -- python3 "$REPO_ROOT/scripts/test-verify-leak-policy.py"

}

checks_templates_worktree_script() {
  head_ "templates — shipped worktree.sh lifecycle safety"
  if bash "$REPO_ROOT/scripts/test-worktree.sh"; then
    ok "worktree.sh refuses dirty removal, keeps registered paths with spaces, and names failed provisioning"
  else
    fail_step "worktree.sh lifecycle regression FAILED — dirty removal, prune registration, or provisioning failure handling broke (see output)"
  fi
}

checks_templates_codex_sync() {
  head_ "templates — codex-sync door"
  local cs_copy
  for cs_copy in templates/project/scripts/codex-sync.sh .claude/scripts/codex-sync.sh; do
    if bash "$REPO_ROOT/scripts/test-codex-sync.sh" "$REPO_ROOT/$cs_copy"; then
      ok "codex-sync ($cs_copy) forwards each stage, refuses a bad ENGINE_SYNC_BIN by name, and warns of an unavailable compiler only with a pending edit"
    else
      fail_step "codex-sync regression FAILED ($cs_copy) — see output"
    fi
  done

}

checks_templates_dev_report() {
  head_ "templates — go test report under pipefail"
  if bash "$REPO_ROOT/scripts/test-dev-report.sh" "$REPO_ROOT/.claude/scripts/dev.sh"; then
    ok "go_test_report reaches its log line on filtered and over-cap failure output"
  else
    fail_step "go_test_report regression FAILED — a failing stream aborted the report before its verdict (see output)"
  fi

}

checks_templates_format_md() {
  head_ "templates — format-md hook hands rumdl's unfixed issues to the agent"
  if bash "$REPO_ROOT/scripts/test-format-md-readiness.sh" && bash "$REPO_ROOT/scripts/test-format-md.sh" "$REPO_ROOT/templates/project/scripts/format-md.sh" && bash "$REPO_ROOT/scripts/test-format-md.sh" "$REPO_ROOT/.claude/scripts/format-md.sh"; then
    ok "format-md exits 2 with one UNFIXED line per issue rumdl cannot fix, 0 on a clean file"
  else
    fail_step "format-md regression FAILED — an unfixed rumdl issue must reach the agent as exit 2 with UNFIXED lines (see output)"
  fi

}

checks_templates_flight_templates() {
  head_ "templates — flight prompt contracts"
  run "templates: flight prompt contracts" -- bash "$REPO_ROOT/scripts/test-flight-templates.sh"
}

checks_templates_unit_script() { # <name>: scripts/test-<name>.sh, the self-test of .claude/scripts/<name>.sh, on its default script
  head_ "templates — test-$1.sh self-test"
  run "templates: test-$1.sh self-test" -- bash "$REPO_ROOT/scripts/test-$1.sh"
}

checks_templates_mirrors_opencode() {
  local es_bin="$TMP_BASE/engine-sync/engine-sync" opencode_home="$TMP_BASE/engine-sync/opencode-verify-home"
  head_ "templates — native opencode mirror"
  # engine-sync built from its mounted source checks and doctors the mirror
  # under a scratch home; verification never depends on or installs a host binary.
  if ! need_tool go templates || ! engine_sync_build "$es_bin"; then
    fail_step "opencode mirror NOT CHECKED — the engine-sync binary could not be built (see above)"
  elif ! checks_engine_sync_policy "$opencode_home/.config"; then
    fail_step "opencode mirror NOT CHECKED — the machine policy could not be staged (see above)"
  elif ! checks_mirror_tree_ready; then
    fail_step "opencode mirror NOT CHECKED — the mirrors were not generated from the tree under test (see the generation step above)"
  elif XDG_CONFIG_HOME="$opencode_home/.config" "$es_bin" opencode check "$(checks_mirror_tree)" --home "$opencode_home" \
    && XDG_CONFIG_HOME="$opencode_home/.config" "$es_bin" opencode doctor "$(checks_mirror_tree)" --home "$opencode_home"; then
    ok "opencode mirror parseable and current against the tree under test"
  else
    fail_step "opencode mirror FAILED — the mirror generated from the tree under test does not check (see above)"
  fi

}

checks_templates_opencode_writer_tests() {
  head_ "templates — OpenCode writer check tests"
  node_test_suite "opencode-writer check" "$TMP_BASE/templates/opencode-writer.tap" scripts/check-opencode-writer.test.mjs

}

checks_templates_engine_sync_action_tests() {
  head_ "templates — CI engine-sync action tests"
  node_test_suite "engine-sync action" "$TMP_BASE/templates/engine-sync-action.tap" scripts/engine-sync-action.test.mjs

}

checks_templates_skill_tests() {
  local skill cp_out
  for skill in codeprobe tracetree; do
    head_ "templates — $skill skill tests"
    cp_out="$TMP_BASE/templates/$skill.txt"
    if [[ ! -f templates/global/skills/$skill/${skill}_test.py ]]; then
      fail_step "$skill tests NOT RUN — templates/global/skills/$skill/${skill}_test.py is missing"
    elif ! python3 -m unittest "templates/global/skills/$skill/"*_test.py >"$cp_out" 2>&1; then
      cat "$cp_out"
      if grep -Eq '^Ran 0 tests?' "$cp_out"; then
        fail_step "$skill tests NOT RUN — unittest ran zero tests; a green exit with no test is not a pass"
      else
        fail_step "$skill tests FAILED — a verb or filter regressed, or python3 could not run the suite (see output)"
      fi
    elif ! grep -Eq '^Ran [1-9][0-9]* tests?' "$cp_out"; then
      cat "$cp_out"
      fail_step "$skill tests NOT RUN — unittest ran zero tests; a green exit with no test is not a pass"
    elif grep -Eq 'skipped=[1-9]' "$cp_out"; then
      cat "$cp_out"
      fail_step "$skill tests SKIPPED — a skipped test is a named gap, never a pass"
    else
      ok "$skill skill tests hold ($(grep -Eo '^Ran [0-9]+ tests?' "$cp_out"))"
    fi
  done

}

checks_templates_opencode_writer_refs() {
  head_ "templates — OpenCode writer references"
  if node "$REPO_ROOT/scripts/check-opencode-writer.mjs"; then
    ok "live surfaces name the native writer, none the deleted one"
  else
    fail_step "OpenCode writer reference FAILED — every named surface names engine-sync, the native writer, and none the deleted one (see output)"
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

# One catalogue for the serial runner and the parallel gate. Scheduling-only
# lane/demo aggregates and the gate's heavy check-map stay at their boundaries.
checks_named_template_steps() { # <callback> <name>...: register the matching checks_templates_<name> command
  local add="$1" name
  shift
  for name in "$@"; do
    "$add" "templates.$name" "checks_templates_${name//-/_}"
  done
}

checks_templates_steps() { # <callback>: callback <step name> <command> [args...]
  local add="$1" name
  checks_named_template_steps "$add" clone leak plugin-logs placeholders scratch-paths descriptions mirrors \
    global-variants release-check codex-sync refresh-scope worktree-script dev-report \
    format-md flight-templates
  for name in check-pfm check-templates test-pfm test-templates unit-path; do
    "$add" "templates.$name-tests" checks_templates_unit_script "$name"
  done
  checks_named_template_steps "$add" opencode-writer-tests engine-sync-action-tests skill-tests opencode-writer-refs
}

checks_templates_step_serial() { shift; "$@"; }

checks_templates() {
  checks_templates_check_map_serial
  checks_templates_lanes_serial
  checks_templates_demo_serial
  checks_templates_steps checks_templates_step_serial
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
  # The flow matrix: every test and file TESTPLAN.md cites must exist.
  run "pfm: flow-matrix citations" -- bash "$d/scripts/testplan-check.sh"
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

# checks_pfm_profile_pointers <go-test.json> <profile dir> <FAILURES before the run>: under
# go_test_report's block, each failing package's profile bundles (profile-report.sh failures). A
# failed run whose stream names no failing package (NO TEST EVENTS, an unreadable stream) or a
# report that could not be built still points at the profile root, never at nothing.
checks_pfm_profile_pointers() {
  local json="$1" profile="$2" before="$3" out rc
  if out="$(bash "$REPO_ROOT/infra/fence/profile-report.sh" failures "$json" "$profile" 2>&1)"; then rc=0; else rc=$?; fi
  [[ -z "$out" ]] || printf '%s\n' "$out"
  (( rc == 0 )) || printf '  PROFILE failures NOT REPORTED — profile-report.sh failures exit %s\n' "$rc"
  if (( rc != 0 )) || { [[ "$out" != *'PROFILE '* ]] && (( FAILURES > before )); }; then
    printf '  PROFILE index %s/\n' "$profile"
  fi
}

# checks_gate_fixture <kind> <run dir>: one deliberately broken step of the instrument's own proof
# (gate_run registers the five only under PFM_GATE_FIXTURES=1): go-fail, go-hang and go-slow run
# internal/testjail/testdata/profilefixture, each profiling into its own root <run>/profile/fixture-<kind>
# so the failure pointers name only that fixture's bundles, never a sibling's;
# shell-fail and shell-hang are the scripts under infra/fence/gate-fixtures/. Every kind is red by
# design, and a go-slow step is ended by its bound, never by this function.
checks_gate_fixture() {
  local kind="$1" run_dir="$2" mode timeout json before="$FAILURES"
  case "$kind" in
    shell-fail|shell-hang)
      info "\$ bash infra/fence/gate-fixtures/$kind.sh"
      bash "$REPO_ROOT/infra/fence/gate-fixtures/$kind.sh" || fail_step "fixture: $kind red (expected)"
      return ;;
    go-fail) mode=fail; timeout=2m ;;
    go-hang) mode=hang; timeout=20s ;;
    go-slow) mode=slow; timeout=5m; local -x PFM_PROFILE_FIXTURE_SLOW_S=120 ;;
    *) fail_step "fixture: unknown kind '$kind'"; return ;;
  esac
  json="$run_dir/fixture-$kind.json"
  local root="$run_dir/profile/fixture-$kind"
  local -x PFM_PROFILE_FIXTURE="$mode" PFM_TEST_ARTIFACT_DIR="$root"
  info "\$ go -C pfm test -count=1 -json -timeout $timeout ./internal/testjail/testdata/profilefixture/ (PFM_PROFILE_FIXTURE=$mode)"
  go -C "$REPO_ROOT/pfm" test -count=1 -json -timeout "$timeout" ./internal/testjail/testdata/profilefixture/ > "$json" \
    || fail_step "fixture: go $mode red (expected)"
  go_test_report "$json"
  checks_pfm_profile_pointers "$json" "$root" "$before"
}

checks_pfm_unit() { # pfm dir, run dir, optional already-read TESTFLAGS
  local d="$1" run_dir="$2" flags_text="${3:-}" timing_base history before="$FAILURES"
  local testflags=() shard_args=()
  # Every test process of the run leaves profile/<label>.<pid>/ (internal/testjail).
  local -x PFM_TEST_ARTIFACT_DIR="$run_dir/profile"
  if [[ -z "$flags_text" ]] && ! flags_text="$(make -s -C "$d" --no-print-directory testflags)"; then
    fail_step "pfm: TESTFLAGS could not be read from Makefile"; return
  fi
  read -r -a testflags <<< "$flags_text"
  timing_base="${PFM_TEST_TIMING_DIR:-$TMP_BASE/timing}"
  history="$(checks_pfm_history "$timing_base" "$run_dir")"
  [[ -z "$history" ]] || shard_args=(--history "$history")
  # Positional arguments keep flags and output paths out of shell code.
  # The JSON is retained even on failure; timing is a separate verdict.
  # --bin-dir keeps the prebuilt pfm in the run dir for the templates.check-map step.
  run "pfm: go test" -- bash "$d/scripts/test-shard.sh" run --out "$run_dir/unit.json" --bin-dir "$run_dir/bin" "${shard_args[@]}" -- "${testflags[@]}"
  go_test_report "$run_dir/unit.json"
  checks_pfm_profile_pointers "$run_dir/unit.json" "$run_dir/profile" "$before"
  skip_gate "pfm: skipped tests are all listed (unit)" "$run_dir/unit.json"
  run "pfm: test timing (budget)" -- bash "$d/scripts/test-timing.sh" \
    --check --suite unit --out "$run_dir/unit.tsv" "$run_dir/unit.json"
}

# checks_pfm_e2e <pfm dir> <run dir>: the tagged e2e rows (dev.sh pfm_e2e_rows), profiled into
# <run dir>/profile, with the CPU load sampled into e2e.load for their whole span — stopped only
# after the rows return, so the timing check inside them finds a load row at the stream's end.
checks_pfm_e2e() {
  local d="$1" run_dir="$2" before="$FAILURES" load_pid load_rc
  local -x PFM_TEST_ARTIFACT_DIR="$run_dir/profile"
  bash "$d/scripts/test-shard.sh" sample --out "$run_dir/e2e.load" &
  load_pid=$!
  pfm_e2e_rows "$d" "$run_dir"
  kill -TERM "$load_pid" 2>/dev/null || true # already gone: its exit status below says why
  if wait "$load_pid"; then load_rc=0; else load_rc=$?; fi
  (( load_rc == 0 )) || printf '  PROFILE e2e.load NOT RECORDED — test-shard.sh sample exit %s (its stderr above)\n' "$load_rc"
  checks_pfm_profile_pointers "$run_dir/e2e.json" "$run_dir/profile" "$before"
}

# The tests the gate's race step runs under -race: the harvester chaos suites and the race regressions
# they found, as <package dir under pfm/> <Test> lines. A test that guards a race joins here; the step
# demands a pass event of each, so a rename here or there is a red step, never "no tests to run".
CHECKS_PFM_RACE_TESTS='internal/harvest TestHarvesterChaos
internal/harvest TestFetchFailureIsCachedBeforeItsFlightEnds
internal/harvest TestFetchSizeOnlyLeaderSharesTheWholePage
internal/harvest TestFetchRefreshThatReadsClearsTheCachedFailure
internal/harvest TestCallerHeadersShareTheirFailureAcrossCalls
internal/harvest TestLegacyConcurrentFailuresShareOneInFlightFetch
internal/harvestmcp TestServiceChaos
internal/pricing/modelcost TestRefreshDuringAnUpdateHoldsOwnershipThroughPersistence'

# checks_pfm_race <pfm dir> <run dir>: CHECKS_PFM_RACE_TESTS under the race detector, which needs cgo
# (the pfm-dev image carries gcc; CGO_ENABLED=1 for this command alone, every other build stays static),
# its stream kept as <run dir>/race.json and profiled into its own root <run dir>/profile/race — its
# packages are pfm.unit's too, and failure pointers match a process by package. BROKEN STATE: a named test
# with no pass event in the stream — skipped, renamed, unbuilt, or a stream that was never written — is
# a red line naming each such test; no C compiler is TOOLCHAIN-MISSING and no test runs.
checks_pfm_race() {
  local d="$1" run_dir="$2" json="$2/race.json" before="$FAILURES" regex pkgs=() pkg test passed missing="" n=0 total=0
  local -x PFM_TEST_ARTIFACT_DIR="$run_dir/profile/race"
  need_tool gcc pfm || return 1
  regex="^($(awk '{print $2}' <<< "$CHECKS_PFM_RACE_TESTS" | paste -sd'|' -))\$"
  while read -r pkg; do pkgs+=("./$pkg/"); done < <(awk '{print $1}' <<< "$CHECKS_PFM_RACE_TESTS" | awk '!seen[$0]++')
  run "pfm: go test -race (harvester chaos and race regressions)" -- bash -c '
    CGO_ENABLED=1 go -C "$1" test -race -count=1 -json -timeout 10m -run "$2" "${@:4}" > "$3"
  ' _ "$d" "$regex" "$json" "${pkgs[@]}"
  go_test_report "$json"
  checks_pfm_profile_pointers "$json" "$run_dir/profile/race" "$before"
  passed="$(jq -r 'select(.Action=="pass" and .Test != null) | .Package + " " + .Test' "$json" 2>/dev/null)" || passed=""
  while read -r pkg test; do
    total=$((total + 1))
    grep -Fxq "github.com/rezzminator/professor/pfm/$pkg $test" <<< "$passed" && continue
    n=$((n + 1)); missing+="${missing:+, }$pkg $test"
  done <<< "$CHECKS_PFM_RACE_TESTS"
  (( n == 0 )) || fail_step "pfm: race — $n of $total named test(s) never passed: $missing"
}

# The RUN DIR line: a test run's caller reads the run dir it made from the output's last line,
# `RUN DIR: <absolute host path>`, never by guessing the newest run.* (a gate from another tree
# writes under another base). Each timing_run_dir caller records its run dir with timing_run_note;
# dev.sh's EXIT trap prints it with timing_run_report, after every other line.
timing_run_note() { TIMING_RUN_LAST="$1"; }

# timing_run_host_path <run dir>: the run dir's host path. Inside the fence the base
# PFM_TEST_TIMING_DIR (/pfm-timing) is the bind of PFM_TEST_TIMING_HOST (dev.sh iso); with that
# unset, or a run dir outside the base, the path is already the host's and is printed as is.
timing_run_host_path() {
  local run="$1"
  if [[ -n "${PFM_TEST_TIMING_HOST:-}" && -n "${PFM_TEST_TIMING_DIR:-}" && "$run" == "$PFM_TEST_TIMING_DIR"/* ]]; then
    printf '%s/%s\n' "$PFM_TEST_TIMING_HOST" "${run#"$PFM_TEST_TIMING_DIR"/}"
  else
    printf '%s\n' "$run"
  fi
}

# timing_run_report: dev.sh's EXIT trap. A run that made a run dir prints `RUN DIR: <host path>`;
# under dev.sh iso (PFM_TEST_RUN_NOTE set) it writes the host path to that note instead, and the host
# dev.sh — whose own lines (the gate-history ingest, its footer) come after the container's — prints
# the note it handed in (TIMING_RUN_NOTE) and removes it. No run dir made, no line. BROKEN STATE: a
# note that cannot be written is a stderr line naming it, and the fence prints the line itself.
timing_run_report() {
  local path=""
  if [[ -n "${TIMING_RUN_LAST:-}" ]]; then
    path="$(timing_run_host_path "$TIMING_RUN_LAST")"
    if [[ -n "${PFM_TEST_RUN_NOTE:-}" ]]; then
      if printf '%s\n' "$path" 2>/dev/null > "$PFM_TEST_RUN_NOTE"; then return 0; fi
      echo "timing_run_report: the run-dir note $PFM_TEST_RUN_NOTE could not be written — the host cannot repeat the RUN DIR line" >&2
    fi
  elif [[ -n "${TIMING_RUN_NOTE:-}" ]]; then
    [[ ! -s "$TIMING_RUN_NOTE" ]] || path="$(head -n 1 "$TIMING_RUN_NOTE")"
    rm -f "$TIMING_RUN_NOTE"
  fi
  [[ -z "$path" ]] || printf 'RUN DIR: %s\n' "$path"
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
  timing_run_note "$timing_run"
  checks_pfm_unit "$d" "$timing_run" "$flags_text"
  checks_pfm_e2e "$d" "$timing_run"
}

gate_wall_ceil() { # gate_wall_ceil <seconds>: the wall rounded up to a whole second
  awk -v w="$1" 'BEGIN { printf "%d", (w==int(w) ? w : int(w)+1) }'
}

# gate_budget_params <target> <budget file>: reads the file's tolerance, fail_factor and the
# target's row into GATE_BUDGET_TOLERANCE, GATE_BUDGET_FAIL_FACTOR and GATE_BUDGET_VALUE (empty
# when the target has no row). A file that cannot be judged returns 1 with GATE_BUDGET_REASON set.
gate_budget_params() {
  local target="$1" budgets="$2"
  GATE_BUDGET_TOLERANCE="" GATE_BUDGET_FAIL_FACTOR="" GATE_BUDGET_VALUE="" GATE_BUDGET_REASON=""
  if [[ ! -r "$budgets" ]]; then
    GATE_BUDGET_REASON="budget file unreadable: $budgets"
    return 1
  fi
  GATE_BUDGET_TOLERANCE="$(awk '$1=="tolerance:" {print $2; exit}' "$budgets")"
  GATE_BUDGET_FAIL_FACTOR="$(awk '$1=="fail_factor:" {print $2; exit}' "$budgets")"
  GATE_BUDGET_VALUE="$(awk -v want="$target" '$1=="gate:" {inside=1; next} /^[a-z]/ {inside=0} inside && $1==want ":" {print $2; exit}' "$budgets")"
  if [[ -z "$GATE_BUDGET_TOLERANCE" ]]; then
    GATE_BUDGET_REASON="no tolerance in $budgets"
    return 1
  fi
  if [[ ! "$GATE_BUDGET_TOLERANCE" =~ ^[0-9]+(\.[0-9]+)?$ ]]; then
    GATE_BUDGET_REASON="invalid tolerance $GATE_BUDGET_TOLERANCE in $budgets"
    return 1
  fi
  if [[ -z "$GATE_BUDGET_FAIL_FACTOR" ]]; then
    GATE_BUDGET_REASON="no fail_factor in $budgets"
    return 1
  fi
  if [[ ! "$GATE_BUDGET_FAIL_FACTOR" =~ ^[0-9]+(\.[0-9]+)?$ ]] || ! awk -v f="$GATE_BUDGET_FAIL_FACTOR" 'BEGIN { exit !(f > 0) }'; then
    GATE_BUDGET_REASON="invalid fail_factor $GATE_BUDGET_FAIL_FACTOR in $budgets"
    return 1
  fi
}

# gate_budget_limits <budget> <tolerance> <fail_factor>: prints "<limit> <fail_limit>", the one
# arithmetic both the budget verdict and the step bound take.
gate_budget_limits() {
  local limit fail_limit
  limit="$(awk -v v="$1" -v t="$2" 'BEGIN { printf "%d", v * t }')"
  fail_limit="$(awk -v l="$limit" -v f="$3" 'BEGIN { printf "%d", l * f }')"
  printf '%s %s\n' "$limit" "$fail_limit"
}

# gate_budget_verdict <target> <wall seconds> [budget file] [attribution]: prints the verdict
# line, returns 1 when red, and leaves PASS, WARN, FAIL, UNPINNED or ERROR in BUDGET_VERDICT.
# attribution is test-contention.sh's `<WORD><TAB><evidence>` line for the run: an over-limit line
# ends with it, and a fail-tier wall the judge calls CONTENTION is a WARN, not a red gate.
gate_budget_verdict() {
  local target="$1" wall="$2" budgets="${3:-$REPO_ROOT/infra/fence/gate-budget.yml}" attribution="${4:-}"
  local value limit fail_limit name="gate($target)" word="" suffix=""
  if [[ -n "$attribution" ]]; then
    word="${attribution%%$'\t'*}"
    case "$word" in
      CONTENTION|CODE|'not measured') suffix=" · attribution $word (${attribution#*$'\t'})" ;;
      *) word='not measured'; suffix=" · attribution not measured (attribution unreadable: ${attribution//$'\t'/ })" ;;
    esac
  fi
  BUDGET_VERDICT=ERROR
  if ! gate_budget_params "$target" "$budgets"; then
    printf 'budget: ✗ %s — %s\n' "$name" "$GATE_BUDGET_REASON"
    return 1
  fi
  value="$GATE_BUDGET_VALUE"
  if [[ ! "$wall" =~ ^[0-9]+(\.[0-9]+)?$ ]]; then
    printf 'budget: ✗ %s — no wall time read (got %s); the gate table is missing or has no STEPS row\n' "$name" "${wall:-nothing}"
    return 1
  fi
  wall="$(gate_wall_ceil "$wall")"
  if [[ -z "$value" ]]; then
    printf 'budget: ✗ %s — UNBUDGETED: no row in %s (add one, `unpinned` until three green runs)\n' "$name" "$(basename "$budgets")"
    return 1
  fi
  if [[ "$value" == unpinned ]]; then
    BUDGET_VERDICT=UNPINNED
    printf 'budget: %s unpinned — recorded, not judged: %ss\n' "$name" "$wall"
    return 0
  fi
  if [[ ! "$value" =~ ^[0-9]+$ ]]; then
    printf 'budget: ✗ %s — invalid budget %s in %s\n' "$name" "$value" "$budgets"
    return 1
  fi
  read -r limit fail_limit < <(gate_budget_limits "$value" "$GATE_BUDGET_TOLERANCE" "$GATE_BUDGET_FAIL_FACTOR")
  if (( wall > fail_limit )); then
    if [[ "$word" == CONTENTION ]]; then
      BUDGET_VERDICT=WARN
      printf 'budget: ⚠ %s — %ss over the fail limit %ss (limit %ss ×%s)%s · downgraded from FAIL\n' "$name" "$wall" "$fail_limit" "$limit" "$GATE_BUDGET_FAIL_FACTOR" "$suffix"
      return 0
    fi
    BUDGET_VERDICT=FAIL
    printf 'budget: ✗ %s — %ss over the fail limit %ss (limit %ss ×%s)%s\n' "$name" "$wall" "$fail_limit" "$limit" "$GATE_BUDGET_FAIL_FACTOR" "$suffix"
    return 1
  fi
  if (( wall > limit )); then
    BUDGET_VERDICT=WARN
    printf 'budget: ⚠ %s — %ss over limit %ss; fails past %ss (budget %ss ×%s ×%s)%s\n' "$name" "$wall" "$limit" "$fail_limit" "$value" "$GATE_BUDGET_TOLERANCE" "$GATE_BUDGET_FAIL_FACTOR" "$suffix"
    return 0
  fi
  BUDGET_VERDICT=PASS
  printf 'budget: ✓ %s — %ss within %ss (budget %ss ×%s)\n' "$name" "$wall" "$limit" "$value" "$GATE_BUDGET_TOLERANCE"
}

# gate_derive_bound <target>: when STEPS_BOUND_S is unset, exports the target's budget fail limit
# as the per-step bound; a budget that cannot give one falls back to 1500 s and says why.
# Returns 0 when it set the bound (the caller unsets it after the run), 1 when the caller's stays.
gate_derive_bound() {
  local target="$1" budgets="$REPO_ROOT/infra/fence/gate-budget.yml" reason="" bound=1500 limit fail_limit
  [[ -z "${STEPS_BOUND_S:-}" ]] || return 1
  if ! gate_budget_params "$target" "$budgets"; then
    reason="$GATE_BUDGET_REASON"
  elif [[ -z "$GATE_BUDGET_VALUE" ]]; then
    reason="no row for gate($target) in $(basename "$budgets")"
  elif [[ "$GATE_BUDGET_VALUE" == unpinned ]]; then
    reason="gate($target) is unpinned in $(basename "$budgets")"
  elif [[ ! "$GATE_BUDGET_VALUE" =~ ^[0-9]+$ ]]; then
    reason="invalid budget $GATE_BUDGET_VALUE in $budgets"
  else
    read -r limit fail_limit < <(gate_budget_limits "$GATE_BUDGET_VALUE" "$GATE_BUDGET_TOLERANCE" "$GATE_BUDGET_FAIL_FACTOR")
    if (( fail_limit >= 1 )); then bound="$fail_limit"; else reason="fail limit $fail_limit s from $budgets is no bound"; fi
  fi
  [[ -z "$reason" ]] || printf 'gate: step bound %s s — %s\n' "$bound" "$reason"
  export STEPS_BOUND_S="$bound"
}

# gate_write_meta <run dir> <target> <started> <finished>: gate.meta, key<TAB>value; a value that
# cannot be read is "NA <reason>", never an empty or a made-up one.
gate_write_meta() {
  local run_dir="$1" target="$2" started="$3" finished="$4" commit dirty jobs heavy_jobs out
  if commit="$(repo_git rev-parse --short=12 HEAD 2>&1)"; then :; else commit="NA git rev-parse failed: ${commit//$'\n'/ }"; fi
  if out="$(repo_git status --porcelain 2>&1)"; then
    if [[ -z "$out" ]]; then dirty=0; else dirty="$(printf '%s\n' "$out" | wc -l | tr -d ' ')"; fi
  else dirty="NA git status failed: ${out//$'\n'/ }"; fi
  if jobs="$(steps_jobs 2>&1)"; then :; else jobs="NA ${jobs//$'\n'/ }"; fi
  if heavy_jobs="$(steps_heavy_jobs 2>&1)"; then :; else heavy_jobs="NA ${heavy_jobs//$'\n'/ }"; fi
  {
    printf 'target\t%s\ncommit\t%s\ndirty_files\t%s\n' "$target" "$commit" "$dirty"
    printf 'started\t%s\nfinished\t%s\n' "$started" "$finished"
    printf 'fixtures\t%s\nsteps_bound_s\t%s\n' "$([[ "${PFM_GATE_FIXTURES:-}" == 1 ]] && echo 1 || echo 0)" "${STEPS_BOUND_S:-1500}"
    printf 'steps_jobs\t%s\nsteps_heavy_jobs\t%s\n' "$jobs" "$heavy_jobs"
  } > "$run_dir/gate.meta"
}

checks_templates_mirrors() {
  checks_templates_mirrors_generate
  checks_templates_mirrors_marker
  checks_templates_mirrors_roster
  checks_templates_mirrors_opencode
  checks_templates_mirrors_manifest
}

checks_gate_check_map() { # run dir
  local run_dir="$1" check_map_pfm="$1/bin/pfm"
  # The pfm pfm.unit prebuilt (test-shard.sh --bin-dir) when this gate ran it and it built; else build one.
  if [[ ! -x "$check_map_pfm" ]]; then
    check_map_pfm="$run_dir/check-map-pfm"
    if ! go -C pfm build -o "$check_map_pfm" ./cmd/pfm; then
      fail_step "templates: check-map pfm build FAILED"
      return
    fi
  fi
  run "templates: lane↔command map (check-map)" -- bash infra/fence/lanes/check-map.sh --pfm "$check_map_pfm"
}

# gate_resources_stop <sampler pid> <run dir>: stops the gate's resource sampler and says when its
# record is missing — the judge then reads "not measured", never a quiet pass.
gate_resources_stop() {
  local pid="$1" run_dir="$2" rc attempt
  # sampler.sh emits its header only after arming TERM; a stop during bootstrap
  # can otherwise be lost and leave wait hanging after a very short steps_run.
  for ((attempt=0; attempt<50; attempt++)); do
    [[ -s "$run_dir/resources.tsv" ]] && break
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.1
  done
  if [[ ! -s "$run_dir/resources.tsv" ]] && kill -0 "$pid" 2>/dev/null; then
    printf 'PROFILE resources NOT RECORDED — sampler readiness timed out\n'
    kill -KILL "$pid" 2>/dev/null || true
  else
    kill -TERM "$pid" 2>/dev/null || true # already gone: its exit status below says why
  fi
  if wait "$pid"; then rc=0; else rc=$?; fi
  if (( rc != 0 )); then
    printf 'PROFILE resources NOT RECORDED — sampler.sh exit %s (its stderr above)\n' "$rc"
  elif [[ ! -s "$run_dir/resources.tsv" ]]; then
    printf 'PROFILE resources NOT RECORDED — sampler.sh left no %s\n' "$run_dir/resources.tsv"
  fi
}

# gate_attribution <pfm dir> <run dir>: the judge's `<WORD><TAB><evidence>` line for the whole
# run; a judge that fails or prints no verdict line is `not measured` with what it said.
gate_attribution() {
  local d="$1" run_dir="$2" out line
  if out="$(bash "$d/scripts/test-contention.sh" window --resources "$run_dir/resources.tsv" 2>&1)"; then
    while IFS= read -r line; do
      if [[ "$line" =~ ^(CONTENTION|CODE|not\ measured)$'\t' ]]; then printf '%s\n' "$line"; return 0; fi
    done <<< "$out"
    printf 'not measured\tjudge failed: no verdict line in: %s\n' "${out//$'\n'/ }"
    return 0
  fi
  printf 'not measured\tjudge failed: %s\n' "${out//$'\n'/ }"
}

gate_run() { # pfm, templates, or all
  local target="${1:-all}" d="$REPO_ROOT/pfm" base run_dir row stem f step_rc=0 budget_rc=0 derived=0 started
  local gate_pid sampler_pid attribution summary_rc
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
  timing_run_note "$run_dir"
  steps_reset
  if [[ "$target" == pfm || "$target" == all ]]; then
    steps_add_heavy pfm.unit checks_pfm_unit "$d" "$run_dir"
    steps_add_heavy pfm.e2e checks_pfm_e2e "$d" "$run_dir"
    steps_add_heavy pfm.race checks_pfm_race "$d" "$run_dir"
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
  if [[ "${PFM_GATE_FIXTURES:-}" == 1 ]]; then
    for stem in go-fail go-hang go-slow shell-fail shell-hang; do
      steps_add "fixture.$stem" checks_gate_fixture "$stem" "$run_dir"
      case "$stem" in go-slow) steps_set_bound 45 ;; shell-hang) steps_set_bound 5 ;; esac
    done
  fi
  steps_barrier
  if [[ "$target" == pfm || "$target" == all ]]; then
    # Heavy static steps longest first, so the short ones queue behind them for a heavy slot.
    steps_add_heavy pfm.lint-new run 'pfm: lint-new (golangci-lint, changed lines)' -- make -C "$d" --no-print-directory lint-new
    steps_add_heavy pfm.fmt-check run 'pfm: fmt-check (gofumpt + gci + golines)' -- make -C "$d" --no-print-directory fmt-check
    steps_add_heavy pfm.vet run 'pfm: go vet' -- go -C "$d" vet ./...
    steps_add_heavy pfm.vet-darwin run 'pfm: go vet (darwin/arm64)' -- env GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go -C "$d" vet ./...
    steps_add pfm.arch run 'pfm: architecture ratchet' -- bash "$d/scripts/arch-check.sh"
    steps_add pfm.testplan run 'pfm: flow-matrix citations' -- bash "$d/scripts/testplan-check.sh"
  fi
  if [[ "$target" == templates || "$target" == all ]]; then
    steps_add_heavy templates.check-map checks_gate_check_map "$run_dir"
    checks_templates_steps steps_add
  fi
  if gate_derive_bound "$target"; then derived=1; fi
  started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  if ! gate_write_meta "$run_dir" "$target" "$started" 'NA running'; then
    fail_step "gate: cannot write $run_dir/gate.meta"
    (( derived == 0 )) || unset STEPS_BOUND_S
    return 1
  fi
  # The words of a `&` command expand in the forked child: read this shell's pid before the fork.
  gate_pid="$BASHPID"
  bash "$REPO_ROOT/infra/fence/sampler.sh" run --out "$run_dir/resources.tsv" --parent "$gate_pid" &
  sampler_pid=$!
  if steps_run "$run_dir"; then step_rc=0; else step_rc=$?; fi
  gate_resources_stop "$sampler_pid" "$run_dir"
  attribution="$(gate_attribution "$d" "$run_dir")"
  row="$(awk -F '\t' '$1=="STEPS" {print $3}' "$run_dir/gate.tsv" 2>/dev/null)"
  BUDGET_VERDICT=ERROR
  if gate_budget_verdict "$target" "$row" "" "$attribution"; then budget_rc=0; else budget_rc=$?; fi
  if [[ "$row" =~ ^[0-9]+(\.[0-9]+)?$ ]]; then row="$(gate_wall_ceil "$row")"; else row=NA; fi
  if [[ -f "$run_dir/gate.tsv" ]] && ! printf 'BUDGET\t%s\t%s\n' "$BUDGET_VERDICT" "$row" >> "$run_dir/gate.tsv"; then
    fail_step "gate: cannot append the BUDGET row to $run_dir/gate.tsv"; budget_rc=1
  fi
  # The PROFILE block, profile.tsv and profile/INDEX.txt; a report that fails is named, the
  # gate's verdict stays the steps' and the budget's.
  if bash "$REPO_ROOT/infra/fence/profile-report.sh" summary "$run_dir"; then summary_rc=0; else summary_rc=$?; fi
  (( summary_rc == 0 )) || bad "PROFILE summary FAILED — profile-report.sh summary exit $summary_rc; no PROFILE block, profile.tsv or profile/INDEX.txt for $run_dir"
  if ! gate_write_meta "$run_dir" "$target" "$started" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"; then
    fail_step "gate: cannot rewrite $run_dir/gate.meta"; budget_rc=1
  fi
  (( derived == 0 )) || unset STEPS_BOUND_S
  (( step_rc == 0 && budget_rc == 0 ))
}
