# shellcheck shell=bash
# lib/verdict.sh: beats and checks (D13), the passport and vital signs (L4-L16),
# checks.tsv -> report.md (D16, L17) and the ledger row. Verdicts: PASS FAIL
# ERROR GAP STOPPED; exit 0 on PASS/GAP only, 1 on FAIL, 2 on ERROR or STOPPED.

# The words a lab_scenario body may use (tests/scenarios_test.sh holds every scenario to them).
# shellcheck disable=SC2034
LAB_VERBS='beat need_corpus need_agent need_dir_mode need_fixture_plugin expect_error expect_bypass bypass_seated say wait_turn turn slash wait_subagent exit_session read_turns fill_until_boundary witness_compactions decision_has decision_lacks decision_count level_count decision_first decision_precondition level_precondition decision_prefix_save decision_prefix_kept level_has level_lacks jsonl_has file_absent transcript_has agent_transcript_has reply_has pane_lacks mark_has'

expect_error() { LAB_EXPECT_ERRORS+=("$1"); } # <event>: a level-log error row this scenario induces
expect_bypass() { LAB_EXPECT_BYPASS+=("$1: $2 bypassed by"); } # <plugin> <event>: a hook bypass this scenario induces

check() { # <id> <row> <verdict> <evidence>: one checks.tsv row, one printed line
  local ev=${4//$'\t'/ } m='✗'
  ev=${ev//$'\n'/ }
  printf '%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$ev" >> "$LAB_RUN/checks.tsv"
  case $3 in PASS) m='✓' ;; GAP) m='·' ;; esac
  printf '%s %s %s %s\n' "$m" "$1" "$3" "$ev"
}

lab_need_fail() { # <evidence>: a failed prelude is a setup ERROR row, and every later beat is blocked by it
  check "${LAB_SCENARIO:-scenario}/setup" "${LAB_ROWS[0]:-}" ERROR "$1"
  LAB_DEAD=${LAB_SCENARIO:-scenario}/setup LAB_DEAD_VERDICT=ERROR
  return 2
}

lab_check_verdict() { awk -F '\t' -v c="$1" '$1 == c { v = $3 } END { print v }' "$LAB_RUN/checks.tsv" 2>/dev/null; } # <id>

beat() { # <slug> [--row R] [--needs slug]... -- <verb> [args]: one scenario check
  local slug=$1 row=${LAB_ROWS[0]:-} id v rc need needs=()
  shift
  while (( $# )); do
    case $1 in
      --row) row=$2; shift 2 ;;
      --needs) needs+=("$2"); shift 2 ;;
      --) shift; break ;;
      *) check "${LAB_SCENARIO:-scenario}/$slug" "$row" ERROR "beat usage: $1"; return 2 ;;
    esac
  done
  id=${LAB_SCENARIO:-scenario}/$slug
  LAB_BEAT=$slug LAB_EV='' LAB_FATAL=''
  if [[ -n ${LAB_DEAD:-} ]]; then
    v=ERROR; [[ ${LAB_DEAD_VERDICT:-} == GAP ]] && v=GAP
    check "$id" "$row" "$v" "blocked-by $LAB_DEAD"
    [[ $v == GAP ]] && return 3
    return 2
  fi
  for need in "${needs[@]}"; do
    v=$(lab_check_verdict "${LAB_SCENARIO:-scenario}/$need")
    [[ $v == PASS ]] && continue
    [[ $v == GAP ]] && { check "$id" "$row" GAP "blocked-by ${LAB_SCENARIO:-scenario}/$need"; return 3; }
    check "$id" "$row" ERROR "blocked-by ${LAB_SCENARIO:-scenario}/$need"
    return 2
  done
  "$@"; rc=$?
  case $rc in 0) v=PASS ;; 1) v=FAIL ;; 3) v=GAP ;; 5) v=STOPPED ;; *) v=ERROR ;; esac
  check "$id" "$row" "$v" "$LAB_EV"
  if [[ -n ${LAB_FATAL:-} ]]; then LAB_DEAD=$id LAB_DEAD_VERDICT=$v; fi
  return "$rc"
}

lab_verdict_of() { # <checks.tsv>: the run's verdict word
  awk -F '\t' '{ s[$3] = 1; n++ } END {
    if (!n) print "ERROR"; else if (s["STOPPED"]) print "STOPPED"; else if (s["ERROR"]) print "ERROR"
    else if (s["FAIL"]) print "FAIL"; else print "PASS" }' "$1" 2>/dev/null || echo ERROR
}

