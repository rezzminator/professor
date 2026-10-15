#!/usr/bin/env bash
set -uo pipefail
SHTEST_TAG=checks-test
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
SUT="${CHECKS_SUT:-$(dirname -- "${BASH_SOURCE[0]}")/../../checks.sh}"
source "$SUT"

REPO_ROOT="$(cd "$(dirname -- "${BASH_SOURCE[0]}")/../../../.." && pwd)"
TMP_BASE="$T"
FAILURES=0
shtest_unset_gate_env # run nested under the gate's own stepprof wrapper, the suite starts from none of its knobs
cat > "$T/budgets.yml" <<'YAML'
tolerance: 1.25
fail_factor: 2
gate:
  all: unpinned
  pfm: 10
YAML

if out="$(gate_budget_verdict all 7.1 "$T/budgets.yml")" &&
  [ "$out" = 'budget: gate(all) unpinned — recorded, not judged: 8s' ]; then
  ok 'unpinned budget records rounded-up wall without judging'
else bad 'unpinned budget' "$out"; fi
if ! out="$(gate_budget_verdict templates 2 "$T/budgets.yml")" &&
  [[ "$out" == *'UNBUDGETED'* ]]; then
  ok 'missing target budget is red'
else bad 'missing budget' "$out"; fi
if out="$(gate_budget_verdict pfm 18 "$T/budgets.yml")" &&
  [[ "$out" == 'budget: ⚠ gate(pfm) — 18s over limit 12s; fails past 24s (budget 10s ×1.25 ×2)' ]]; then
  ok 'wall at 1.5 times limit warns and passes'
else bad 'warning tier' "$out"; fi
if ! out="$(gate_budget_verdict pfm 25.2 "$T/budgets.yml")" &&
  [[ "$out" == 'budget: ✗ gate(pfm) — 26s over the fail limit 24s (limit 12s ×2)' ]]; then
  ok 'wall at 2.1 times limit fails after rounding up'
else bad 'fail tier' "$out"; fi
if out="$(gate_budget_verdict pfm 12 "$T/budgets.yml")" &&
  [[ "$out" == *'12s within 12s (budget 10s ×1.25)'* ]]; then
  ok 'pinned budget accepts wall at tolerance'
else bad 'pinned within budget' "$out"; fi
if ! out="$(gate_budget_verdict pfm 1 "$T/no-budget.yml")" &&
  [[ "$out" == *"$T/no-budget.yml"* ]]; then
  ok 'unreadable budget names its path and is red'
else bad 'unreadable budget' "$out"; fi
if ! out="$(gate_budget_verdict pfm '' "$T/budgets.yml")" &&
  [[ "$out" == *'✗ gate(pfm) — no wall time read (got nothing); the gate table is missing or has no STEPS row'* ]]; then
  ok 'an unread wall is red, never 0s within budget, and names the STEPS row'
else bad 'unread wall' "$out"; fi

printf 'tolerance: abc\ngate:\n  pfm: 10\n' > "$T/bad-tolerance.yml"
if ! out="$(gate_budget_verdict pfm 1 "$T/bad-tolerance.yml")" &&
  [[ "$out" == *"invalid tolerance abc in $T/bad-tolerance.yml"* ]]; then
  ok 'a non-numeric tolerance is named, never judged as a limit of 0'
else bad 'invalid tolerance' "$out"; fi
printf 'tolerance: 1.25\ngate:\n  pfm: 10\n' > "$T/no-fail-factor.yml"
if ! out="$(gate_budget_verdict pfm 1 "$T/no-fail-factor.yml")" &&
  [[ "$out" == *"✗ gate(pfm) — no fail_factor in $T/no-fail-factor.yml"* ]]; then
  ok 'missing fail_factor is named and red'
else bad 'missing fail_factor' "$out"; fi
printf 'tolerance: 1.25\nfail_factor: abc\ngate:\n  pfm: 10\n' > "$T/bad-fail-factor.yml"
if ! out="$(gate_budget_verdict pfm 1 "$T/bad-fail-factor.yml")" &&
  [[ "$out" == *"✗ gate(pfm) — invalid fail_factor abc in $T/bad-fail-factor.yml"* ]]; then
  ok 'invalid fail_factor is named and red'
else bad 'invalid fail_factor' "$out"; fi

# BUDGET_VERDICT is a global of the call, so these run in this shell, not in $( ).
verdict_case() { # verdict_case <expected> <target> <wall> <budget file>
  local want="$1" got
  BUDGET_VERDICT=stale
  gate_budget_verdict "$2" "$3" "$4" >/dev/null; got="$BUDGET_VERDICT"
  if [ "$got" = "$want" ]; then ok "BUDGET_VERDICT $want for target $2 wall '$3' ($(basename "$4"))"; else bad "BUDGET_VERDICT $want" "got $got"; fi
}
verdict_case PASS pfm 12 "$T/budgets.yml"
verdict_case WARN pfm 18 "$T/budgets.yml"
verdict_case FAIL pfm 25.2 "$T/budgets.yml"
verdict_case UNPINNED all 7.1 "$T/budgets.yml"
verdict_case ERROR pfm 1 "$T/no-budget.yml"
verdict_case ERROR pfm '' "$T/budgets.yml"
verdict_case ERROR templates 2 "$T/budgets.yml"
verdict_case ERROR pfm 1 "$T/bad-tolerance.yml"
verdict_case ERROR pfm 1 "$T/no-fail-factor.yml"
verdict_case ERROR pfm 1 "$T/bad-fail-factor.yml"
printf 'tolerance: 1.25\nfail_factor: 2\ngate:\n  pfm: abc\n' > "$T/bad-value.yml"
verdict_case ERROR pfm 1 "$T/bad-value.yml"

# attr_case <expected verdict> <expected rc> <wall> <word> <evidence> <expected line>: the 4th
# argument is test-contention.sh's `<WORD><TAB><evidence>` line; run in this shell for BUDGET_VERDICT.
attr_case() {
  local out rc
  BUDGET_VERDICT=stale
  gate_budget_verdict pfm "$3" "$T/budgets.yml" "$4"$'\t'"$5" >"$T/attr.out"; rc=$?
  out="$(cat "$T/attr.out")"
  if [ "$BUDGET_VERDICT" = "$1" ] && [ "$rc" = "$2" ] && [ "$out" = "$6" ]; then
    ok "attribution $4 at wall $3: BUDGET_VERDICT $1, rc $2, line as pinned"
  else bad "attribution $4 at wall $3" "verdict $BUDGET_VERDICT rc $rc" "$out"; fi
}
attr_case PASS 0 12 CODE 'spin ×1.0 (< ×1.5)' 'budget: ✓ gate(pfm) — 12s within 12s (budget 10s ×1.25)'
attr_case WARN 0 18 CODE 'tick-deficit -1% (≤ -5%)' \
  'budget: ⚠ gate(pfm) — 18s over limit 12s; fails past 24s (budget 10s ×1.25 ×2) · attribution CODE (tick-deficit -1% (≤ -5%))'
attr_case WARN 0 25.2 CONTENTION 'cpu-psi 40% (≥ 20%)' \
  'budget: ⚠ gate(pfm) — 26s over the fail limit 24s (limit 12s ×2) · attribution CONTENTION (cpu-psi 40% (≥ 20%)) · downgraded from FAIL'
attr_case FAIL 1 25.2 CODE 'spin ×1.0 (< ×1.5)' \
  'budget: ✗ gate(pfm) — 26s over the fail limit 24s (limit 12s ×2) · attribution CODE (spin ×1.0 (< ×1.5))'
attr_case FAIL 1 25.2 'not measured' 'judge failed: test-contention: malformed header' \
  'budget: ✗ gate(pfm) — 26s over the fail limit 24s (limit 12s ×2) · attribution not measured (judge failed: test-contention: malformed header)'

need_tool() { return 0; }
timing_run_dir() { mkdir -p "$1"; mktemp -d "$1/run.XXXXXX"; }
steps_reset() { : > "$T/registered"; : > "$T/heavy"; : > "$T/bounds"; }
steps_add() { printf '%s\n' "$1" >> "$T/registered"; printf '%s\n' "$*" >> "$T/registered.args"; }
steps_add_heavy() { steps_add "$@"; printf '%s\n' "$1" >> "$T/heavy"; }
steps_barrier() { printf 'BARRIER\n' >> "$T/registered"; }
steps_set_bound() { printf '%s %s\n' "$(grep -v '^BARRIER$' "$T/registered" | tail -1)" "$1" >> "$T/bounds"; }
# Stub scripts log to $STUB_EVENTS; wait_event polls it in 0.1 s ticks, bounded at 5 s, for a line that
# starts with one of its arguments (fixed strings, each naming one run's own directory).
export STUB_EVENTS="$T/events"
wait_event() {
  local i p
  for ((i = 0; i < 50; i++)); do
    for p in "$@"; do
      awk -v p="$p" 'index($0, p) == 1 {found = 1; exit} END {exit !found}' "$STUB_EVENTS" 2>/dev/null && return 0
    done
    sleep 0.1
  done
  return 1
}
event_words() { cut -d' ' -f1 "$STUB_EVENTS" | paste -sd' ' -; }
steps_run() {
  if [ "$REPO_ROOT" = "$T/fx" ]; then # a fixture run's TERM to its sampler comes only after the sampler logged
    wait_event "sampler-start run --out $1/resources.tsv" "sampler-failed run --out $1/resources.tsv"
    printf 'steps\n' >> "$STUB_EVENTS"
  fi
  printf '%s\n' "${STEPS_BOUND_S:-unset}" > "$T/bound.seen"
  cp "$1/gate.meta" "$T/meta.during" 2>/dev/null
  printf 'step\tverdict\tseconds\n' > "$1/gate.tsv"
  [ "${STUB_NO_STEPS:-0}" = 1 ] || printf 'STEPS\tPASS\t%s\n' "${STUB_WALL:-0.1}" >> "$1/gate.tsv"
  return "${STUB_STEPS_RC:-0}"
}
steps_jobs() { printf '6\n'; }
steps_heavy_jobs() { printf '2\n'; }
repo_git() {
  case "$1" in
    rev-parse) printf 'abcdef012345\n' ;;
    status) printf ' M a\n?? b\n M c\n' ;;
  esac
}
graph_order() {
  awk -v target="$1" '
    $0=="BARRIER" {barriers++; after=1; next}
    !after {
      if ($0=="pfm.unit") group=1
      else if ($0=="pfm.e2e") group=2
      else if ($0=="pfm.race") group=3
      else if ($0 ~ /^pfm\.self\./) group=4
      else if ($0 ~ /^templates\.lanes\./) group=5
      else if ($0 ~ /^templates\.demo\./) group=6
      else exit 1
      if (group<previous || (target=="pfm" && group>4) || (target=="templates" && group<5)) exit 1
      previous=group
      next
    }
    after && ($0 ~ /^pfm\.self\./ || $0 ~ /^templates\.(lanes|demo)\./ || $0=="pfm.unit" || $0=="pfm.e2e" || $0=="pfm.race") {exit 1}
    END {if (barriers!=1) exit 1}
  ' "$T/registered"
}
static_names() { sed -n '/^BARRIER$/,$p' "$T/registered" | sed '1d' | sort | paste -sd, -; }
pfm_static='pfm.lint-new,pfm.fmt-check,pfm.vet,pfm.vet-darwin,pfm.arch,pfm.testplan'
templates_static='templates.check-map,templates.clone,templates.leak,templates.plugin-logs,templates.plugin-lab,templates.placeholders,templates.scratch-paths,templates.descriptions,templates.mirrors,templates.generated-agents,templates.release-check,templates.codex-sync,templates.refresh-scope,templates.worktree-script,templates.dev-report,templates.format-md,templates.flight-templates,templates.check-pfm-tests,templates.check-templates-tests,templates.test-pfm-tests,templates.test-templates-tests,templates.unit-path-tests,templates.engine-sync-action-tests,templates.skill-tests'
heavy_names='pfm.e2e,pfm.fmt-check,pfm.lint-new,pfm.race,pfm.unit,pfm.vet,pfm.vet-darwin,templates.check-map'
if gate_run all >"$T/all.out" &&
  [ "$(grep -c '^templates\.leak$' "$T/registered")" -eq 1 ] &&
  [ "$(grep -c '^templates\.mirrors$' "$T/registered")" -eq 1 ] &&
  [ "$(grep -c '^pfm\.unit$' "$T/registered")" -eq 1 ] &&
  [ "$(grep -c '^pfm\.e2e$' "$T/registered")" -eq 1 ] &&
  [ "$(grep -c '^templates\.lanes\.' "$T/registered")" -eq "$(find "$REPO_ROOT/infra/fence/lanes/tests" -name '*_test.sh' | wc -l | tr -d ' ')" ] &&
  [ "$(grep -c '^templates\.demo\.' "$T/registered")" -eq "$(find "$REPO_ROOT/infra/demo/tests" -name '*_test.sh' | wc -l | tr -d ' ')" ] &&
  [ "$(sed -n '1,3p' "$T/registered" | paste -sd, -)" = 'pfm.unit,pfm.e2e,pfm.race' ] &&
  [ "$(static_names)" = "$(printf '%s' "$pfm_static,$templates_static" | tr ',' '\n' | sort | paste -sd, -)" ] &&
  [ "$(sort "$T/heavy" | paste -sd, -)" = "$heavy_names" ] &&
  [ -z "$(sort "$T/registered" | uniq -d)" ] && graph_order all; then
  ok 'all registers tests before one barrier and each static step once'
else bad 'all registration' "$(cat "$T/all.out")" "$(cat "$T/registered")"; fi

