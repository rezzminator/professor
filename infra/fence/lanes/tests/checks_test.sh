#!/usr/bin/env bash
set -uo pipefail
SHTEST_TAG=checks-test
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
SUT="${CHECKS_SUT:-$(dirname -- "${BASH_SOURCE[0]}")/../../checks.sh}"
source "$SUT"

REPO_ROOT="$(cd "$(dirname -- "${BASH_SOURCE[0]}")/../../../.." && pwd)"
TMP_BASE="$T"
FAILURES=0
cat > "$T/budgets.yml" <<'YAML'
tolerance: 1.25
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
if ! out="$(gate_budget_verdict pfm 12.1 "$T/budgets.yml")" &&
  [[ "$out" == *'13s over budget 10s ×1.25 = 12s'* ]]; then
  ok 'pinned budget rejects wall above tolerance'
else bad 'pinned over budget' "$out"; fi
if out="$(gate_budget_verdict pfm 12 "$T/budgets.yml")" &&
  [[ "$out" == *'12s within 12s (budget 10s ×1.25)'* ]]; then
  ok 'pinned budget accepts wall at tolerance'
else bad 'pinned within budget' "$out"; fi
if ! out="$(gate_budget_verdict pfm 1 "$T/no-budget.yml")" &&
  [[ "$out" == *"$T/no-budget.yml"* ]]; then
  ok 'unreadable budget names its path and is red'
else bad 'unreadable budget' "$out"; fi
if ! out="$(gate_budget_verdict pfm '' "$T/budgets.yml")" &&
  [[ "$out" == *'✗ gate(pfm) — no wall time read'* ]]; then
  ok 'an unread wall is red, never 0s within budget'
else bad 'unread wall' "$out"; fi
printf 'tolerance: abc\ngate:\n  pfm: 10\n' > "$T/bad-tolerance.yml"
if ! out="$(gate_budget_verdict pfm 1 "$T/bad-tolerance.yml")" &&
  [[ "$out" == *"invalid tolerance abc in $T/bad-tolerance.yml"* ]]; then
  ok 'a non-numeric tolerance is named, never judged as a limit of 0'
else bad 'invalid tolerance' "$out"; fi

need_tool() { return 0; }
timing_run_dir() { mkdir -p "$1"; mktemp -d "$1/run.XXXXXX"; }
steps_reset() { : > "$T/registered"; : > "$T/heavy"; }
steps_add() { printf '%s\n' "$1" >> "$T/registered"; }
steps_add_heavy() { steps_add "$@"; printf '%s\n' "$1" >> "$T/heavy"; }
steps_barrier() { printf 'BARRIER\n' >> "$T/registered"; }
steps_run() {
  printf 'step\tverdict\tseconds\nWALL\tPASS\t0.1\n' > "$1/gate.tsv"
  return "${STUB_STEPS_RC:-0}"
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
pfm_static='pfm.lint-new,pfm.vet,pfm.vet-darwin,pfm.fmt-check,pfm.arch'
templates_static='templates.check-map,templates.clone,templates.leak,templates.placeholders,templates.scratch-paths,templates.descriptions,templates.mirrors,templates.token-audit,templates.flight-index,templates.release-check,templates.codex-sync,templates.refresh-scope,templates.pfm-guard,templates.dev-report,templates.opencode-writer-tests,templates.skill-tests,templates.opencode-writer-refs'
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

if gate_run pfm >"$T/pfm.out" &&
  [ "$(sed -n '1,2p' "$T/registered" | paste -sd, -)" = 'pfm.unit,pfm.e2e' ] &&
  [ "$(static_names)" = "$pfm_static" ] &&
  [ "$(sort "$T/heavy" | paste -sd, -)" = 'pfm.e2e,pfm.fmt-check,pfm.lint-new,pfm.unit,pfm.vet,pfm.vet-darwin' ] &&
  graph_order pfm; then
  ok 'pfm graph runs its tests before static checks'
else bad 'pfm graph' "$(cat "$T/registered")"; fi

if gate_run templates >"$T/templates.out" &&
  [ "$(static_names)" = "$templates_static" ] &&
  [ "$(cat "$T/heavy")" = templates.check-map ] &&
  graph_order templates; then
  ok 'templates graph runs its tests before static checks'
else bad 'templates graph' "$(cat "$T/registered")"; fi

STUB_STEPS_RC=1
if ! gate_run all >"$T/red.out"; then
  ok 'red step makes gate_run red'
else bad 'red step was lost'; fi
unset STUB_STEPS_RC

cat > "$T/gate-budget.yml" <<'YAML'
tolerance: 1.25
gate:
  pfm: 0
YAML
if ! gate_budget_verdict pfm 1 "$T/gate-budget.yml" >"$T/budget-red.out"; then
  ok 'red budget makes budget verdict non-zero'
else bad 'red budget was lost'; fi
gate_budget_verdict() { printf 'budget: forced red\n'; return 1; }
if ! gate_run all >"$T/gate-budget-red.out"; then
  ok 'red budget makes gate_run red'
else bad 'gate_run lost red budget'; fi

PFM_TEST_TIMING_DIR="$T/history"
mkdir -p "$PFM_TEST_TIMING_DIR"/{run.old,run.new,run.invalid,run.current}
printf '%s\n' '{"Test":"TestOld","Action":"pass"}' > "$PFM_TEST_TIMING_DIR/run.old/unit.json"
printf '%s\n' '{"Test":"TestNew","Action":"pass"}' > "$PFM_TEST_TIMING_DIR/run.new/unit.json"
printf '%s\n' '{"Action":"pass"}' > "$PFM_TEST_TIMING_DIR/run.invalid/unit.json"
touch -t 202001010000 "$PFM_TEST_TIMING_DIR/run.old/unit.json"
touch -t 202101010000 "$PFM_TEST_TIMING_DIR/run.new/unit.json"
touch -t 202201010000 "$PFM_TEST_TIMING_DIR/run.invalid/unit.json"
make() { printf '%s\n' '-race'; }
run() { printf '%s\n' "$*" >> "$T/unit.calls"; }
go_test_report() { :; }
skip_gate() { :; }
checks_pfm_unit "$REPO_ROOT/pfm" "$PFM_TEST_TIMING_DIR/run.current"
if grep -Fq "test-shard.sh run --out $PFM_TEST_TIMING_DIR/run.current/unit.json --history $PFM_TEST_TIMING_DIR/run.new/unit.json -- -race" "$T/unit.calls"; then
  ok 'unit row uses sharder with newest usable history and TESTFLAGS'
else bad 'unit row or history selection' "$(cat "$T/unit.calls")"; fi

shtest_end