lab_render_report() { # <run>: report.md from meta.json and checks.tsv, printed; rc 0 PASS/GAP, 1 FAIL, 2 ERROR/STOPPED
  local run=$1 v
  [[ -f $run/meta.json && -f $run/checks.tsv ]] || { printf 'ERROR report: no meta.json or checks.tsv in %s\n' "$run" >&2; return 2; }
  v=$(lab_verdict_of "$run/checks.tsv")
  {
    jq -r '"# plugin-lab report · \(.run_id)", "",
      "- scenario \(.scenario) · plugin \(.plugin) \(.version // "?") · claude \(.claude // "?")",
      "- rev \(.rev // "?") · dirty \(.dirty // "?") · started \(.started // "?") · wall \(.wall_s // "?")s", ""' "$run/meta.json"
    printf '## Passport\n\n'
    awk -F '\t' '$1 ~ /^passport\// { printf "- %s %s %s\n", $1, $3, $4 }' "$run/checks.tsv"
    printf '\n## Checks\n\n| check | row | verdict | evidence |\n| --- | --- | --- | --- |\n'
    awk -F '\t' '$1 !~ /^passport\// { gsub(/\|/, "\\\\|", $4); printf "| %s | %s | %s | %s |\n", $1, $2, $3, $4 }' "$run/checks.tsv"
    printf '\n'
    jq -r --arg v "$v" '"verdict \($v) · $\(.usd // "?") / \(.cap)"' "$run/meta.json"
  } > "$run/report.md"
  cat "$run/report.md"
  case $v in PASS) return 0 ;; FAIL) return 1 ;; *) return 2 ;; esac
}