if PFM_TEST_TIMING_DIR="$T/real-pfm" gate_run pfm >"$T/pfm.out" &&
  [ "$(sed -n '1,3p' "$T/registered" | paste -sd, -)" = 'pfm.unit,pfm.e2e,pfm.race' ] &&
  [ "$(static_names)" = "$(printf '%s' "$pfm_static" | tr ',' '\n' | sort | paste -sd, -)" ] &&
  [ "$(sort "$T/heavy" | paste -sd, -)" = 'pfm.e2e,pfm.fmt-check,pfm.lint-new,pfm.race,pfm.unit,pfm.vet,pfm.vet-darwin' ] &&
  graph_order pfm; then
  ok 'pfm graph runs its tests before static checks'
else bad 'pfm graph' "$(cat "$T/registered")"; fi
real_run="$(ls -d "$T/real-pfm"/run.* | head -1)"
if grep -Fxq "pfm.e2e checks_pfm_e2e $REPO_ROOT/pfm $real_run" "$T/registered.args"; then
  ok 'pfm.e2e is the profiled e2e wrapper, given the pfm dir and the run dir'
else bad 'pfm.e2e registration' "$(grep '^pfm.e2e' "$T/registered.args")"; fi
if grep -Fxq "pfm.race checks_pfm_race $REPO_ROOT/pfm $real_run" "$T/registered.args"; then
  ok 'pfm.race is the race step, given the pfm dir and the run dir'
else bad 'pfm.race registration' "$(grep '^pfm.race' "$T/registered.args")"; fi
if [ -s "$real_run/profile.tsv" ] && [ -f "$real_run/profile/INDEX.txt" ] && grep -Fq "PROFILE index $real_run/profile/INDEX.txt" "$T/pfm.out"; then
  ok 'the real profile-report.sh summary prints its PROFILE block and writes profile.tsv and profile/INDEX.txt'
else bad 'real summary' "$(cat "$T/pfm.out")"; fi

if gate_run templates >"$T/templates.out" &&
  [ "$(static_names)" = "$(printf '%s' "$templates_static" | tr ',' '\n' | sort | paste -sd, -)" ] &&
  [ "$(cat "$T/heavy")" = templates.check-map ] &&
  graph_order templates; then
  ok 'templates graph runs its tests before static checks'
else bad 'templates graph' "$(cat "$T/registered")"; fi
for n in check-pfm check-templates test-pfm test-templates unit-path; do
  if grep -Fxq "templates.$n-tests checks_templates_unit_script $n" "$T/registered.args"; then
    ok "$n suite is registered with its own name"
  else bad "$n suite registration" "$(grep 'checks_templates_unit_script' "$T/registered.args")"; fi
done

if grep -Fxq "templates.plugin-logs checks_templates_plugin_logs" "$T/registered.args"; then
  ok 'plugin-logs self-test is a named gate step'
else bad 'plugin-logs gate registration' "$(cat "$T/registered.args")"; fi
if grep -Fxq "templates.plugin-lab checks_templates_plugin_lab" "$T/registered.args"; then
  ok 'plugin-lab self-tests are a named gate step'
else bad 'plugin-lab gate registration' "$(cat "$T/registered.args")"; fi

# The pfm that pfm.unit prebuilds is kept in <run>/bin: check-map is handed it; the mirrors build engine-sync instead.
if PFM_TEST_TIMING_DIR="$T/reuse" gate_run all >"$T/reuse.out"; then
  reuse_run="$(ls -d "$T/reuse"/run.* | head -1)"
  if grep -Fxq "pfm.unit checks_pfm_unit $REPO_ROOT/pfm $reuse_run" "$T/registered.args" &&
    grep -Fxq "templates.check-map checks_gate_check_map $reuse_run" "$T/registered.args" &&
    grep -Fxq "templates.mirrors checks_templates_mirrors" "$T/registered.args" &&
    [ "$(static_names)" = "$(printf '%s' "$pfm_static,$templates_static" | tr ',' '\n' | sort | paste -sd, -)" ] && [ "$(sort "$T/heavy" | paste -sd, -)" = "$heavy_names" ]; then
    ok 'templates.mirrors is registered with no pfm and stays light; check-map and pfm.unit keep their arguments'
  else bad 'kept pfm registration' "$(grep -E '^(pfm.unit|templates.check-map|templates.mirrors) ' "$T/registered.args")"; fi
else bad 'gate_run all (kept pfm registration)' "$(cat "$T/reuse.out")"; fi
if [ -n "${reuse_run:-}" ] && [ "${TIMING_RUN_LAST:-}" = "$reuse_run" ]; then
  ok 'gate_run records the run dir it made for the RUN DIR line'
else bad 'gate_run run-dir record' "recorded: ${TIMING_RUN_LAST:-<none>}" "made: ${reuse_run:-<none>}"; fi

STUB_STEPS_RC=1
if ! gate_run all >"$T/red.out"; then
  ok 'red step makes gate_run red'
else bad 'red step was lost'; fi
unset STUB_STEPS_RC

cat > "$T/gate-budget.yml" <<'YAML'
tolerance: 1.25
fail_factor: 2
gate:
  pfm: 0
YAML
if ! gate_budget_verdict pfm 1 "$T/gate-budget.yml" >"$T/budget-red.out"; then
  ok 'red budget makes budget verdict non-zero'
else bad 'red budget was lost'; fi

# checks_gate_fixture, shell kinds: the real scripts under infra/fence/gate-fixtures, the real stepprof.sh.
info() { :; }
fail_step() { printf 'FAILSTEP %s\n' "$*"; FAILURES=$((FAILURES + 1)); }
FAILURES=0
checks_gate_fixture shell-fail "$T/fixture-run" >"$T/shell-fail.out" 2>&1
if [ "$FAILURES" -eq 1 ] && grep -Fxq 'FAILSTEP fixture: shell-fail red (expected)' "$T/shell-fail.out"; then
  ok 'fixture.shell-fail: the script dies and the step is red with its reason'
else bad 'shell-fail step' "failures $FAILURES" "$(cat "$T/shell-fail.out")"; fi
bash "$REPO_ROOT/infra/fence/stepprof.sh" "$T/sp-fail" fixture.shell-fail 30 -- bash "$REPO_ROOT/infra/fence/gate-fixtures/shell-fail.sh" >"$T/sp-fail.out" 2>&1; sp_rc=$?
first_err="$(grep -m1 '^ERR ' "$T/sp-fail/fixture.shell-fail.xtrace" 2>/dev/null)"
if [ "$sp_rc" -eq 1 ] && [[ "$first_err" =~ ^ERR\ rc=1\ [0-9.]+\ .*/shell-fail\.sh:[0-9]+\ innermost:\ false$ ]] &&
  grep -Eq '^  at .*/shell-fail\.sh:[0-9]+ middle$' "$T/sp-fail/fixture.shell-fail.xtrace"; then
  ok 'shell-fail.sh under stepprof: the first ERR record names shell-fail.sh, its line and the function, with the call stack'
else bad 'shell-fail ERR record' "rc $sp_rc" "$first_err" "$(cat "$T/sp-fail.out")"; fi
TMPDIR="$T" STEPPROF_GRACE_TICKS=5 bash "$REPO_ROOT/infra/fence/stepprof.sh" "$T/sp-hang" fixture.shell-hang 2 -- bash "$REPO_ROOT/infra/fence/gate-fixtures/shell-hang.sh" >"$T/sp-hang.out" 2>&1; sp_rc=$?
tree="$T/sp-hang/fixture.shell-hang.hang/tree.txt"
if [ "$sp_rc" -eq 124 ] && grep -Eq '^pid [0-9]+  state S cat  wchan wait_for_partner ' "$tree" &&
  grep -Eq "^  cmd: cat $T/gate-fixture-hang\.[A-Za-z0-9]{6}/fifo " "$tree" &&
  grep -Eq "^$T/gate-fixture-hang\.[A-Za-z0-9]{6}/fifo awaited by .*\(cat\)" "$tree"; then
  ok 'shell-hang.sh under a 2 s bound: the hang tree names cat in state S, wchan wait_for_partner, and the fifo it awaits'
else bad 'shell-hang tree' "rc $sp_rc" "$(cat "$tree" 2>&1)" "$(cat "$T/sp-hang.out")"; fi
if [ -z "$(find "$T" -maxdepth 1 -name 'gate-fixture-hang.*')" ]; then
  ok 'shell-hang.sh: the TERM at the bound removes its fifo directory'
else bad 'shell-hang cleanup' "$(find "$T" -maxdepth 1 -name 'gate-fixture-hang.*')"; fi

# gate_run against a fixture repo root, so the budget file it reads is the case's own.
mkdir -p "$T/fx/infra/fence/lanes/tests" "$T/fx/infra/demo/tests" "$T/fx/pfm/scripts"
: > "$T/fx/infra/fence/lanes/tests/a_test.sh"; : > "$T/fx/infra/demo/tests/b_test.sh"; : > "$T/fx/pfm/scripts/c_test.sh"
# The fixture's sampler, judge, report and load sampler: each logs to $STUB_EVENTS; a sampler
# logs its start only once its TERM trap is set, so a stop after it is always logged. One that
# cannot start ignores TERM, logs its -failed line, then exits 2: the gate's stop never decides its status.
cat > "$T/fx/infra/fence/sampler.sh" <<'SH'
if [ "${STUB_SAMPLER_FAIL:-0}" = 1 ]; then
  trap '' TERM
  printf 'sampler-failed %s\n' "$*" >> "$STUB_EVENTS"
  echo 'sampler: stub cannot start' >&2
  exit 2
fi
trap 'printf "sampler-stop\n" >> "$STUB_EVENTS"; exit 0' TERM
printf 'epoch_s\n' > "$3"
printf 'sampler-start %s\n' "$*" >> "$STUB_EVENTS"
while :; do sleep 0.1; done
SH
cat > "$T/fx/pfm/scripts/test-contention.sh" <<'SH'
printf 'judge %s\n' "$*" >> "$STUB_EVENTS"
[ "${STUB_JUDGE_RC:-0}" = 0 ] || { echo 'test-contention: malformed header' >&2; exit "$STUB_JUDGE_RC"; }
printf '%s\t%s\n' "${STUB_JUDGE_WORD:-CODE}" "${STUB_JUDGE_EVIDENCE:-spin ×1.0 (< ×1.5)}"
SH
cat > "$T/fx/infra/fence/profile-report.sh" <<'SH'
printf 'report %s\n' "$*" >> "$STUB_EVENTS"
case "$1" in
  summary)
    printf 'summary-saw %s\n' "$(tail -1 "$2/gate.tsv" | cut -f1)" >> "$STUB_EVENTS"
    [ "${STUB_SUMMARY_RC:-0}" = 0 ] || exit "$STUB_SUMMARY_RC"
    printf 'PROFILE index %s/profile/INDEX.txt\n' "$2" ;;
  failures) [ -z "${STUB_FAILURES_OUT:-}" ] || printf '%s\n' "$STUB_FAILURES_OUT"; exit "${STUB_FAILURES_RC:-0}" ;;
esac
SH
cat > "$T/fx/pfm/scripts/test-shard.sh" <<'SH'
if [ "${STUB_LOAD_FAIL:-0}" = 1 ]; then
  trap '' TERM
  printf 'load-failed %s\n' "$*" >> "$STUB_EVENTS"
  echo 'test-shard: stub load record cannot open' >&2
  exit 2
fi
trap 'printf "load-stop\n" >> "$STUB_EVENTS"; exit 0' TERM
printf 'load-start %s\n' "$*" >> "$STUB_EVENTS"
while :; do sleep 0.1; done
SH
fx_budget() { printf 'tolerance: 1.25\nfail_factor: 2\ngate:\n  all: 198\n  pfm: unpinned\n  templates: abc\n' > "$T/fx/infra/fence/gate-budget.yml"; }
fx_case() { # fx_case <name> <target>: gate_run in $T/case-<name>; sets GATE_RC, GATE_OUT and RUN
  local saved="$REPO_ROOT"
  REPO_ROOT="$T/fx"; PFM_TEST_TIMING_DIR="$T/case-$1"; GATE_OUT="$T/case-$1.out"
  gate_run "$2" >"$GATE_OUT" 2>&1; GATE_RC=$?
  REPO_ROOT="$saved"; unset PFM_TEST_TIMING_DIR
  RUN="$(ls -d "$T/case-$1"/run.* | head -1)"
}
meta_get() { awk -F '\t' -v k="$1" '$1==k {print substr($0, length(k) + 2)}' "$2"; }

# The fixture steps (PFM_GATE_FIXTURES=1): five broken steps close phase 0, before the barrier; every
# graph case above registers with the env unset and so holds none of them.
fixture_names='fixture.go-fail,fixture.go-hang,fixture.go-slow,fixture.shell-fail,fixture.shell-hang'
fixture_case() { # fixture_case <target> <last phase-0 step of that target>
  PFM_GATE_FIXTURES=1 fx_case "fixtures-$1" "$1"
  if [ "$(sed -n '/^BARRIER$/q;p' "$T/registered" | tail -6 | paste -sd, -)" = "$2,$fixture_names" ] &&
    [ "$(grep -B1 '^BARRIER$' "$T/registered" | head -1)" = fixture.shell-hang ] &&
    [ "$(grep -c '^fixture\.' "$T/registered")" -eq 5 ] &&
    [ "$(cat "$T/bounds")" = "$(printf 'fixture.go-slow 45\nfixture.shell-hang 5')" ] &&
    grep -Fxq "fixture.go-slow checks_gate_fixture go-slow $RUN" "$T/registered.args" &&
    grep -Fxq "fixture.shell-fail checks_gate_fixture shell-fail $RUN" "$T/registered.args"; then
    ok "PFM_GATE_FIXTURES=1 ($1): the five fixture steps follow $2, precede the barrier, carry their kind and run dir, and bound go-slow 45 and shell-hang 5"
  else bad "fixture registration ($1)" "$(cat "$T/registered")" "$(cat "$T/bounds")"; fi
}
fx_budget
fixture_case all templates.demo.b
fixture_case pfm pfm.self.c
fixture_case templates templates.demo.b

