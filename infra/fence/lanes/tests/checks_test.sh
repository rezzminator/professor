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
      else if ($0 ~ /^pfm\.self\./) group=3
      else if ($0 ~ /^templates\.lanes\./) group=4
      else if ($0 ~ /^templates\.demo\./) group=5
      else exit 1
      if (group<previous || (target=="pfm" && group>3) || (target=="templates" && group<4)) exit 1
      previous=group
      next
    }
    after && ($0 ~ /^pfm\.self\./ || $0 ~ /^templates\.(lanes|demo)\./ || $0=="pfm.unit" || $0=="pfm.e2e") {exit 1}
    END {if (barriers!=1) exit 1}
  ' "$T/registered"
}
static_names() { sed -n '/^BARRIER$/,$p' "$T/registered" | sed '1d' | paste -sd, -; }
pfm_static='pfm.lint-new,pfm.fmt-check,pfm.vet,pfm.vet-darwin,pfm.arch'
templates_static='templates.check-map,templates.clone,templates.leak,templates.placeholders,templates.scratch-paths,templates.descriptions,templates.mirrors,templates.token-audit,templates.flight-index,templates.release-check,templates.codex-sync,templates.refresh-scope,templates.pfm-guard,templates.dev-report,templates.format-md,templates.check-pfm-tests,templates.check-templates-tests,templates.test-pfm-tests,templates.test-templates-tests,templates.unit-path-tests,templates.opencode-writer-tests,templates.skill-tests,templates.opencode-writer-refs'
heavy_names='pfm.e2e,pfm.fmt-check,pfm.lint-new,pfm.unit,pfm.vet,pfm.vet-darwin,templates.check-map'
if gate_run all >"$T/all.out" &&
  [ "$(grep -c '^templates\.leak$' "$T/registered")" -eq 1 ] &&
  [ "$(grep -c '^templates\.mirrors$' "$T/registered")" -eq 1 ] &&
  [ "$(grep -c '^pfm\.unit$' "$T/registered")" -eq 1 ] &&
  [ "$(grep -c '^pfm\.e2e$' "$T/registered")" -eq 1 ] &&
  [ "$(grep -c '^templates\.lanes\.' "$T/registered")" -eq "$(find "$REPO_ROOT/infra/fence/lanes/tests" -name '*_test.sh' | wc -l | tr -d ' ')" ] &&
  [ "$(grep -c '^templates\.demo\.' "$T/registered")" -eq "$(find "$REPO_ROOT/infra/demo/tests" -name '*_test.sh' | wc -l | tr -d ' ')" ] &&
  [ "$(sed -n '1,2p' "$T/registered" | paste -sd, -)" = 'pfm.unit,pfm.e2e' ] &&
  [ "$(static_names)" = "$pfm_static,$templates_static" ] &&
  [ "$(sort "$T/heavy" | paste -sd, -)" = "$heavy_names" ] &&
  [ -z "$(sort "$T/registered" | uniq -d)" ] && graph_order all; then
  ok 'all registers tests before one barrier and each static step once'
else bad 'all registration' "$(cat "$T/all.out")" "$(cat "$T/registered")"; fi

if PFM_TEST_TIMING_DIR="$T/real-pfm" gate_run pfm >"$T/pfm.out" &&
  [ "$(sed -n '1,2p' "$T/registered" | paste -sd, -)" = 'pfm.unit,pfm.e2e' ] &&
  [ "$(static_names)" = "$pfm_static" ] &&
  [ "$(sort "$T/heavy" | paste -sd, -)" = 'pfm.e2e,pfm.fmt-check,pfm.lint-new,pfm.unit,pfm.vet,pfm.vet-darwin' ] &&
  graph_order pfm; then
  ok 'pfm graph runs its tests before static checks'
else bad 'pfm graph' "$(cat "$T/registered")"; fi
real_run="$(ls -d "$T/real-pfm"/run.* | head -1)"
if grep -Fxq "pfm.e2e checks_pfm_e2e $REPO_ROOT/pfm $real_run" "$T/registered.args"; then
  ok 'pfm.e2e is the profiled e2e wrapper, given the pfm dir and the run dir'