lab_ledger_row() { # <ledger> <verdict>: one ledger row from meta.json; RUNNING has no spend yet, so it counts at its cap
  local fields=()
  mapfile -t fields < <(jq -r --arg v "$2" \
    '.run_id, .scenario, .plugin, .rev, .dirty, $v, (.usd // "?"), .cap, .started, (.wall_s // "?"), "lab-reports/\(.run_id).md"' \
    "$LAB_RUN/meta.json")
  (( ${#fields[@]} == 11 )) || { printf 'ERROR ledger: meta.json unreadable in %s\n' "$LAB_RUN" >&2; return 2; }
  lab_ledger_append "$1" "${fields[@]}"
}

lab_ledger_reserve() { # <ledger> <cap> <budget>: under the ledger lock, the budget check and the run's RUNNING row
  # as one step, so runs started together never each pass on the same sum; a run id already there is refused
  local l=$1 id line rc=0
  id=$(jq -r .run_id "$LAB_RUN/meta.json")
  lab_ledger_lock "$l" || return 2
  if [[ -e $l ]] && awk -F '\t' -v id="$id" '$1 == id { f = 1 } END { exit !f }' "$l"; then
    printf 'ERROR ledger already holds run id %s in %s\n' "$id" "$l" >&2; rc=2
  elif line=$(lab_budget_preflight "$l" "$2" "$3"); then lab_ledger_row "$l" RUNNING || rc=2
  else rc=2; fi
  lab_ledger_unlock "$l"
  (( rc == 0 )) && printf '%s\n' "$line"
  return "$rc"
}

lab_ledger_record() { # <ledger>: the report copy beside it, then the run's final row, which the sum takes in place
  # of its RUNNING one; the ledger is append-only, so no run's row is ever rewritten away
  local l=$1 dir id rc=0
  dir=$(dirname "$l")
  id=$(jq -r .run_id "$LAB_RUN/meta.json")
  if ! mkdir -p "$dir/lab-reports" 2>/dev/null || ! cp "$LAB_RUN/report.md" "$dir/lab-reports/$id.md" 2>/dev/null; then
    printf 'ERROR ledger cannot write %s/lab-reports/%s.md\n' "$dir" "$id" >&2
    return 2
  fi
  lab_ledger_lock "$l" || return 2
  lab_ledger_row "$l" "$(lab_verdict_of "$LAB_RUN/checks.tsv")" || rc=2
  lab_ledger_unlock "$l"
  return "$rc"
}

lab_identity() { # <plugin dir>: "{rev} {dirty hash}" (D23): the hash covers the path and content of every
  # modified or untracked file under the folder, never the engine-written tsconfig.json or .claude-plugin/types/
  local rev f sum lines='' paths=()
  rev=$(git -C "$1" rev-parse HEAD 2>/dev/null) || return 2
  mapfile -d '' -t paths < <(git -C "$1" ls-files -z -m -o --exclude-standard -- . \
    ':(exclude)tsconfig.json' ':(exclude).claude-plugin/types' 2>/dev/null; printf 'rc=%s' "$?")
  [[ ${paths[-1]:-} == rc=0 ]] || return 2
  unset 'paths[-1]'
  for f in "${paths[@]}"; do
    if [[ -f $1/$f ]]; then sum=$(git -C "$1" hash-object --no-filters -- "$f" 2>/dev/null) || return 2
    else sum=-; fi
    lines+="$sum $f"$'\n'
  done
  printf '%s %s\n' "$rev" "$(lab_sha256 "$lines" | cut -c1-12)"
}

passport_env() { # L5: the observed env keys equal the allowlist plus LAB_ENV
  local want got extra missing
  if [[ ! -s $LAB_RUN/env-keys ]]; then
    check passport/env-allowlist R8 ERROR "could-not-look $(head -c 200 "$LAB_RUN/env-keys.err" 2>/dev/null)"
    return
  fi
  if grep -q '^ERROR' "$LAB_RUN/env-keys"; then check passport/env-allowlist R8 ERROR "$(head -n 1 "$LAB_RUN/env-keys")"; return; fi
  want=$(lab_env_keys); got=$(cat "$LAB_RUN/env-keys")
  extra=$(LC_ALL=C comm -13 <(printf '%s\n' "$want") <(printf '%s\n' "$got") | tr '\n' ' ')
  missing=$(LC_ALL=C comm -23 <(printf '%s\n' "$want") <(printf '%s\n' "$got") | tr '\n' ' ')
  if [[ -z ${extra// } && -z ${missing// } ]]; then
    check passport/env-allowlist R8 PASS "$(wc -l <<< "$got" | tr -d ' ') keys"
  else
    check passport/env-allowlist R8 FAIL "${extra:+${extra}not in the allowlist }${missing:+missing ${missing}}"
  fi
}

passport_live_store() { # L12: the run's session id appears in no live store
  local s r out seen=' ' hits='' n=0
  [[ -n ${LAB_BOOTED:-} ]] || { check passport/live-store-untouched R8 PASS 'no session started'; return; }
  [[ -n ${LAB_SID:-} ]] || { check passport/live-store-untouched R8 ERROR 'could-not-look no session id'; return; }
  for s in "$LAB_AUTH_FROM" "$LAB_REAL_HOME/.claude"; do
    r=$(cd "$s" 2>/dev/null && pwd -P) || r=$s
    [[ $seen == *" $r "* ]] && continue
    seen+="$r " n=$((n + 1))
    [[ -d $r/projects ]] || continue
    out=$(find "$r/projects" -maxdepth 2 -name "$LAB_SID.jsonl" 2>&1) \
      || { check passport/live-store-untouched R8 ERROR "could-not-look $r/projects: ${out%%$'\n'*}"; return; }
    hits+=${out:+"$out "}
  done
  if [[ -z $hits ]]; then check passport/live-store-untouched R8 PASS "$n stores"
  else check passport/live-store-untouched R8 FAIL "$hits"; fi
}

passport_keychain() { # L13
  local out
  out=$(lab_keychain_clean "$LAB_RUN")
  check passport/keychain-clean R8 "${out%% *}" "${out#* }"
}

passport_bypass() { # L14: any "bypassed by" line FAILs unless the scenario declared it
  local lines l e ok=0 bad=''
  lines=$(lab_bypass_lines "$LAB_RUN/debug.txt") || { check passport/hook-bypass R8 ERROR "could-not-look $LAB_RUN/debug.txt"; return; }
  while IFS= read -r l; do
    [[ -n $l ]] || continue
    for e in "${LAB_EXPECT_BYPASS[@]}"; do [[ $l == *"$e"* ]] && { ok=$((ok + 1)); continue 2; }; done
    bad=$l; break
  done <<< "$lines"
  if [[ -z $bad ]]; then check passport/hook-bypass R8 PASS "$ok declared bypass lines"
  else check passport/hook-bypass R8 FAIL "$bad"; fi
}

passport_identity() { # L16: rev and dirty hash equal at start and end, and the engine never hot-reloaded the plugin's hooks
  local end reload
  end=$(lab_identity "$LAB_PLUGIN_DIR") || { check passport/identity R8 ERROR "could-not-look git in $LAB_PLUGIN_DIR"; return; }
  LAB_IDENTITY_END=$end
  # the content hash sees an edit between start and end; the engine's reload line sees one it loaded mid-run and the tree then lost
  [[ -f $LAB_RUN/debug.txt ]] || { check passport/identity R8 ERROR "could-not-look $LAB_RUN/debug.txt"; return; }
  reload=$(grep -F "hooks module $LAB_PLUGIN@" "$LAB_RUN/debug.txt" | grep -F ' reloaded ' | head -n 1)
  if [[ $end != "$LAB_IDENTITY" ]]; then check passport/identity R8 FAIL "tree-changed start $LAB_IDENTITY end $end"
  elif [[ -n $reload ]]; then check passport/identity R8 FAIL "tree-changed hot-reload ${reload%% *}"
  else check passport/identity R8 PASS "$LAB_PLUGIN $LAB_PLUGIN_VERSION rev ${end% *} dirty ${end#* }"; fi
}

passport_installed_off() { # D10: each live id off in the run settings, the run store installs none of it
  local ids id off=0 rs=$LAB_RUN/config/plugins/installed_plugins.json
  ids=$(lab_installed_ids "$LAB_AUTH_FROM" "$LAB_PLUGIN" 2>&1) || { check passport/installed-copy-off R8 ERROR "${ids#ERROR }"; return; }
  for id in $ids; do
    jq -e --arg id "$id" '.enabledPlugins[$id] == false' "$LAB_RUN/settings.json" >/dev/null 2>&1 \
      || { check passport/installed-copy-off R8 FAIL "$id not false in settings.json"; return; }
    off=$((off + 1))
  done
  if [[ -e $rs ]] && ! jq -e --arg n "$LAB_PLUGIN" '.plugins | keys | all(split("@")[0] != $n)' "$rs" >/dev/null 2>&1; then
    check passport/installed-copy-off R8 FAIL "run store installs $LAB_PLUGIN: $rs"
    return
  fi
  check passport/installed-copy-off R8 PASS "$off live ids off; run store installs none of it"
}

passport_spend() { # D9: metered against the cap
  if [[ -n ${LAB_STOPPED:-} ]]; then # the session is gone now: the final read adds the calls in flight at the kill
    if lab_meter_read --final 2>/dev/null; then check passport/spend R8 STOPPED "$LAB_STOPPED; final $LAB_USD/$LAB_CAP"
    else check passport/spend R8 STOPPED "$LAB_STOPPED; final read failed: $LAB_METER_EV"; fi
    return
  fi
  if [[ -z ${LAB_BOOTED:-} ]]; then LAB_USD=0.00; check passport/spend R8 PASS "0.00 / $LAB_CAP no session started"; return; fi
  if ! lab_meter_read --final 2>/dev/null; then LAB_USD='?'; check passport/spend R8 ERROR "$LAB_METER_EV"; return; fi
  if lab_over_cap; then check passport/spend R8 STOPPED "budget $LAB_USD/$LAB_CAP"
  else check passport/spend R8 PASS "$LAB_USD / $LAB_CAP"; fi
}

lab_passport() { passport_env; passport_live_store; passport_keychain; passport_bypass; passport_identity; passport_installed_off; passport_spend; }

lab_since() { # jq: the row's ts (ISO string or epoch ms) at or after the run start
  printf '((.ts // "") | if type == "number" then (. / 1000 | floor | todate) else tostring end)[0:19] >= "%s"' "${LAB_STARTED:0:19}"
}

vital_first_row() { # L11: the plugin's earliest row since the start, a level or decision row, under PLUGIN_LOG_DIR
  local d=$LAB_RUN/logs/$LAB_PLUGIN f files rows all=""
  [[ -d $d ]] || { check vital/first-row R1 ERROR "LOG-ABSENT $d"; return; }
  files=$(find "$d" -type f -name '*.jsonl') || { check vital/first-row R1 ERROR "unreadable $d"; return; }
  while IFS= read -r f; do
    [[ -n $f ]] || continue
    rows=$(lab_rows "$f" "select($(lab_since)) | \"\\(.ts) \\(.event // \"?\")\"") || { check vital/first-row R1 ERROR "${rows#ERROR }"; return; }
    [[ -z $rows ]] || all+=$(sed "s|\$| ${f#"$d"/}|" <<< "$rows")$'\n'
  done <<< "$files"
  if [[ -n $all ]]; then
    check vital/first-row R1 PASS "$(grep -c . <<< "$all") rows in $d; first $(printf %s "$all" | LC_ALL=C sort | sed -n 1p)"
  else check vital/first-row R1 FAIL "no row since $LAB_STARTED under $d"; fi
}

vital_no_new_errors() { # L11: no error row from this run but the declared ones
  local d=$LAB_RUN/logs/$LAB_PLUGIN rows exp
  exp=$(jq -cn '$ARGS.positional' --args "${LAB_EXPECT_ERRORS[@]}")
  [[ -d $d ]] || { check vital/no-new-errors R1 ERROR "LOG-ABSENT $d"; return; }
  [[ -f $d/errors.jsonl ]] || { check vital/no-new-errors R1 PASS "no errors.jsonl in $d"; return; }
  rows=$(lab_rows "$d/errors.jsonl" "select($(lab_since)) | select((.event // \"\") as \$e | $exp | any(.[]; . == \$e) | not)" ) \
    || { check vital/no-new-errors R1 ERROR "${rows#ERROR }"; return; }
  if [[ -z $rows ]]; then check vital/no-new-errors R1 PASS "0 undeclared rows in $d/errors.jsonl"
  else check vital/no-new-errors R1 FAIL "$(wc -l <<< "$rows" | tr -d ' ') rows: ${rows%%$'\n'*}"; fi
}

lab_vitals() { [[ ${LAB_LOGGER:-yes} == yes ]] || return 0; vital_first_row; vital_no_new_errors; }