fx_budget
fx_case bound-all all
if [ "$GATE_RC" -eq 0 ] && [ "$(cat "$T/bound.seen")" = 494 ] && ! grep -q 'step bound' "$GATE_OUT" && [ -z "${STEPS_BOUND_S+x}" ]; then
  ok 'STEPS_BOUND_S unset: all gets its fail limit (198 x1.25 x2 = 494), and the shell is left as it was'
else bad 'derived gate bound' "seen $(cat "$T/bound.seen")" "$(cat "$GATE_OUT")"; fi
STEPS_BOUND_S=30 fx_case bound-caller all
if [ "$GATE_RC" -eq 0 ] && [ "$(cat "$T/bound.seen")" = 30 ] && ! grep -q 'step bound' "$GATE_OUT" &&
  [ "$(meta_get steps_bound_s "$RUN/gate.meta")" = 30 ]; then
  ok 'a caller-set STEPS_BOUND_S is kept and printed nowhere'
else bad 'caller bound' "seen $(cat "$T/bound.seen")" "$(cat "$GATE_OUT")"; fi

fx_bound_fallback() { # fx_bound_fallback <name> <target> <expected reason>
  fx_case "$1" "$2"
  if [ "$(cat "$T/bound.seen")" = 1500 ] && grep -Fxq "gate: step bound 1500 s — $3" "$GATE_OUT" && [ "$(meta_get steps_bound_s "$RUN/gate.meta")" = 1500 ]; then
    ok "a budget that gives no bound falls back to 1500 and says why ($1)"
  else bad "bound fallback $1" "seen $(cat "$T/bound.seen")" "$(cat "$GATE_OUT")"; fi
}
fx_bound_fallback bound-unpinned pfm 'gate(pfm) is unpinned in gate-budget.yml'
fx_bound_fallback bound-invalid templates "invalid budget abc in $T/fx/infra/fence/gate-budget.yml"
printf 'tolerance: 1.25\nfail_factor: 2\ngate:\n  pfm: 10\n' > "$T/fx/infra/fence/gate-budget.yml"
fx_bound_fallback bound-norow templates 'no row for gate(templates) in gate-budget.yml'
rm "$T/fx/infra/fence/gate-budget.yml"
fx_bound_fallback bound-nofile all "budget file unreadable: $T/fx/infra/fence/gate-budget.yml"
if [ "$(tail -1 "$RUN/gate.tsv")" = "$(printf 'BUDGET\tERROR\t1')" ] && [ "$GATE_RC" -ne 0 ]; then
  ok 'an unreadable budget file is a BUDGET ERROR row and a red gate'
else bad 'BUDGET ERROR row' "$(cat "$RUN/gate.tsv")"; fi

fx_budget
fx_case row-pass all
if [ "$GATE_RC" -eq 0 ] && [ "$(cat "$RUN/gate.tsv")" = "$(printf 'step\tverdict\tseconds\nSTEPS\tPASS\t0.1\nBUDGET\tPASS\t1')" ]; then
  ok 'gate.tsv gains BUDGET PASS with the wall rounded up after the STEPS row'
else bad 'BUDGET PASS row' "$(cat "$RUN/gate.tsv")"; fi
fx_case row-unpinned pfm
if [ "$GATE_RC" -eq 0 ] && [ "$(tail -1 "$RUN/gate.tsv")" = "$(printf 'BUDGET\tUNPINNED\t1')" ]; then ok 'BUDGET UNPINNED row'; else bad 'BUDGET UNPINNED row' "$(cat "$RUN/gate.tsv")"; fi
STUB_WALL=300 fx_case row-warn all
if [ "$GATE_RC" -eq 0 ] && [ "$(tail -1 "$RUN/gate.tsv")" = "$(printf 'BUDGET\tWARN\t300')" ]; then ok 'BUDGET WARN row, gate still green'; else bad 'BUDGET WARN row' "$(cat "$RUN/gate.tsv")"; fi
STUB_WALL=500.2 fx_case row-fail all
if [ "$GATE_RC" -ne 0 ] && [ "$(tail -1 "$RUN/gate.tsv")" = "$(printf 'BUDGET\tFAIL\t501')" ]; then ok 'BUDGET FAIL row, gate red'; else bad 'BUDGET FAIL row' "$(cat "$RUN/gate.tsv")"; fi
STUB_NO_STEPS=1 fx_case row-nosteps all
if [ "$GATE_RC" -ne 0 ] && grep -q 'no wall time read (got nothing); the gate table is missing or has no STEPS row' "$GATE_OUT" &&
  [ "$(tail -1 "$RUN/gate.tsv")" = "$(printf 'BUDGET\tERROR\tNA')" ]; then
  ok 'gate.tsv without a STEPS row: the red line names STEPS and BUDGET is ERROR NA'
else bad 'no STEPS row' "$(cat "$GATE_OUT")" "$(cat "$RUN/gate.tsv")"; fi
STUB_STEPS_RC=1 fx_case row-stepsred all
if [ "$GATE_RC" -ne 0 ] && [ "$(tail -1 "$RUN/gate.tsv")" = "$(printf 'BUDGET\tPASS\t1')" ]; then
  ok 'red steps: the BUDGET row still records its own verdict'
else bad 'red steps with a budget row' "$(cat "$RUN/gate.tsv")"; fi

PFM_GATE_FIXTURES=1 fx_case meta all
ts='^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$'
if [ "$(cut -f1 "$RUN/gate.meta" | paste -sd, -)" = 'target,commit,dirty_files,started,finished,fixtures,steps_bound_s,steps_jobs,steps_heavy_jobs' ] &&
  [ "$(meta_get target "$RUN/gate.meta")" = all ] && [ "$(meta_get commit "$RUN/gate.meta")" = abcdef012345 ] &&
  [ "$(meta_get dirty_files "$RUN/gate.meta")" = 3 ] && [ "$(meta_get fixtures "$RUN/gate.meta")" = 1 ] &&
  [ "$(meta_get steps_bound_s "$RUN/gate.meta")" = 494 ] && [ "$(meta_get steps_jobs "$RUN/gate.meta")" = 6 ] &&
  [ "$(meta_get steps_heavy_jobs "$RUN/gate.meta")" = 2 ] &&
  [[ "$(meta_get started "$RUN/gate.meta")" =~ $ts ]] && [[ "$(meta_get finished "$RUN/gate.meta")" =~ $ts ]]; then
  ok 'gate.meta records what ran: target, commit, dirty files, UTC start and finish, fixtures, bound, job caps'
else bad 'gate.meta' "$(cat "$RUN/gate.meta")"; fi
if [ "$(meta_get finished "$T/meta.during")" = 'NA running' ] && [ "$(meta_get started "$T/meta.during")" = "$(meta_get started "$RUN/gate.meta")" ]; then
  ok 'gate.meta exists before the steps run, finished NA running'
else bad 'gate.meta during the run' "$(cat "$T/meta.during")"; fi
fx_case meta-plain all
if [ "$(meta_get fixtures "$RUN/gate.meta")" = 0 ]; then ok 'fixtures is 0 without PFM_GATE_FIXTURES=1'; else bad 'fixtures 0' "$(cat "$RUN/gate.meta")"; fi
if (
    eval "orig_$(declare -f gate_budget_verdict)"
    gate_budget_verdict() { cp "$PFM_TEST_TIMING_DIR"/run.*/gate.meta "$T/meta.at-budget"; orig_gate_budget_verdict "$@"; }
    fx_case meta-order all
    cp "$RUN/gate.meta" "$T/meta.final"
  ) && [ "$(meta_get finished "$T/meta.at-budget")" = 'NA running' ] && [[ "$(meta_get finished "$T/meta.final")" =~ $ts ]]; then
  ok 'finished is written after the budget verdict'
else bad 'gate.meta finished order' "$(cat "$T/meta.at-budget")"; fi
repo_git_good="$(declare -f repo_git)"
repo_git() { printf 'fatal: not a git repository\n' >&2; return 128; }
fx_case meta-nogit all
eval "$repo_git_good"
if [ "$(meta_get commit "$RUN/gate.meta")" = 'NA git rev-parse failed: fatal: not a git repository' ] &&
  [ "$(meta_get dirty_files "$RUN/gate.meta")" = 'NA git status failed: fatal: not a git repository' ]; then
  ok 'unreadable git state is NA with its reason, never a made-up commit'
else bad 'gate.meta without git' "$(cat "$RUN/gate.meta")"; fi

# The resource sampler spans steps_run and stops before the judge; the summary follows BUDGET.
fx_budget
: > "$STUB_EVENTS"
fx_case order all
if [ "$GATE_RC" -eq 0 ] && [ "$(event_words)" = 'sampler-start steps sampler-stop judge report summary-saw' ] &&
  grep -Fxq "sampler-start run --out $RUN/resources.tsv --parent $$" "$STUB_EVENTS" &&
  grep -Fxq "judge window --resources $RUN/resources.tsv" "$STUB_EVENTS" &&
  grep -Fxq "report summary $RUN" "$STUB_EVENTS" && grep -Fxq 'summary-saw BUDGET' "$STUB_EVENTS" &&
  grep -Fxq "PROFILE index $RUN/profile/INDEX.txt" "$GATE_OUT"; then
  ok 'gate_run: sampler started before the steps and stopped right after; judge, BUDGET, then the PROFILE summary'
else bad 'gate_run sampler/judge/summary order' "rc $GATE_RC, this shell $$" "$(cat "$STUB_EVENTS")" "$(cat "$GATE_OUT")"; fi
if grep -Fxq 'budget: ✓ gate(all) — 1s within 247s (budget 198s ×1.25)' "$GATE_OUT"; then
  ok 'a wall within its limit keeps the budget line as it was, no attribution'
else bad 'within-limit line' "$(cat "$GATE_OUT")"; fi

STUB_WALL=300 fx_case attr-warn all
if [ "$GATE_RC" -eq 0 ] && grep -Fxq 'budget: ⚠ gate(all) — 300s over limit 247s; fails past 494s (budget 198s ×1.25 ×2) · attribution CODE (spin ×1.0 (< ×1.5))' "$GATE_OUT"; then
  ok 'gate_run: an over-limit wall carries the judge attribution'
else bad 'gate_run attribution on warn' "$(cat "$GATE_OUT")"; fi
export STUB_JUDGE_WORD=CONTENTION STUB_JUDGE_EVIDENCE='tick-deficit -15.5% (≤ -5%)'
STUB_WALL=500.2 fx_case attr-contention all
unset STUB_JUDGE_WORD STUB_JUDGE_EVIDENCE
if [ "$GATE_RC" -eq 0 ] && [ "$(tail -1 "$RUN/gate.tsv")" = "$(printf 'BUDGET\tWARN\t501')" ] &&
  grep -Fxq 'budget: ⚠ gate(all) — 501s over the fail limit 494s (limit 247s ×2) · attribution CONTENTION (tick-deficit -15.5% (≤ -5%)) · downgraded from FAIL' "$GATE_OUT"; then
  ok 'gate_run: past the fail limit under CONTENTION is a WARN row and a green gate'
else bad 'gate_run contention downgrade' "$(cat "$GATE_OUT")" "$(cat "$RUN/gate.tsv")"; fi
export STUB_JUDGE_RC=2
STUB_WALL=500.2 fx_case attr-judge-failed all
unset STUB_JUDGE_RC
if [ "$GATE_RC" -ne 0 ] && [ "$(tail -1 "$RUN/gate.tsv")" = "$(printf 'BUDGET\tFAIL\t501')" ] &&
  grep -Fxq 'budget: ✗ gate(all) — 501s over the fail limit 494s (limit 247s ×2) · attribution not measured (judge failed: test-contention: malformed header)' "$GATE_OUT"; then
  ok 'gate_run: a judge that fails is not measured, never a downgrade'
else bad 'gate_run judge failure' "$(cat "$GATE_OUT")" "$(cat "$RUN/gate.tsv")"; fi

export STUB_SAMPLER_FAIL=1
fx_case sampler-fail all
unset STUB_SAMPLER_FAIL
if [ "$GATE_RC" -eq 0 ] && grep -Fxq 'PROFILE resources NOT RECORDED — sampler.sh exit 2 (its stderr above)' "$GATE_OUT" &&
  [ "$(tail -1 "$RUN/gate.tsv")" = "$(printf 'BUDGET\tPASS\t1')" ]; then
  ok 'a sampler that cannot start is named and the gate goes on'
else bad 'sampler start failure' "$(cat "$GATE_OUT")"; fi
if (
    bad() { printf 'RED %s\n' "$*"; }
    export STUB_SUMMARY_RC=3
    fx_case summary-fail all
    [ "$GATE_RC" -eq 0 ] && grep -Fq "RED PROFILE summary FAILED — profile-report.sh summary exit 3" "$GATE_OUT"
  ); then
  ok 'a summary that fails is one red line naming its exit, the gate verdict unchanged'
else bad 'summary failure' "$(cat "$T/case-summary-fail.out")"; fi

gate_budget_verdict() { printf 'budget: forced red\n'; return 1; }
PFM_TEST_TIMING_DIR="$T/case-forced"
# Red for the budget's reason only: an early return elsewhere in gate_run is red too, and leaves no gate.tsv.
if ! gate_run all >"$T/gate-budget-red.out" && grep -Fxq 'budget: forced red' "$T/gate-budget-red.out"; then
  ok 'red budget makes gate_run red'
else bad 'gate_run lost red budget, or went red before its budget verdict' "$(cat "$T/gate-budget-red.out")"; fi
forced_tsv="$(ls -d "$PFM_TEST_TIMING_DIR"/run.* | head -1)/gate.tsv"
unset PFM_TEST_TIMING_DIR
if [ "$(tail -1 "$forced_tsv" | cut -f1,2)" = "$(printf 'BUDGET\tERROR')" ]; then
  ok 'a verdict function that never says its verdict is a BUDGET ERROR row'
else bad 'BUDGET row without a verdict' "$(cat "$forced_tsv")" "$(ls -A "${forced_tsv%/gate.tsv}")" "$(cat "$T/gate-budget-red.out")"; fi