else bad 'pfm.e2e registration' "$(grep '^pfm.e2e' "$T/registered.args")"; fi
if [ -s "$real_run/profile.tsv" ] && [ -f "$real_run/profile/INDEX.txt" ] && grep -Fq "PROFILE index $real_run/profile/INDEX.txt" "$T/pfm.out"; then
  ok 'the real profile-report.sh summary prints its PROFILE block and writes profile.tsv and profile/INDEX.txt'
else bad 'real summary' "$(cat "$T/pfm.out")"; fi

if gate_run templates >"$T/templates.out" &&
  [ "$(static_names)" = "$templates_static" ] &&
  [ "$(cat "$T/heavy")" = templates.check-map ] &&
  graph_order templates; then
  ok 'templates graph runs its tests before static checks'
else bad 'templates graph' "$(cat "$T/registered")"; fi
unit_script_rows="$(printf 'templates.%s-tests checks_templates_unit_script %s\n' check-pfm check-pfm check-templates check-templates test-pfm test-pfm test-templates test-templates unit-path unit-path)"
if [ "$(grep '^templates\.[a-z-]*-tests checks_templates_unit_script ' "$T/registered.args" | tail -5)" = "$unit_script_rows" ] &&
  grep -Fxq 'templates.unit-path-tests checks_templates_unit_script unit-path' "$T/registered.args"; then
  ok 'each unit-script suite is a gate step handed its own name: templates.unit-path-tests runs checks_templates_unit_script unit-path'
else bad 'unit-script step registration' "$(grep 'checks_templates_unit_script' "$T/registered.args")"; fi

# The pfm that pfm.unit prebuilds is kept in <run>/bin: check-map and the mirrors are handed it.
if PFM_TEST_TIMING_DIR="$T/reuse" gate_run all >"$T/reuse.out"; then
  reuse_run="$(ls -d "$T/reuse"/run.* | head -1)"
  if grep -Fxq "pfm.unit checks_pfm_unit $REPO_ROOT/pfm $reuse_run" "$T/registered.args" &&
    grep -Fxq "templates.check-map checks_gate_check_map $reuse_run" "$T/registered.args" &&
    grep -Fxq "templates.mirrors checks_templates_mirrors $reuse_run/bin/pfm" "$T/registered.args" &&
    [ "$(static_names)" = "$pfm_static,$templates_static" ] && [ "$(sort "$T/heavy" | paste -sd, -)" = "$heavy_names" ]; then
    ok 'templates.mirrors is registered with <run>/bin/pfm, same position and light; check-map and pfm.unit keep their arguments'
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

# checks_templates_mirrors_opencode: the same pfm copied to the scratch binary, or built there when none is given.
: > "$T/go.calls"; : > "$MIRROR_CALLS"; FAILURES=0
PFM_TEST_TIMING_DIR="$T/mirror-reuse" checks_templates_mirrors_opencode "$T/prebuilt-pfm" >"$T/mirror-reuse.out" 2>&1
want="$(printf '%s\n%s' \
  "$T/mirror-reuse/pfm-dev-bin opencode check $REPO_ROOT --home $T/mirror-reuse/opencode-verify-home" \
  "$T/mirror-reuse/pfm-dev-bin opencode doctor $REPO_ROOT --home $T/mirror-reuse/opencode-verify-home")"
if [ ! -s "$T/go.calls" ] && [ "$FAILURES" -eq 0 ] && [ "$(cat "$MIRROR_CALLS")" = "$want" ] && cmp -s "$T/prebuilt-pfm" "$T/mirror-reuse/pfm-dev-bin" &&
  grep -Fq 'opencode mirror current and parseable' "$T/mirror-reuse.out"; then
  ok 'the opencode mirror copies the prebuilt pfm to its scratch binary, then checks and doctors with it'