PFM_TEST_TIMING_DIR="$T/history"
mkdir -p "$PFM_TEST_TIMING_DIR"/{run.old,run.new,run.invalid,run.current}
printf '%s\n' '{"Test":"TestOld","Action":"pass"}' > "$PFM_TEST_TIMING_DIR/run.old/unit.json"
printf '%s\n' '{"Test":"TestNew","Action":"pass"}' > "$PFM_TEST_TIMING_DIR/run.new/unit.json"
printf '%s\n' '{"Action":"pass"}' > "$PFM_TEST_TIMING_DIR/run.invalid/unit.json"
touch -t 202001010000 "$PFM_TEST_TIMING_DIR/run.old/unit.json"
touch -t 202101010000 "$PFM_TEST_TIMING_DIR/run.new/unit.json"
touch -t 202201010000 "$PFM_TEST_TIMING_DIR/run.invalid/unit.json"
make() { printf '%s\n' '-race'; }
run() { printf '%s %s\n' "${PFM_TEST_ARTIFACT_DIR:-unset}" "$*" >> "$T/unit.calls"; }
go_test_report() {
  printf 'go_test_report %s\n' "$1" >> "$STUB_EVENTS"
  [ "${STUB_REPORT_FAILS:-0}" = 0 ] || FAILURES=$((FAILURES + 1))
}
skip_gate() { :; }
REPO_ROOT="$T/fx"
cur="$PFM_TEST_TIMING_DIR/run.current"
: > "$STUB_EVENTS"
export STUB_FAILURES_OUT="  PROFILE github.com/rezzminator/professor/pfm/internal/pricing: $cur/profile/internal_pricing.42/exit/DIAGNOSIS.txt"
checks_pfm_unit "$T/fx/pfm" "$cur" >"$T/unit.out"
unset STUB_FAILURES_OUT
if grep -Fq "test-shard.sh run --out $cur/unit.json --bin-dir $cur/bin --history $PFM_TEST_TIMING_DIR/run.new/unit.json -- -race" "$T/unit.calls"; then
  ok 'unit row uses sharder with newest usable history and TESTFLAGS, and keeps its prebuilt binaries in <run>/bin'
else bad 'unit row or history selection' "$(cat "$T/unit.calls")"; fi
if grep -Fq "$cur/profile pfm: go test -- bash $T/fx/pfm/scripts/test-shard.sh run --out" "$T/unit.calls" && [ -z "${PFM_TEST_ARTIFACT_DIR+x}" ]; then
  ok 'the unit sharder runs with PFM_TEST_ARTIFACT_DIR=<run>/profile, scoped to the row'
else bad 'unit artifact dir' "$(cat "$T/unit.calls")"; fi
if [ "$(cat "$STUB_EVENTS")" = "$(printf 'go_test_report %s\nreport failures %s %s' "$cur/unit.json" "$cur/unit.json" "$cur/profile")" ] &&
  grep -Fxq "  PROFILE github.com/rezzminator/professor/pfm/internal/pricing: $cur/profile/internal_pricing.42/exit/DIAGNOSIS.txt" "$T/unit.out"; then
  ok 'a failing unit package gets its profile pointer right after the go_test_report block'
else bad 'unit failure pointer' "$(cat "$STUB_EVENTS")" "$(cat "$T/unit.out")"; fi
STUB_REPORT_FAILS=1 checks_pfm_unit "$T/fx/pfm" "$cur" >"$T/unit-noevents.out"
if grep -Fxq "  PROFILE index $cur/profile/" "$T/unit-noevents.out"; then
  ok 'a failed unit run whose stream names no failing package still points at the profile root'
else bad 'unit index pointer' "$(cat "$T/unit-noevents.out")"; fi
export STUB_FAILURES_RC=2
checks_pfm_unit "$T/fx/pfm" "$cur" >"$T/unit-report-failed.out"
unset STUB_FAILURES_RC
if grep -Fxq '  PROFILE failures NOT REPORTED — profile-report.sh failures exit 2' "$T/unit-report-failed.out" &&
  grep -Fxq "  PROFILE index $cur/profile/" "$T/unit-report-failed.out"; then
  ok 'a failures report that cannot be built is named and still points at the profile root'
else bad 'failures report error' "$(cat "$T/unit-report-failed.out")"; fi

# checks_gate_fixture, Go kinds: the fixture package run through a stubbed go, then the shared report
# and the failure pointers on the stream it wrote.
go() {
  printf 'go %s | PFM_PROFILE_FIXTURE=%s SLOW_S=%s ARTIFACT=%s\n' "$*" "${PFM_PROFILE_FIXTURE-unset}" "${PFM_PROFILE_FIXTURE_SLOW_S-unset}" "${PFM_TEST_ARTIFACT_DIR-unset}" >> "$T/go.calls"
  printf '{"Action":"fail","Package":"stub"}\n'
  return "${STUB_GO_RC:-1}"
}
fixture_go_case() { # fixture_go_case <step kind> <PFM_PROFILE_FIXTURE> <-timeout> <expected PFM_PROFILE_FIXTURE_SLOW_S>
  local rd="$T/fixture-run-$1" json="$T/fixture-run-$1/fixture-$1.json" root="$T/fixture-run-$1/profile/fixture-$1"
  mkdir -p "$rd"; : > "$STUB_EVENTS"; : > "$T/go.calls"; FAILURES=0
  export STUB_FAILURES_OUT="  PROFILE github.com/rezzminator/professor/pfm/internal/testjail/testdata/profilefixture: $root/internal_testjail_testdata_profilefixture.9/exit/DIAGNOSIS.txt"
  checks_gate_fixture "$1" "$rd" >"$T/fixture-$1.out" 2>&1
  unset STUB_FAILURES_OUT
  if [ "$(cat "$T/go.calls")" = "go -C $T/fx/pfm test -count=1 -json -timeout $3 ./internal/testjail/testdata/profilefixture/ | PFM_PROFILE_FIXTURE=$2 SLOW_S=$4 ARTIFACT=$root" ] &&
    [ "$(cat "$json")" = '{"Action":"fail","Package":"stub"}' ] && [ "$FAILURES" -eq 1 ] &&
    [ "$(cat "$STUB_EVENTS")" = "$(printf 'go_test_report %s\nreport failures %s %s' "$json" "$json" "$root")" ] &&
    [ "$(grep -v '^  PROFILE ' "$T/fixture-$1.out")" = "FAILSTEP fixture: go $2 red (expected)" ] &&
    grep -Fxq "  PROFILE github.com/rezzminator/professor/pfm/internal/testjail/testdata/profilefixture: $root/internal_testjail_testdata_profilefixture.9/exit/DIAGNOSIS.txt" "$T/fixture-$1.out" &&
    [ -z "${PFM_PROFILE_FIXTURE+x}${PFM_PROFILE_FIXTURE_SLOW_S+x}${PFM_TEST_ARTIFACT_DIR+x}" ]; then
    ok "fixture.$1: go test -timeout $3 on the profile fixture in mode $2 profiling into profile/fixture-$1, its stream kept in the run dir, red, then the report and the failure pointers over that root"
  else bad "fixture.$1" "$(cat "$T/go.calls")" "$(cat "$STUB_EVENTS")" "$(cat "$T/fixture-$1.out")"; fi
}
fixture_go_case go-fail fail 2m unset
fixture_go_case go-hang hang 20s unset
fixture_go_case go-slow slow 5m 120
checks_gate_fixture no-such-kind "$T/fixture-run" >"$T/fixture-unknown.out" 2>&1
if grep -Fxq "FAILSTEP fixture: unknown kind 'no-such-kind'" "$T/fixture-unknown.out"; then
  ok 'an unknown fixture kind is a named red step, never a silent pass'
else bad 'unknown fixture kind' "$(cat "$T/fixture-unknown.out")"; fi

# checks_pfm_e2e: the dev.sh rows (stubbed) run inside the e2e.load span with the artifact dir.
pfm_e2e_rows() {
  wait_event "load-start sample --out $2/e2e.load" "load-failed sample --out $2/e2e.load"
  printf 'rows %s %s %s\n' "$1" "$2" "${PFM_TEST_ARTIFACT_DIR:-unset}" >> "$STUB_EVENTS"
}
: > "$STUB_EVENTS"
export STUB_FAILURES_OUT="  PROFILE github.com/rezzminator/professor/pfm/e2e: no bundle — $cur/profile/e2e.7/summary.json (event exit, exit 1)"
checks_pfm_e2e "$T/fx/pfm" "$cur" >"$T/e2e.out"
unset STUB_FAILURES_OUT
if [ "$(event_words)" = 'load-start rows load-stop report' ] &&
  grep -Fxq "load-start sample --out $cur/e2e.load" "$STUB_EVENTS" &&
  grep -Fxq "rows $T/fx/pfm $cur $cur/profile" "$STUB_EVENTS" &&
  grep -Fxq "report failures $cur/e2e.json $cur/profile" "$STUB_EVENTS" &&
  grep -Fxq "  PROFILE github.com/rezzminator/professor/pfm/e2e: no bundle — $cur/profile/e2e.7/summary.json (event exit, exit 1)" "$T/e2e.out"; then
  ok 'e2e: load sampled over the rows, profiled into <run>/profile, failure pointers for e2e.json'
else bad 'e2e wrapper' "$(cat "$STUB_EVENTS")" "$(cat "$T/e2e.out")"; fi
: > "$STUB_EVENTS"
export STUB_LOAD_FAIL=1
checks_pfm_e2e "$T/fx/pfm" "$cur" >"$T/e2e-noload.out" 2>&1
unset STUB_LOAD_FAIL
if grep -q '^rows ' "$STUB_EVENTS" && grep -Eq '^  PROFILE e2e.load NOT RECORDED — test-shard.sh sample exit 2 \(its stderr above\)$' "$T/e2e-noload.out"; then
  ok 'a load sampler that cannot start is one line, and the e2e rows still run'
else bad 'e2e load failure' "$(cat "$STUB_EVENTS")" "$(cat "$T/e2e-noload.out")"; fi

# checks_pfm_race: the named harvester race tests under cgo's -race, their stream kept in the run dir and
# profiled into its own root <run>/profile/race (never pfm.unit's processes of the same packages), then a pass event demanded of every named test: a renamed, skipped or
# unbuilt test is a red step naming it, never a green "no tests to run".
race_pass() { printf '{"Action":"pass","Package":"github.com/rezzminator/professor/pfm/%s","Test":"%s"}\n' "$1" "$2"; }
# The cases read the list checks.sh holds, so a test joining it changes no case here; the chaos suites
# must be on it (the wrong-package and missing cases below name them).
race_all="$CHECKS_PFM_RACE_TESTS"
race_n="$(wc -l <<< "$race_all" | tr -d ' ')"
race_regex="^($(cut -d' ' -f2 <<< "$race_all" | paste -sd'|' -))\$"
race_pkgs="$(cut -d' ' -f1 <<< "$race_all" | sort -u | sed 's|.*|./&/|' | paste -sd' ' -)"
race_case() { # race_case <label> <expected FAILSTEP line or empty> <stream: "pkg test" pass lines, or ABSENT>
  local label="$1" want="$2" stream="$3" rd="$T/race-run" pkg test want_failures=0
  [ -z "$want" ] || want_failures=1
  rm -rf "$rd"; mkdir -p "$rd"; : > "$STUB_EVENTS"; : > "$T/unit.calls"; FAILURES=0
  if [ "$stream" != ABSENT ]; then
    while read -r pkg test; do race_pass "$pkg" "$test"; done <<< "$stream" > "$rd/race.json"
  fi
  checks_pfm_race "$T/fx/pfm" "$rd" >"$T/race.out" 2>&1
  if grep -Fq "$rd/profile/race pfm: go test -race (harvester chaos and race regressions) -- bash -c" "$T/unit.calls" &&
    grep -Fq 'CGO_ENABLED=1 go -C "$1" test -race -count=1 -json -timeout 10m -run "$2" "${@:4}" > "$3"' "$T/unit.calls" &&
    grep -Fq "_ $T/fx/pfm $race_regex $rd/race.json $race_pkgs" "$T/unit.calls" &&
    [ "$(cat "$STUB_EVENTS")" = "$(printf 'go_test_report %s\nreport failures %s %s' "$rd/race.json" "$rd/race.json" "$rd/profile/race")" ] &&
    [ "$(grep -v '^  PROFILE ' "$T/race.out")" = "$want" ] && [ -z "${PFM_TEST_ARTIFACT_DIR+x}" ] &&
    [ "$FAILURES" -eq "$want_failures" ]; then
    ok "pfm.race: $label"
  else bad "pfm.race: $label" "$(cat "$T/unit.calls")" "$(cat "$STUB_EVENTS")" "$(cat "$T/race.out")" "FAILURES=$FAILURES"; fi
}
race_case 'every named test passed under -race with cgo on: green' '' "$race_all"
race_case 'a named test with no pass event (skipped, renamed, never built) is red by name' \
  "FAILSTEP pfm: race — 1 of $race_n named test(s) never passed: internal/harvestmcp TestServiceChaos" \
  "$(grep -v TestServiceChaos <<< "$race_all")"
race_case 'a pass in the wrong package does not stand in for the named one' \
  "FAILSTEP pfm: race — 1 of $race_n named test(s) never passed: internal/harvest TestHarvesterChaos" \
  "$(sed 's|^internal/harvest TestHarvesterChaos$|internal/harvestmcp TestHarvesterChaos|' <<< "$race_all")"
race_case 'no stream at all: every named test is named as never passed' \
  "FAILSTEP pfm: race — $race_n of $race_n named test(s) never passed: $(paste -sd, - <<< "$race_all" | sed 's/,/, /g')" ABSENT
# No C compiler (an image built without gcc): the step is red as a missing toolchain and runs no go test,
# never a "never passed" list that reads like a renamed suite.
: > "$STUB_EVENTS"; : > "$T/unit.calls"; : > "$T/need.calls"; FAILURES=0
(
  need_tool() { printf '%s\n' "$*" >> "$T/need.calls"; [ "$1" != gcc ] || { fail_step "$2: TOOLCHAIN-MISSING — '$1' not on PATH"; return 1; }; }
  checks_pfm_race "$T/fx/pfm" "$T/race-run" >"$T/race-nogcc.out" 2>&1
  printf 'FAILURES=%s\n' "$FAILURES" >> "$T/race-nogcc.out"
)
if grep -Fxq 'gcc pfm' "$T/need.calls" && [ ! -s "$T/unit.calls" ] && [ ! -s "$STUB_EVENTS" ] &&
  [ "$(cat "$T/race-nogcc.out")" = "$(printf "FAILSTEP pfm: TOOLCHAIN-MISSING — 'gcc' not on PATH\nFAILURES=1")" ]; then
  ok 'pfm.race: no C compiler is a missing toolchain by name, and no go test runs'
else bad 'pfm.race: no gcc' "$(cat "$T/need.calls")" "$(cat "$T/unit.calls")" "$(cat "$STUB_EVENTS")" "$(cat "$T/race-nogcc.out")"; fi

# checks_pfm_test (iso test pfm): unit and e2e profiled into the one run dir it makes.
: > "$STUB_EVENTS"; : > "$T/unit.calls"
PFM_TEST_TIMING_DIR="$T/serial" checks_pfm_test "$T/fx/pfm" >"$T/serial.out"
serial_run="$(ls -d "$T/serial"/run.* | head -1)"
if grep -Fq "$serial_run/profile pfm: go test -- bash $T/fx/pfm/scripts/test-shard.sh run --out $serial_run/unit.json" "$T/unit.calls" &&
  grep -Fxq "rows $T/fx/pfm $serial_run $serial_run/profile" "$STUB_EVENTS"; then
  ok 'iso test pfm profiles its unit and e2e rows into its run dir'
else bad 'serial test rows' "$(cat "$T/unit.calls")" "$(cat "$STUB_EVENTS")"; fi
if [ "${TIMING_RUN_LAST:-}" = "$serial_run" ]; then
  ok 'checks_pfm_test records the run dir it made for the RUN DIR line'
else bad 'checks_pfm_test run-dir record' "recorded: ${TIMING_RUN_LAST:-<none>}" "made: $serial_run"; fi

# checks_gate_check_map: the pfm pfm.unit kept in <run>/bin when it is there, a build of its own when it is not.
head_() { :; }
cat > "$T/prebuilt-pfm" <<'SH'
#!/bin/sh
printf '%s\n' "$0 $*" >> "$MIRROR_CALLS"
SH
chmod +x "$T/prebuilt-pfm"
export MIRROR_CALLS="$T/mirror.calls"
cm_reuse="$T/cm-reuse"; mkdir -p "$cm_reuse/bin"; cp "$T/prebuilt-pfm" "$cm_reuse/bin/pfm"
: > "$T/unit.calls"; : > "$T/go.calls"; FAILURES=0
STUB_GO_RC=0 checks_gate_check_map "$cm_reuse" >"$T/cm-reuse.out" 2>&1
if [ ! -s "$T/go.calls" ] && [ "$FAILURES" -eq 0 ] &&
  [ "$(cat "$T/unit.calls")" = "unset templates: lane↔command map (check-map) -- bash infra/fence/lanes/check-map.sh --pfm $cm_reuse/bin/pfm" ]; then
  ok 'check-map reuses <run>/bin/pfm and builds nothing'
else bad 'check-map reuse' "$(cat "$T/go.calls")" "$(cat "$T/unit.calls")" "$(cat "$T/cm-reuse.out")"; fi
cm_build="$T/cm-build"; mkdir -p "$cm_build"
: > "$T/unit.calls"; : > "$T/go.calls"; FAILURES=0
STUB_GO_RC=0 checks_gate_check_map "$cm_build" >"$T/cm-build.out" 2>&1
if [ "$(sed 's/ |.*//' "$T/go.calls")" = "go -C pfm build -o $cm_build/check-map-pfm ./cmd/pfm" ] && [ "$FAILURES" -eq 0 ] &&
  [ "$(cat "$T/unit.calls")" = "unset templates: lane↔command map (check-map) -- bash infra/fence/lanes/check-map.sh --pfm $cm_build/check-map-pfm" ]; then
  ok 'check-map without <run>/bin/pfm builds check-map-pfm as before'
else bad 'check-map build' "$(cat "$T/go.calls")" "$(cat "$T/unit.calls")" "$(cat "$T/cm-build.out")"; fi
: > "$T/unit.calls"; : > "$T/go.calls"; FAILURES=0
STUB_GO_RC=1 checks_gate_check_map "$cm_build" >"$T/cm-build-fail.out" 2>&1
if [ "$FAILURES" -eq 1 ] && [ ! -s "$T/unit.calls" ] && grep -Fxq 'FAILSTEP templates: check-map pfm build FAILED' "$T/cm-build-fail.out"; then
  ok 'a failed check-map pfm build is one red step and no check-map run'
else bad 'check-map build failure' "$(cat "$T/unit.calls")" "$(cat "$T/cm-build-fail.out")"; fi

mkdir -p "$REPO_ROOT/templates/global/engine-sync"
printf '%s\n' '{"version":1,"codex":{"modelMap":{"smart":"fixture-model"}}}' > "$REPO_ROOT/templates/global/engine-sync/config.json"
# checks_templates_mirrors_generate and _opencode: engine-sync, built from the mounted source onto
# $TMP_BASE/engine-sync/engine-sync, checks and builds the mirrors; a failed build is its own red line.
cat > "$T/es-stub" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$MIRROR_CALLS"
if [ -f "${XDG_CONFIG_HOME:-$HOME/.config}/engine-sync/config.json" ]; then
  cat "${XDG_CONFIG_HOME:-$HOME/.config}/engine-sync/config.json" > "$MIRROR_CALLS.policy"
else printf 'missing\n' > "$MIRROR_CALLS.policy"; fi
case "$1" in
  check) [ "${STUB_ES_CHECK_RC:-0}" -eq 0 ] || echo "STALE AGENTS.md"; exit "${STUB_ES_CHECK_RC:-0}" ;;
  build) exit "${STUB_ES_BUILD_RC:-0}" ;;
  global) # renders each name in STUB_ES_RENDER into the --home it is given, as the real binary does
    while [ $# -gt 0 ]; do [ "$1" = --home ] && home="$2"; shift; done
    printf 'HOME=%s XDG_CONFIG_HOME=%s XDG_STATE_HOME=%s XDG_DATA_HOME=%s ENGINE_SYNC_HOME=%s ENGINE_SYNC_STATE=%s CODEX_HOME=%s\n' \
      "$HOME" "${XDG_CONFIG_HOME-unset}" "${XDG_STATE_HOME-unset}" "${XDG_DATA_HOME-unset}" \
      "${ENGINE_SYNC_HOME-unset}" "${ENGINE_SYNC_STATE-unset}" "${CODEX_HOME-unset}" > "$MIRROR_CALLS.env"
    if [ -f "$home/.config/engine-sync/config.json" ]; then
      cat "$home/.config/engine-sync/config.json" > "$MIRROR_CALLS.policy"
    else printf 'missing\n' > "$MIRROR_CALLS.policy"; fi
    if [ "${STUB_ES_GLOBAL_RC:-0}" -ne 0 ]; then
      echo 'engine-sync global: beta-max.md: variant "beta-max" from beta.md: "replace" text "ghost" occurs 0 times in the body, want exactly 1'
      exit "$STUB_ES_GLOBAL_RC"
    fi
    mkdir -p "$home/.claude/agents" "$home/.codex/agents"
    if [ "${STUB_ES_DRIFT:-0}" -ne 0 ]; then # exit 0 with a named drift: the check must still go red
      echo 'SECTION-DRIFT beta-max: beta.md § Budget changed since pinned (pin sha256:a, now sha256:b) — review the override, then run engine-sync variants pin beta-max'
    fi
    if [ "${STUB_ES_UNKNOWN:-0}" -ne 0 ]; then # exit 0 with the OpenCode renderer's warning for a server it cannot grant
      for tool in mcp__sub-agent-compact__compact mcp__sub-agent-compact__compact mcp__nosuch__x; do
        echo "skip    unknown MCP server in tool $tool in $home/agents/beta.md — no configured or project .mcp.json server serves it; it stays denied"
      done
    fi
    if [ "${STUB_ES_UNMAPPED:-0}" -ne 0 ]; then # exit 0 with the OpenCode renderer's warning for a model alias its modelMap lacks
      echo "warn    unmapped OpenCode model alias \"claude-sonnet-5-5\" in $home/agents/beta.md — model omitted"
    fi
    for n in $STUB_ES_RENDER; do
      printf 'x\n' > "$home/gen-$n.md"; printf 'x\n' > "$home/.codex/agents/$n.toml"; ln -s "$home/gen-$n.md" "$home/.claude/agents/$n.md"
    done
    for n in $STUB_ES_CLAUDE_ONLY; do # a derived agent whose engines block turns Codex off: a Claude link and no Codex role
      printf -- '---\nengines:\n  codex: false\n---\n' > "$home/gen-$n.md"; ln -s "$home/gen-$n.md" "$home/.claude/agents/$n.md"
    done ;;
  *) exit "${STUB_ES_OPENCODE_RC:-0}" ;;