else bad 'mirrors reuse' "$(cat "$T/go.calls")" "$(cat "$MIRROR_CALLS")" "$(cat "$T/mirror-reuse.out")"; fi
for given in none-given "$T/no-such-pfm"; do
  : > "$T/go.calls"; : > "$MIRROR_CALLS"; FAILURES=0
  scratch="$T/mirror-build-$(basename "$given")"
  # The stub go builds by writing the logging pfm to the -o path.
  if [ "$given" = none-given ]; then args=(); else args=("$given"); fi
  (
    go() { printf 'go %s\n' "$*" >> "$T/go.calls"; cp "$T/prebuilt-pfm" "${@: -2:1}"; }
    PFM_TEST_TIMING_DIR="$scratch" checks_templates_mirrors_opencode ${args[@]+"${args[@]}"}
  ) >"$T/mirror-build.out" 2>&1
  if [ "$(cat "$T/go.calls")" = "go -C pfm build -o $scratch/pfm-dev-bin ./cmd/pfm" ] && [ "$(wc -l < "$MIRROR_CALLS" | tr -d ' ')" -eq 2 ] &&
    grep -Fq 'opencode mirror current and parseable' "$T/mirror-build.out"; then
    ok "the opencode mirror builds its scratch binary when no pfm is given ($(basename "$given"))"
  else bad "mirrors build ($given)" "$(cat "$T/go.calls")" "$(cat "$MIRROR_CALLS")" "$(cat "$T/mirror-build.out")"; fi
done
# checks_templates_mirrors hands its argument on to the opencode check.
(
  checks_templates_mirrors_generate() { :; }; checks_templates_mirrors_marker() { :; }; checks_templates_mirrors_roster() { :; }
  checks_templates_mirrors_manifest() { :; }
  checks_templates_mirrors_opencode() { printf 'opencode-arg [%s]\n' "$*"; }
  checks_templates_mirrors "$T/some-pfm"; checks_templates_mirrors
) >"$T/mirrors-args.out" 2>&1
if [ "$(cat "$T/mirrors-args.out")" = "$(printf 'opencode-arg [%s]\nopencode-arg []' "$T/some-pfm")" ]; then
  ok 'checks_templates_mirrors passes its pfm argument to the opencode check, and none when called bare'
else bad 'mirrors argument' "$(cat "$T/mirrors-args.out")"; fi

# checks_templates_unit_script: one suite of .claude/scripts under its own label; a red suite is run's FAIL, so its status passes through.
out="$( ( run() { printf 'run [%s]\n' "$*"; }; head_() { printf 'head [%s]\n' "$*"; }; REPO_ROOT=/repo; checks_templates_unit_script unit-path ) 2>&1 )"
if [ "$out" = "$(printf 'head [templates — test-unit-path.sh self-test]\nrun [templates: test-unit-path.sh self-test -- bash /repo/scripts/test-unit-path.sh]')" ]; then
  ok 'checks_templates_unit_script runs bash scripts/test-<name>.sh under the label "templates: test-<name>.sh self-test"'
else bad 'checks_templates_unit_script run' "$out"; fi
out="$( ( run() { return 3; }; head_() { :; }; REPO_ROOT=/repo; checks_templates_unit_script check-pfm; echo "rc=$?" ) 2>&1 )"
if [ "$out" = 'rc=3' ]; then ok "checks_templates_unit_script returns run's status, a red suite is not swallowed"
else bad 'checks_templates_unit_script red suite' "$out"; fi

# checks_templates (CI's iso test templates): the five suites follow format-md, in order, before the opencode mirror check.
out="$( (
  for f in $(declare -F | awk '{print $3}' | grep '^checks_templates_'); do
    eval "$f() { printf '%s%s\\n' '$f' \"\${*:+ \$*}\"; }"
  done
  checks_templates
) 2>&1 | sed -n '/^checks_templates_format_md$/,/^checks_templates_mirrors_opencode$/p' | paste -sd, -)"
if [ "$out" = 'checks_templates_format_md,checks_templates_unit_script check-pfm,checks_templates_unit_script check-templates,checks_templates_unit_script test-pfm,checks_templates_unit_script test-templates,checks_templates_unit_script unit-path,checks_templates_mirrors_opencode' ]; then
  ok 'checks_templates calls the five unit-script suites in order, right after format-md'
else bad 'checks_templates serial list' "$out"; fi

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

shtest_end