esac
SH
chmod +x "$T/es-stub"
es_bin="$TMP_BASE/engine-sync/engine-sync"
es_case() { # es_case <function> [VAR=value…]: run it with engine_sync_build stubbed; output in $T/es.out
  local fn="$1"; shift
  : > "$MIRROR_CALLS"; : > "$T/es-build.calls"; : > "$T/es-stage.calls"; : > "$MIRROR_CALLS.policy"; FAILURES=0
  rm -rf "$TMP_BASE/engine-sync/mirror-tree" "$TMP_BASE/engine-sync/mirror-tree.built"
  (
    [ $# -eq 0 ] || export "$@"
    engine_sync_build() {
      printf 'engine_sync_build %s\n' "$*" >> "$T/es-build.calls"
      [ "${STUB_ES_BIN_RC:-0}" -eq 0 ] || { echo 'engine-sync: source unavailable: fixture' >&2; return 1; }
      mkdir -p "$(dirname "$1")" && cp "$T/es-stub" "$1"
    }
    checks_mirror_tree_stage() { # the staging has its own test below; here it only names the tree
      printf 'stage %s\n' "$1" >> "$T/es-stage.calls"
      [ "${STUB_ES_STAGE_RC:-0}" -eq 0 ] || { echo 'mirror tree: fixture could not stage' >&2; return 1; }
      mkdir -p "$1"
    }
    # Every mirror gate but the generation reads the tree it built: ready unless a case says otherwise.
    if [ "$fn" != checks_templates_mirrors_generate ] && [ -z "${MIRROR_UNBUILT:-}" ]; then
      mkdir -p "$(checks_mirror_tree)" && : > "$(checks_mirror_tree).built"
    fi
    "$fn"
    echo "FAILURES=$FAILURES"
  ) >"$T/es.out" 2>&1
}
es_case checks_templates_mirrors_generate
if [ "$(cat "$MIRROR_CALLS")" = "build $TMP_BASE/engine-sync/mirror-tree" ] && [ "$(cat "$T/es-build.calls")" = "engine_sync_build $es_bin" ] &&
  [ "$(cat "$T/es-stage.calls")" = "stage $TMP_BASE/engine-sync/mirror-tree" ] && [ -f "$TMP_BASE/engine-sync/mirror-tree.built" ] &&
  cmp -s "$REPO_ROOT/templates/global/engine-sync/config.json" "$MIRROR_CALLS.policy" &&
  grep -Fq 'engine mirrors generated from the tree under test' "$T/es.out" && grep -Fxq 'FAILURES=0' "$T/es.out"; then
  ok 'mirror generation stages the tree under test and builds both mirrors there with the engine-sync it built and the tree'"'"'s policy'
else bad 'mirror generation, current' "$(cat "$T/es-build.calls")" "$(cat "$MIRROR_CALLS")" "$(cat "$T/es.out")"; fi
es_case checks_templates_mirrors_generate STUB_ES_BUILD_RC=1
if [ "$(cat "$MIRROR_CALLS")" = "build $TMP_BASE/engine-sync/mirror-tree" ] && [ ! -e "$TMP_BASE/engine-sync/mirror-tree.built" ] &&
  grep -Fq 'mirror generation FAILED (engine-sync build exit 1)' "$T/es.out" && grep -Fq 'no mirror gate below is a verdict on the tree' "$T/es.out" &&
  grep -Fxq 'FAILURES=1' "$T/es.out"; then
  ok 'a tree whose mirrors do not build is one red step, and no later mirror gate reads a half-built tree'
else bad 'mirror generation, build failure' "$(cat "$MIRROR_CALLS")" "$(cat "$T/es.out")"; fi
es_case checks_templates_mirrors_generate STUB_ES_STAGE_RC=1
if [ ! -s "$MIRROR_CALLS" ] && [ ! -e "$TMP_BASE/engine-sync/mirror-tree.built" ] &&
  grep -Fq 'mirror generation could not run — the tree under test could not be staged' "$T/es.out" && grep -Fxq 'FAILURES=1' "$T/es.out"; then
  ok 'a tree that cannot be staged is named, and engine-sync builds nothing'
else bad 'mirror generation, staging failure' "$(cat "$MIRROR_CALLS")" "$(cat "$T/es.out")"; fi
# The staging itself: the tree under test is copied as it is — tracked, deleted-tracked and untracked
# files — and nothing a host build left in it (the stale mirrors) or a submodule's checkout comes along.
stage_repo="$T/stage-repo"; rm -rf "$stage_repo"; mkdir -p "$stage_repo/src" "$stage_repo/.codex/agents"
(
  cd "$stage_repo" || exit 1
  git init -q . && git -c user.name=t -c user.email=t@example.invalid commit -q --allow-empty -m base
  printf '/AGENTS.md\n.codex/agents/\n' > .gitignore
  printf 'kept\n' > src/tracked.txt; printf 'gone\n' > src/deleted.txt
  git add .gitignore src && git -c user.name=t -c user.email=t@example.invalid commit -q -m files
  git update-index --add --cacheinfo 160000,"$(git rev-parse HEAD)",sub
  rm src/deleted.txt; printf 'new\n' > src/untracked.txt
  mkdir -p sub && printf 'foreign\n' > sub/inside.txt
  printf 'stale host mirror\n' > AGENTS.md; printf 'stale\n' > .codex/agents/old.toml
) >/dev/null 2>&1
stage_out="$T/stage.out"; stage_dest="$T/stage-dest"
if ( REPO_ROOT="$stage_repo"; unset PFM_DEV_REPO_GIT_DIR PFM_DEV_REPO_WORK_TREE; checks_mirror_tree_stage "$stage_dest" ) >"$stage_out" 2>&1 &&
  [ "$(cat "$stage_dest/src/tracked.txt")" = kept ] && [ "$(cat "$stage_dest/src/untracked.txt")" = new ] &&
  [ ! -e "$stage_dest/src/deleted.txt" ] && [ ! -e "$stage_dest/AGENTS.md" ] && [ ! -e "$stage_dest/.codex/agents/old.toml" ] &&
  [ ! -e "$stage_dest/sub" ] && git -C "$stage_dest" ls-files | grep -Fxq src/untracked.txt; then
  ok 'the mirror tree holds the tree under test as it is: untracked files in, stale host mirrors and submodule checkouts out'
else bad 'mirror tree staging' "$(cat "$stage_out")" "$(find "$stage_dest" -not -path '*/.git/*' 2>&1)"; fi
es_case checks_templates_mirrors_generate STUB_ES_BIN_RC=1
if [ ! -s "$MIRROR_CALLS" ] && grep -Fxq 'engine-sync: source unavailable: fixture' "$T/es.out" &&
  grep -Fxq 'FAILSTEP mirror generation could not run — the engine-sync binary could not be built (see above); no mirror gate below is a verdict on the tree' "$T/es.out" &&
  grep -Fxq 'FAILURES=1' "$T/es.out"; then
  ok 'an engine-sync binary that cannot be built is named, and no mirror command runs'
else bad 'mirror generation, no binary' "$(cat "$MIRROR_CALLS")" "$(cat "$T/es.out")"; fi
mv "$REPO_ROOT/templates/global/engine-sync/config.json" "$T/saved-policy.json"
es_case checks_templates_mirrors_generate
if [ ! -s "$MIRROR_CALLS" ] &&
  grep -Fq 'mirror generation could not run — the machine policy could not be staged' "$T/es.out" && grep -Fxq 'FAILURES=1' "$T/es.out"; then
  ok 'mirror generation reports an unreadable machine policy before checking mirrors'
else bad 'mirror generation, unreadable policy' "$(cat "$MIRROR_CALLS")" "$(cat "$T/es.out")"; fi
mv "$T/saved-policy.json" "$REPO_ROOT/templates/global/engine-sync/config.json"
roster_case() {
  es_case roster_probe "$@"
}
roster_probe() {
  node() {
    printf '%s %s %s\n' "${ENGINE_SYNC_BIN:-unset}" "${XDG_CONFIG_HOME:-unset}" "$*" > "$T/roster.calls"
    if [ -n "${XDG_CONFIG_HOME:-}" ]; then
      cp "$XDG_CONFIG_HOME/engine-sync/config.json" "$T/roster.policy" || return 1
    else printf 'unstaged\n' > "$T/roster.policy"; fi
    return "${STUB_ROSTER_RC:-0}"
  }
  node_test_suite() { printf '%s\n' "$*" > "$T/roster.tests"; }
  checks_templates_mirrors_roster
}
: > "$T/roster.calls"; : > "$T/roster.tests"
roster_case
if [ "$(cat "$T/roster.calls")" = "$es_bin $TMP_BASE/engine-sync/project-config scripts/check-agent-roster.mjs" ] &&
  cmp -s "$REPO_ROOT/templates/global/engine-sync/config.json" "$T/roster.policy" &&
  grep -Fq 'scripts/check-agent-roster.test.mjs' "$T/roster.tests" && grep -Fxq 'FAILURES=0' "$T/es.out"; then
  ok 'roster uses the mirror compiler and staged project-config machine policy and runs its regression suite'
else bad 'roster effective config handoff' "$(cat "$T/roster.calls")" "$(cat "$T/es.out")"; fi
roster_case STUB_ROSTER_RC=2
if grep -Fq 'FAILSTEP agent roster ERROR' "$T/es.out" && grep -Fxq 'FAILURES=1' "$T/es.out"; then
  ok 'roster config ERROR remains distinct from a roster mismatch at the gate'
else bad 'roster config error' "$(cat "$T/es.out")"; fi
roster_case MIRROR_UNBUILT=1
if grep -Fq 'FAILSTEP agent roster NOT GENERATED — the mirrors were not generated from the tree under test' "$T/es.out" && grep -Fxq 'FAILURES=1' "$T/es.out"; then
  ok 'roster without a generated mirror tree is NOT GENERATED, and reads no host mirror'
else bad 'roster without a mirror tree' "$(cat "$T/es.out")"; fi
es_case checks_templates_mirrors_opencode MIRROR_UNBUILT=1
if [ ! -s "$MIRROR_CALLS" ] && grep -Fxq 'FAILSTEP opencode mirror NOT CHECKED — the mirrors were not generated from the tree under test (see the generation step above)' "$T/es.out" && grep -Fxq 'FAILURES=1' "$T/es.out"; then
  ok 'the opencode check without a generated mirror tree is NOT CHECKED, never a pass'
else bad 'opencode without a mirror tree' "$(cat "$MIRROR_CALLS")" "$(cat "$T/es.out")"; fi
roster_case STUB_ROSTER_RC=3
if grep -Fq 'FAILSTEP agent roster NOT GENERATED' "$T/es.out" && grep -Fxq 'FAILURES=1' "$T/es.out"; then
  ok 'roster on absent mirrors is NOT GENERATED, never a missing source role'
else bad 'roster not generated' "$(cat "$T/es.out")"; fi
for roster_state in '4|GAP install|inventory toolchain gap' '5|SOURCE REJECTED|inventory source rejection'; do
  IFS='|' read -r roster_rc roster_label roster_reason <<<"$roster_state"
  roster_case "STUB_ROSTER_RC=$roster_rc"
  if grep -Fq "FAILSTEP agent roster $roster_label" "$T/es.out" && grep -Fxq 'FAILURES=1' "$T/es.out"; then
    ok "roster $roster_reason has its own gate classification"
  else bad "roster $roster_reason" "$(cat "$T/es.out")"; fi
done
mv "$REPO_ROOT/templates/global/engine-sync/config.json" "$T/saved-roster-policy.json"
roster_case
if grep -Fq 'FAILSTEP agent roster ERROR' "$T/es.out" && grep -Fxq 'FAILURES=1' "$T/es.out"; then
  ok 'roster reports policy staging failure as ERROR'
else bad 'roster policy staging error' "$(cat "$T/es.out")"; fi
mv "$T/saved-roster-policy.json" "$REPO_ROOT/templates/global/engine-sync/config.json"
# checks_templates_codex_sync: the door test runs once per twin, the project-tier copy first; a red run is one red step
# naming the twin that failed, and the other twin still runs.
cs_case() { # cs_case [VAR=value…]: run it with `bash` stubbed to record each test invocation; output in $T/es.out
  : > "$T/cs.calls"; FAILURES=0
  (
    [ $# -eq 0 ] || export "$@"
    bash() { printf '%s\n' "$*" >> "$T/cs.calls"; [ "${STUB_CS_FAIL:-}" != "$2" ]; }
    checks_templates_codex_sync
    echo "FAILURES=$FAILURES"
  ) >"$T/es.out" 2>&1
}
cs_case
if [ "$(cat "$T/cs.calls")" = "$(printf '%s\n%s\n' "$REPO_ROOT/scripts/test-codex-sync.sh $REPO_ROOT/templates/project/scripts/codex-sync.sh" "$REPO_ROOT/scripts/test-codex-sync.sh $REPO_ROOT/.claude/scripts/codex-sync.sh")" ] && grep -Fxq 'FAILURES=0' "$T/es.out"; then
  ok 'the codex-sync door test runs against the project-tier copy and then the .claude twin'
else bad 'codex-sync door runs' "$(cat "$T/cs.calls")" "$(cat "$T/es.out")"; fi
cs_case "STUB_CS_FAIL=$REPO_ROOT/.claude/scripts/codex-sync.sh"
if [ "$(wc -l < "$T/cs.calls" | tr -d ' ')" = 2 ] && grep -Fxq 'FAILSTEP codex-sync regression FAILED (.claude/scripts/codex-sync.sh) — see output' "$T/es.out" && grep -Fxq 'FAILURES=1' "$T/es.out"; then
  ok 'a red codex-sync door test on one twin is one red step naming it'
else bad 'codex-sync door failure' "$(cat "$T/cs.calls")" "$(cat "$T/es.out")"; fi
# checks_templates_generated_agents: every machine agent-file declaration is rendered by the
# engine-sync it built into a jail home; a render error, a short count and an unbuilt binary are each one red step.
mkdir -p "$REPO_ROOT/templates/global/agents"
ga_sources() {
  mkdir -p "$REPO_ROOT/templates/global/agents"
  for n in alpha-pro beta-max gamma-lite; do
    printf -- '---\nname: %s\nfrom: %s\n---\n' "$n" "${n%-*}" > "$REPO_ROOT/templates/global/agents/$n.md"
  done
  mkdir -p "$REPO_ROOT/templates/global/agents/shared"
  printf -- '---\nmodel: smart\n---\n' > "$REPO_ROOT/templates/global/agents/shared/base.md"
}
ga_sources
# The fixture owns its scanner; the real compiler remains stubbed at its boundary.
mkdir -p "$REPO_ROOT/scripts"
if [ -f "${SUT%/*}/../../scripts/agent-source-inventory.py" ]; then
  cp "${SUT%/*}/../../scripts/agent-source-inventory.py" "$REPO_ROOT/scripts/agent-source-inventory.py"
fi
ga_names='alpha-pro beta-max gamma-lite'
ga_count=3
ga_jailed() { [ -z "$(ls -A "$TMP_BASE/engine-sync" | grep '^generated-agents\.')" ]; } # no jail left under $TMP_BASE
es_case checks_templates_generated_agents "STUB_ES_RENDER=$ga_names" HOME=/real-home XDG_CONFIG_HOME=/real-home/.config \
  XDG_STATE_HOME=/real-home/.local/state XDG_DATA_HOME=/real-home/.local/share ENGINE_SYNC_HOME=/real-home/es \
  ENGINE_SYNC_STATE=/real-home/es-state CODEX_HOME=/real-home/.codex
if [ "$(wc -l < "$MIRROR_CALLS" | tr -d ' ')" -eq 1 ] && [ "$(cat "$T/es-build.calls")" = "engine_sync_build $es_bin" ] &&
  [[ "$(cat "$MIRROR_CALLS")" == "global --home $TMP_BASE/engine-sync/generated-agents."*"/home --source-repo $TMP_BASE/engine-sync/generated-agents."*"/src --claude-config-dir $TMP_BASE/engine-sync/generated-agents."*"/home/.claude --codex-home $TMP_BASE/engine-sync/generated-agents."*"/home/.codex --capability gpt-agents" ]] &&
  [[ "$(cat "$MIRROR_CALLS.env")" == "HOME=$TMP_BASE/engine-sync/generated-agents."*"/home XDG_CONFIG_HOME=unset XDG_STATE_HOME=unset XDG_DATA_HOME=unset ENGINE_SYNC_HOME=unset ENGINE_SYNC_STATE=unset CODEX_HOME=unset" ]] &&
  grep -Fq "all $ga_count generated agent(s) render through engine-sync into a jail home" "$T/es.out" && grep -Fxq 'FAILURES=0' "$T/es.out" &&
  cmp -s "$MIRROR_CALLS.policy" "$REPO_ROOT/templates/global/engine-sync/config.json" && ga_jailed; then
  ok "generated agents: every declaration renders into a jail home that no caller's HOME, XDG, engine-sync or Codex variable reaches, the count is named, the jail is removed ($ga_count)"
else bad 'generated agents, rendered' "$(cat "$MIRROR_CALLS")" "$(cat "$MIRROR_CALLS.env")" "$(cat "$T/es.out")"; fi
es_case checks_templates_generated_agents STUB_ES_GLOBAL_RC=1
if grep -Fq 'variant "beta-max" from beta.md' "$T/es.out" &&
  grep -Fq 'FAILSTEP generated agents FAILED to render (engine-sync global exit 1; declaration named in the error: beta-max)' "$T/es.out" &&
  grep -Fxq 'FAILURES=1' "$T/es.out" && ga_jailed; then
  ok 'a render error is one red step that names the declaration, and the jail is removed'
else bad 'generated agents, render error' "$(cat "$T/es.out")"; fi
short="${ga_names#* }"
es_case checks_templates_generated_agents "STUB_ES_RENDER=$short"
if grep -Fq "FAILSTEP generated agents: $((ga_count - 1)) of $ga_count declaration(s) rendered — missing: ${ga_names%% *}" "$T/es.out" &&
  grep -Fxq 'FAILURES=1' "$T/es.out" && ga_jailed; then
  ok 'a rendered count short of the declared count is red and names the missing declaration, and the jail is removed'
else bad 'generated agents, short count' "$(cat "$T/es.out")"; fi
es_case checks_templates_generated_agents "STUB_ES_RENDER="
if grep -Fq "FAILSTEP generated agents: 0 of $ga_count declaration(s) rendered — missing: " "$T/es.out" && grep -Fxq 'FAILURES=1' "$T/es.out"; then
  ok 'an engine-sync that renders nothing is red, never clean'
else bad 'generated agents, none rendered' "$(cat "$T/es.out")"; fi
es_case checks_templates_generated_agents "STUB_ES_RENDER=${ga_names% *}" STUB_ES_CLAUDE_ONLY="${ga_names##* }"
if grep -Fq "all $ga_count generated agent(s) render through engine-sync into a jail home" "$T/es.out" && grep -Fxq 'FAILURES=0' "$T/es.out" && ga_jailed; then
  ok 'a derived agent whose engines block turns Codex off renders as a Claude link alone'
else bad 'generated agents, Claude-only declaration' "$(cat "$T/es.out")"; fi
es_case checks_templates_generated_agents "STUB_ES_RENDER=$ga_names" STUB_ES_DRIFT=1
if grep -Fq 'SECTION-DRIFT beta-max' "$T/es.out" &&
  grep -Fq 'FAILSTEP generated agents FAILED to render (engine-sync global exit 1; declaration named in the error: beta-max)' "$T/es.out" &&
  grep -Fxq 'FAILURES=1' "$T/es.out" && ga_jailed; then
  ok 'a SECTION-DRIFT is red and names the declaration even when engine-sync exits 0'
else bad 'generated agents, drift' "$(cat "$T/es.out")"; fi
es_case checks_templates_generated_agents "STUB_ES_RENDER=$ga_names" STUB_ES_UNKNOWN=1
if grep -Eq '^ *2 skip    unknown MCP server in tool mcp__sub-agent-compact__compact in ' "$T/es.out" &&
  grep -Fq 'FAILSTEP generated agents: engine-sync dropped MCP tools from OpenCode twins (2 distinct' "$T/es.out" &&
  ! grep -Fq 'generated agents FAILED to render' "$T/es.out" && grep -Fxq 'FAILURES=1' "$T/es.out" && ga_jailed; then
  ok 'an unknown-MCP-server warning from an exit-0 render is red and names each server and its count, never a clean render'
else bad 'generated agents, unknown MCP server' "$(cat "$T/es.out")"; fi
es_case checks_templates_generated_agents "STUB_ES_RENDER=$ga_names" STUB_ES_UNMAPPED=1
if grep -Fq 'unmapped OpenCode model alias "claude-sonnet-5-5" in ' "$T/es.out" &&
  grep -Fq 'FAILSTEP generated agents: engine-sync omitted the model of an OpenCode twin (1 distinct' "$T/es.out" &&
  ! grep -Fq 'generated agents FAILED to render' "$T/es.out" && grep -Fxq 'FAILURES=1' "$T/es.out" && ga_jailed; then
  ok 'an unmapped-model-alias warning from an exit-0 render is red and names the alias, never a clean render'
else bad 'generated agents, unmapped model alias' "$(cat "$T/es.out")"; fi
es_case checks_templates_generated_agents STUB_ES_BIN_RC=1
if [ ! -s "$MIRROR_CALLS" ] &&
  grep -Fxq 'FAILSTEP generated agents NOT RENDERED — the engine-sync binary could not be built (see above)' "$T/es.out" &&
  grep -Fxq 'FAILURES=1' "$T/es.out"; then
  ok 'generated agents without an engine-sync binary are NOT RENDERED, never a pass'
else bad 'generated agents, no binary' "$(cat "$MIRROR_CALLS")" "$(cat "$T/es.out")"; fi
ga_dir="$REPO_ROOT/templates/global/agents"
for ga_state in missing unparseable empty invalid; do
  rm -rf "$ga_dir"
  case "$ga_state" in
    missing) ga_want="FAILSTEP generated agents NOT RENDERED — $ga_dir is missing, so nothing was looked up" ;;
    unparseable) mkdir -p "$ga_dir"; printf -- '---\nfrom: [\n---\n' > "$ga_dir/broken.md"; ga_want='FAILSTEP generated agents NOT RENDERED — agent sources do not parse: ' ;;
    empty) mkdir -p "$ga_dir"; printf -- '---\nname: solo\n---\n' > "$ga_dir/solo.md"; ga_want='FAILSTEP generated agents NOT RENDERED — agent sources carry no declaration, so nothing could render' ;;
    invalid) mkdir -p "$ga_dir"; printf -- '---\nfrom: []\n---\n' > "$ga_dir/broken.md"; ga_want='FAILSTEP generated agents NOT RENDERED — agent sources do not parse: ' ;;
  esac
  es_case checks_templates_generated_agents "STUB_ES_RENDER=$ga_names"
  if [ ! -s "$MIRROR_CALLS" ] && grep -Fq "$ga_want" "$T/es.out" && grep -Fxq 'FAILURES=1' "$T/es.out" && ga_jailed; then
    ok "agent sources ($ga_state) are NOT RENDERED, red, with engine-sync never run"
  else bad "generated agents, $ga_state source" "$(cat "$MIRROR_CALLS")" "$(cat "$T/es.out")"; fi
done
rm -rf "$ga_dir"; ga_sources
ga_without_python() { need_tool() { [ "$1" != python3 ]; }; checks_templates_generated_agents; }
es_case ga_without_python "STUB_ES_RENDER=$ga_names"
if [ ! -s "$MIRROR_CALLS" ] && [ ! -s "$T/es-build.calls" ] &&
  grep -Fxq 'FAILSTEP generated agents NOT RENDERED — the toolchain is missing' "$T/es.out" && grep -Fxq 'FAILURES=1' "$T/es.out"; then
  ok 'generated agents without python3 are NOT RENDERED, nothing built or run'
else bad 'generated agents, no python3' "$(cat "$T/es-build.calls")" "$(cat "$T/es.out")"; fi
# A.09 counts effective Claude agents eligible for Codex, not raw layers or declarations.
lane_agents="$T/lane-agents"
mkdir -p "$lane_agents/blueprint/templates/global/agents" "$lane_agents/home/.claude/agents" "$lane_agents/home/.codex/agents" "$lane_agents/blueprint/scripts"
cp "${SUT%/*}/../../scripts/agent-source-inventory.py" "$lane_agents/blueprint/scripts/agent-source-inventory.py"
printf -- '---\nname: alpha\nmodel: smart\n---\n' > "$lane_agents/blueprint/templates/global/agents/alpha.md"
mkdir -p "$lane_agents/blueprint/templates/global/agents/shared"
printf -- '---\nmodel: smart\n---\n' > "$lane_agents/blueprint/templates/global/agents/shared/layer.md"
printf -- '---\nname: derived\nfrom: alpha\n---\n' > "$lane_agents/blueprint/templates/global/agents/derived.md"
cp "$lane_agents/blueprint/templates/global/agents/alpha.md" "$lane_agents/home/.claude/agents/alpha.md"
printf -- '---\nname: derived\nengines:\n  codex: false\n---\n' > "$lane_agents/home/.claude/agents/derived.md"
printf '# Generated by engine-sync from alpha.md\n' > "$lane_agents/home/.codex/agents/alpha.toml"
sed -n '/^beat A.09-codex-agents$/,/^# .*A.10/p' "${SUT%/*}/lanes/A.sh" > "$lane_agents/beat.sh"
lane_agents_case() (
  export HOME="$lane_agents/home"
  BLUEPRINT="$lane_agents/blueprint"
  beat() { :; }; spends() { :; }; _lane_log_only() { :; }
  one_line() { tr '\n' ' ' <<<"$1"; }
  pfm() { printf '%s\nCODEX AGENTS PASS\n' "$HOME/.codex/agents/alpha.toml: 43 B, parses clean"; }
  fail() { printf 'FAIL %s\n' "$*"; }
  pass() { printf 'PASS %s\n' "$*"; }
  source "$lane_agents/beat.sh"
)
lane_agents_case > "$T/lane-agents.out" 2>&1
if grep -Fq 'PASS 1 global agents compiled to parsing .toml roles' "$T/lane-agents.out"; then
  ok 'A.09 judges one compiled agent, not a shared file or a Claude-only declaration'
else bad 'A.09 effective agents' "$(cat "$T/lane-agents.out")"; fi
cp "$lane_agents/blueprint/scripts/agent-source-inventory.py" "$lane_agents/inventory.py"
for lane_inventory_failure in '1|agent-source-inventory: ERROR — fixture source rejection' '3|GAP install (agent source inventory is missing: PyYAML)'; do
  IFS='|' read -r lane_rc lane_error <<<"$lane_inventory_failure"
  printf 'import sys\nprint(%s, file=sys.stderr)\nsys.exit(%s)\n' "\"$lane_error\"" "$lane_rc" > "$lane_agents/blueprint/scripts/agent-source-inventory.py"
  lane_agents_case > "$T/lane-agents.out" 2>&1
  if grep -Fq "FAIL  effective global agents NOT SCANNED: $lane_error;" "$T/lane-agents.out" &&
    ! grep -Fq 'no effective global Claude agents eligible' "$T/lane-agents.out"; then
    ok "A.09 failed inventory ($lane_rc) is NOT SCANNED, never empty"
  else bad "A.09 failed inventory ($lane_rc)" "$(cat "$T/lane-agents.out")"; fi
done
cp "$lane_agents/inventory.py" "$lane_agents/blueprint/scripts/agent-source-inventory.py"
es_case checks_templates_mirrors_opencode
want="$(printf '%s\n%s' \
  "opencode check $TMP_BASE/engine-sync/mirror-tree --home $TMP_BASE/engine-sync/opencode-verify-home" \
  "opencode doctor $TMP_BASE/engine-sync/mirror-tree --home $TMP_BASE/engine-sync/opencode-verify-home")"
if [ "$(cat "$MIRROR_CALLS")" = "$want" ] && [ "$(cat "$T/es-build.calls")" = "engine_sync_build $es_bin" ] &&
  cmp -s "$REPO_ROOT/templates/global/engine-sync/config.json" "$MIRROR_CALLS.policy" &&
  grep -Fq 'opencode mirror parseable and current against the tree under test' "$T/es.out" && grep -Fxq 'FAILURES=0' "$T/es.out"; then
  ok 'the opencode mirror is checked and doctored by the engine-sync it built, under its own scratch home'
else bad 'opencode mirror' "$(cat "$T/es-build.calls")" "$(cat "$MIRROR_CALLS")" "$(cat "$T/es.out")"; fi
es_case checks_templates_mirrors_opencode STUB_ES_OPENCODE_RC=1
if grep -Fxq "FAILSTEP opencode mirror FAILED — the mirror generated from the tree under test does not check (see above)" "$T/es.out" && grep -Fxq 'FAILURES=1' "$T/es.out"; then
  ok 'a failing opencode check names the tree'"'"'s own mirror, not the host'"'"'s'
else bad 'opencode mirror failure' "$(cat "$MIRROR_CALLS")" "$(cat "$T/es.out")"; fi
es_case checks_templates_mirrors_opencode STUB_ES_BIN_RC=1
if [ ! -s "$MIRROR_CALLS" ] &&
  grep -Fxq 'FAILSTEP opencode mirror NOT CHECKED — the engine-sync binary could not be built (see above)' "$T/es.out" &&
  grep -Fxq 'FAILURES=1' "$T/es.out"; then
  ok 'an opencode check without an engine-sync binary is NOT CHECKED, never a pass'
else bad 'opencode mirror, no binary' "$(cat "$MIRROR_CALLS")" "$(cat "$T/es.out")"; fi

# checks_templates_unit_script: one suite of .claude/scripts under its own label; a red suite is run's FAIL, so its status passes through.
out="$( ( run() { shift 2; printf 'run [%s]\n' "$*"; }; head_() { printf 'head [%s]\n' "$*"; }; REPO_ROOT=/repo; checks_templates_unit_script unit-path ) 2>&1 )"
if grep -Fxq 'run [bash /repo/scripts/test-unit-path.sh]' <<<"$out"; then
  ok 'the unit-path suite runs through bash'
else bad 'checks_templates_unit_script run' "$out"; fi
out="$( ( run() { return 3; }; head_() { :; }; REPO_ROOT=/repo; checks_templates_unit_script check-pfm; echo "rc=$?" ) 2>&1 )"
if [ "$out" = 'rc=3' ]; then ok "checks_templates_unit_script returns run's status, a red suite is not swallowed"
else bad 'checks_templates_unit_script red suite' "$out"; fi

# checks_templates (CI's iso test templates) calls each of the five unit-script suites once.
out="$( (
  for f in $(declare -F | awk '{print $3}' | grep '^checks_templates_'); do
    case "$f" in checks_templates_steps|checks_templates_step_serial) continue ;; esac
    eval "$f() { printf '%s%s\\n' '$f' \"\${*:+ \$*}\"; }"
  done
  checks_templates
) 2>&1 )"
for n in check-pfm check-templates test-pfm test-templates unit-path; do
  if [ "$(grep -Fxc "checks_templates_unit_script $n" <<<"$out")" -eq 1 ]; then
    ok "the serial list calls the $n suite once"
  else bad "the serial list calls the $n suite once" "$out"; fi
done

# The RUN DIR line (dev.sh's EXIT trap, so it is the output's last line): the run dir this invocation
# made, by its HOST path — inside the fence /pfm-timing is the bind of PFM_TEST_TIMING_HOST.
run_report() { # run_report <TIMING_RUN_LAST> <PFM_TEST_TIMING_HOST> <PFM_TEST_RUN_NOTE> <TIMING_RUN_NOTE>
  TIMING_RUN_LAST="$1" PFM_TEST_TIMING_DIR=/pfm-timing PFM_TEST_TIMING_HOST="$2" PFM_TEST_RUN_NOTE="$3" TIMING_RUN_NOTE="$4" \
    timing_run_report 2>&1
}
out="$(run_report /pfm-timing/run.abc123 /tmp/gate-tip/timing '' '')"
if [ "$out" = 'RUN DIR: /tmp/gate-tip/timing/run.abc123' ]; then
  ok 'RUN DIR names the host path when PFM_TEST_TIMING_HOST is set'
else bad 'RUN DIR host path' "$out"; fi
out="$(run_report /pfm-timing/run.abc123 '' '' '')"
if [ "$out" = 'RUN DIR: /pfm-timing/run.abc123' ]; then
  ok 'RUN DIR names the run dir as made when PFM_TEST_TIMING_HOST is unset (a host run)'
else bad 'RUN DIR without host' "$out"; fi
out="$(run_report '' /tmp/gate-tip/timing '' '')"
if [ -z "$out" ]; then
  ok 'no run dir made, no RUN DIR line'
else bad 'RUN DIR with no run dir' "$out"; fi
rm -f "$T/run-note"
out="$(run_report /pfm-timing/run.abc123 /tmp/gate-tip/timing "$T/run-note" '')"
if [ -z "$out" ] && [ "$(cat "$T/run-note" 2>/dev/null)" = /tmp/gate-tip/timing/run.abc123 ]; then
  ok 'under dev.sh iso the fence hands the host path to the note and leaves the line to the host'
else bad 'RUN DIR note write' "stdout: $out" "note: $(cat "$T/run-note" 2>&1)"; fi
out="$(run_report '' '' '' "$T/run-note")"
if [ "$out" = 'RUN DIR: /tmp/gate-tip/timing/run.abc123' ] && [ ! -e "$T/run-note" ]; then
  ok 'the host prints the note it was handed as the RUN DIR line and removes the note'
else bad 'RUN DIR note read' "$out" "$(ls -l "$T/run-note" 2>&1)"; fi
: > "$T/run-note"
out="$(run_report '' '' '' "$T/run-note")"
if [ -z "$out" ] && [ ! -e "$T/run-note" ]; then
  ok 'an empty note (the fence made no run dir) prints nothing and is removed'
else bad 'RUN DIR empty note' "$out" "$(ls -l "$T/run-note" 2>&1)"; fi
out="$(run_report /pfm-timing/run.abc123 /tmp/gate-tip/timing "$T/no-such-dir/run-note" '')"
if [ "$(printf '%s\n' "$out" | tail -1)" = 'RUN DIR: /tmp/gate-tip/timing/run.abc123' ] && [[ "$out" == *"$T/no-such-dir/run-note could not be written"* ]]; then
  ok 'a note that cannot be written is named and the fence prints the line itself'
else bad 'RUN DIR unwritable note' "$out"; fi

# checks_templates_leak: a submodule's gitlink is another repository, counted as such, never scanned and never a deleted path.
leak_repo="$T/leak-submodule"
mkdir -p "$leak_repo/scripts" "$leak_repo/plugins/demo"
printf 'clean\n' > "$leak_repo/kept.md"
printf 'other repository\n' > "$leak_repo/plugins/demo/file.md"
cat > "$leak_repo/scripts/leak-check.sh" <<'STUB'
#!/usr/bin/env bash
shift; printf '%s\n' "$@" >> "$LEAK_FILES_OUT"
[ -z "${LEAK_STUB_NOTE:-}" ] || printf '%s\n' "$LEAK_STUB_NOTE" >&2
STUB
chmod +x "$leak_repo/scripts/leak-check.sh"
git -C "$leak_repo" init -q &&
  git -C "$leak_repo" add kept.md scripts/leak-check.sh &&
  git -C "$leak_repo" update-index --add --cacheinfo 160000,1111111111111111111111111111111111111111,plugins/demo &&
  git -C "$leak_repo" -c user.name=fixture -c user.email=fixture.invalid commit -qm fixture ||
  bad 'leak submodule fixture' 'git could not build the fixture repository'
leak_gate() {
  : > "$T/leak-files"
  (
    cd "$leak_repo" || exit 1
    repo_git() { git "$@"; }; run() { :; }; head_() { :; }; info() { :; }
    ok() { printf 'ok [%s]\n' "$*"; }; fail_step() { printf 'fail [%s]\n' "$*"; }
    TMP_BASE="$T/leak-tmp" LEAK_FILES_OUT="$T/leak-files" checks_templates_leak
  ) 2>&1
}
leak_clean='ok [leak-check clean (2 file(s): the whole tracked tree plus 0 changed, 0 deleted path(s) skipped, 1 submodule(s) left to their own repository'"'"'s gate)]'
leak_scanned="$(printf 'kept.md\nscripts/leak-check.sh')"
out="$(leak_gate)"
if [ "$out" = "$leak_clean" ] && [ "$(cat "$T/leak-files" 2>&1)" = "$leak_scanned" ]; then
  ok 'the leak gate scans the tracked files and counts a submodule as its own repository, not a deleted path'
else bad 'leak gate submodule' "$out" "$(cat "$T/leak-files" 2>&1)"; fi
# F6: a scan run under the explicit `!structural-only` policy applied no private denylist; its row says so,
# and the denylist row's text stays as it was.
leak_structural_note='leak-check: PRIVATE COVERAGE SKIPPED — explicit structural-only policy; scanning 4 structural pattern(s)'
out="$(LEAK_STUB_NOTE="$leak_structural_note" leak_gate)"
if [[ "$out" == *'ok [leak-check clean (STRUCTURAL-ONLY: the private denylist was NOT applied — 2 file(s):'* && "$out" != *"$leak_clean"* ]]; then
  ok 'F6: the leak row names a structural-only scan, distinct from the denylist row'
else bad 'F6: structural-only leak row' "$out"; fi
# The same plugin once it holds commits its gitlink lacks: status names the gitlink as changed, still neither scanned nor deleted.
git -C "$leak_repo/plugins/demo" init -q &&
  git -C "$leak_repo/plugins/demo" add file.md &&
  git -C "$leak_repo/plugins/demo" -c user.name=fixture -c user.email=fixture.invalid commit -qm plugin ||
  bad 'leak submodule commits fixture' 'git could not commit inside the fixture plugin'
status="$(git -C "$leak_repo" status --porcelain 2>&1)"
out="$(leak_gate)"
if [ "$status" = ' M plugins/demo' ] && [ "$out" = "$leak_clean" ] && [ "$(cat "$T/leak-files" 2>&1)" = "$leak_scanned" ]; then
  ok 'a submodule with new commits is a changed gitlink, never a changed file or a deleted path'
else bad 'leak gate submodule with new commits' "$status" "$out" "$(cat "$T/leak-files" 2>&1)"; fi

# F7: the generated-marker row carries the script's own count line, so a run that examined nothing reads as one.
marker_repo="$T/marker-row"
mkdir -p "$marker_repo/scripts"
marker_gate() { # marker_gate <script body>
  printf '%s\n' "$1" > "$marker_repo/scripts/check-codex-markers.mjs"
  (
    cd "$marker_repo" || exit 1
    head_() { :; }; node_test_suite() { :; }
    ok() { printf 'ok [%s]\n' "$*"; }; fail_step() { printf 'fail [%s]\n' "$*"; }
    checks_mirror_tree() { echo "$marker_repo"; }; checks_mirror_tree_ready() { return 0; }
    checks_templates_mirrors_marker
  ) 2>&1
}
out="$(marker_gate 'console.log("codex marker claim OK — 0 marked file(s) (0 tracked, 0 generated mirror), 0 orphans");')"
if [[ "$out" == *'ok [every marked file has a live source; every tracked symlink a tracked target — codex marker claim OK — 0 marked file(s) (0 tracked, 0 generated mirror), 0 orphans]'* ]]; then
  ok 'F7: the marker row shows how many marked files the script examined'
else bad 'F7: marker row count' "$out"; fi
out="$(marker_gate 'console.error("FAIL ORPHAN x"); process.exit(1);')"
if [[ "$out" == *'FAIL ORPHAN x'* && "$out" == *'fail [codex marker claim FAILED'* && "$out" != *'ok ['* ]]; then
  ok 'F7: a failing marker script fails the row and its output is shown'
else bad 'F7: marker row failure' "$out"; fi

# A sampler still bootstrapping may not yet honor TERM. Its emitted header
# is the existing readiness door: real sampler.sh emits it after arming traps.
mkdir -p "$T/boot"
cat > "$T/boot/sampler.sh" <<'SH'
trap '' TERM
printf started > "$1/started"
exec {boot_fd}<> <(:)
read -r -t "${BOOT_GRACE_S:-0.2}" -u "$boot_fd" || true
trap 'printf stopped > "$1/stopped"; exit 0' TERM
printf 'epoch_s\n' > "$1/resources.tsv"
while :; do read -r -t 0.1 -u "$boot_fd" || true; done
SH
cat > "$T/boot/driver.sh" <<'SH'
source "$1"
bash "$2/sampler.sh" "$2" &
pid=$!
trap 'kill -KILL "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true' EXIT
for ((i=0;i<100;i++)); do [[ -f "$2/started" ]] && break; sleep 0.01; done
[[ -f "$2/started" ]] || exit 2
gate_resources_stop "$pid" "$2"
[[ -f "$2/stopped" && -s "$2/resources.tsv" ]]
SH
if timeout --kill-after=1s 2s bash "$T/boot/driver.sh" "$SUT" "$T/boot" > "$T/boot/out" 2>&1; then
  ok 'sampler stop waits for armed-trap header and finishes after bootstrap'
else
  boot_rc=$?
  bad 'sampler stop waits for armed-trap header and finishes after bootstrap' "rc=$boot_rc" "$(cat "$T/boot/out")"
fi
# A tracked templates/generated/agents file no fresh render produces (an
# orphan, or a render the sources moved past) is a red step naming it; the
# hand-written templates/generated/README.md beside agents/ is never compared.
mkdir -p "$REPO_ROOT/templates/generated/agents"
printf -- '---\nname: ghost\n---\n' > "$REPO_ROOT/templates/generated/agents/ghost.md"
printf -- '# templates/generated\n' > "$REPO_ROOT/templates/generated/README.md"
es_case checks_templates_generated_agents "STUB_ES_RENDER=$ga_names"
if grep -Fq 'ghost.md' "$T/es.out" && ! grep -Fq 'README.md' "$T/es.out" &&
  grep -Fq 'FAILSTEP generated agents: tracked templates/generated/agents differs from a fresh render — run pfm install and commit templates/generated/agents' "$T/es.out" &&
  grep -Fxq 'FAILURES=1' "$T/es.out" && ga_jailed; then
  ok "generated agents: a tracked generated agent no fresh render produces is red and named, the README beside agents/ never"
else
  bad "generated agents, tracked generated drift" "$(cat "$T/es.out")"
fi
rm -rf "$REPO_ROOT/templates/generated"

# skill_unittest_sharded: the skill suites run in parallel shards; any red shard, a skip or no test stays visible.
mkdir -p "$T/shard/pkg"
cat > "$T/shard/pkg/a_test.py" <<'PY'
import unittest
class A(unittest.TestCase):
    def test_1(self): pass
    def test_2(self): pass
    def test_3(self): pass
PY
cat > "$T/shard/pkg/b_test.py" <<'PY'
import unittest
class B(unittest.TestCase):
    def test_1(self): pass
    def test_2(self): pass
PY
cat > "$T/shard/pkg/red_test.py" <<'PY'
import unittest
class R(unittest.TestCase):
    def test_ok(self): pass
    def test_red(self): self.fail("planted red")
PY
cat > "$T/shard/pkg/skip_test.py" <<'PY'
import unittest
class S(unittest.TestCase):
    def test_ok(self): pass
    @unittest.skip("planted skip")
    def test_skip(self): pass
PY
printf '' > "$T/shard/pkg/empty_test.py"
if (cd "$T/shard" && SKILL_TEST_SHARDS=3 skill_unittest_sharded "$T/shard/green.out" pkg/a_test.py pkg/b_test.py) &&
  [ "$(grep -c '^SHARD ' "$T/shard/green.out")" -eq 3 ] && grep -Fxq 'Ran 5 tests' "$T/shard/green.out" &&
  [ "$(tail -1 "$T/shard/green.out")" = OK ]; then
  ok 'skill shards: 5 tests over 3 shards aggregate to Ran 5 tests, OK'
else bad 'skill shards green' "$(cat "$T/shard/green.out" 2>&1)"; fi
if ! (cd "$T/shard" && SKILL_TEST_SHARDS=2 skill_unittest_sharded "$T/shard/red.out" pkg/a_test.py pkg/red_test.py) &&
  grep -Fq 'planted red' "$T/shard/red.out" && [[ "$(tail -1 "$T/shard/red.out")" == 'FAILED (shards='* ]]; then
  ok 'skill shards: one red test makes the run non-zero, FAILED, with its trace'
else bad 'skill shards red' "$(cat "$T/shard/red.out" 2>&1)"; fi
if (cd "$T/shard" && skill_unittest_sharded "$T/shard/skip.out" pkg/skip_test.py) &&
  grep -Eq 'skipped=[1-9]' "$T/shard/skip.out"; then
  ok 'skill shards: a skipped test still reads skipped=N for the step to refuse'
else bad 'skill shards skip' "$(cat "$T/shard/skip.out" 2>&1)"; fi
if ! (cd "$T/shard" && skill_unittest_sharded "$T/shard/none.out" pkg/empty_test.py) &&
  grep -Fxq 'Ran 0 tests' "$T/shard/none.out"; then
  ok 'skill shards: a file with no test is Ran 0 tests and non-zero'
else bad 'skill shards none' "$(cat "$T/shard/none.out" 2>&1)"; fi
if ! (cd "$T/shard" && skill_unittest_sharded "$T/shard/missing.out" pkg/nope_test.py) &&
  ! grep -Fxq OK "$T/shard/missing.out"; then
  ok 'skill shards: an unimportable test file is red, never OK'
else bad 'skill shards missing' "$(cat "$T/shard/missing.out" 2>&1)"; fi

shtest_end
