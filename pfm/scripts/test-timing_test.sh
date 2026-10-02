#!/usr/bin/env bash
# Fixture-driven regression tests for scripts/test-timing.sh.
#
# scripts/arch-check.sh — the sibling this file was asked to mirror — has no
# dedicated shell test anywhere in this repo's git history (checked across
# every branch and worktree: `git log --all --name-only -- 'pfm/scripts/*'`
# lists only arch-check.sh itself; its own behaviour is instead hand-verified
# once and recorded in prose, docs/dev/pfm-architecture.md:166). There is
# nothing to mirror there, so this file borrows the bash assert_-style harness
# already used in pfm/testdata/e2e.sh (mktemp -d jail, plain `[[ ]]`
# assertions, a trap-cleaned scratch dir) instead.
#
# The attribution cases that need the contention judge read a fixture thresholds
# file ($T/judge-pfm/.testcontention.yml, through run_judged), never
# pfm/.testcontention.yml, so a calibration of that file turns none of them red;
# the real file is read only by attribution-real-thresholds-accepted.
#
# Run directly: bash scripts/test-timing_test.sh
# Or via: bash scripts/test-timing.sh --self-test
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="$ROOT/scripts/test-timing.sh"
SHTEST_TAG=pfm-test-timing-test
# shellcheck source=/dev/null
source "$ROOT/../scripts/shtest.sh"

# ---- fixtures --------------------------------------------------------------

good_json() {
  cat <<'JSON'
{"Time":"2024-01-01T00:00:00.000000000Z","Action":"run","Package":"github.com/rezzminator/professor/pfm/internal/quick","Test":"TestOK"}
{"Time":"2024-01-01T00:00:00.100000000Z","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/quick","Test":"TestOK","Elapsed":0.1}
{"Time":"2024-01-01T00:00:00.110000000Z","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/quick","Elapsed":0.1}
JSON
}

over_budget_json() {
  cat <<'JSON'
{"Time":"2024-01-01T00:00:00.000000000Z","Action":"run","Package":"github.com/rezzminator/professor/pfm/internal/quick","Test":"TestOK"}
{"Time":"2024-01-01T00:00:00.100000000Z","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/quick","Test":"TestOK","Elapsed":0.1}
{"Time":"2024-01-01T00:00:00.110000000Z","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/quick","Elapsed":0.1}
{"Time":"2024-01-01T00:00:00.000000000Z","Action":"run","Package":"github.com/rezzminator/professor/pfm/internal/slow","Test":"TestSlow"}
{"Time":"2024-01-01T00:00:09.000000000Z","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/slow","Test":"TestSlow","Elapsed":9.0}
{"Time":"2024-01-01T00:00:09.010000000Z","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/slow","Elapsed":9.0}
JSON
}

unbudgeted_json() {
  cat <<'JSON'
{"Time":"2024-01-01T00:00:00.000000000Z","Action":"run","Package":"github.com/rezzminator/professor/pfm/internal/mystery","Test":"TestM"}
{"Time":"2024-01-01T00:00:00.200000000Z","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/mystery","Test":"TestM","Elapsed":0.2}
{"Time":"2024-01-01T00:00:00.210000000Z","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/mystery","Elapsed":0.2}
JSON
}

# quick passes fast; slow FAILS (but well inside its own generous budget) —
# a failing run must never read as "within budget".
failing_json() {
  cat <<'JSON'
{"Time":"2024-01-01T00:00:00.000000000Z","Action":"run","Package":"github.com/rezzminator/professor/pfm/internal/quick","Test":"TestOK"}
{"Time":"2024-01-01T00:00:00.100000000Z","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/quick","Test":"TestOK","Elapsed":0.1}
{"Time":"2024-01-01T00:00:00.110000000Z","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/quick","Elapsed":0.1}
{"Time":"2024-01-01T00:00:00.000000000Z","Action":"run","Package":"github.com/rezzminator/professor/pfm/internal/slow","Test":"TestSlow"}
{"Time":"2024-01-01T00:00:00.500000000Z","Action":"fail","Package":"github.com/rezzminator/professor/pfm/internal/slow","Test":"TestSlow","Elapsed":0.5}
{"Time":"2024-01-01T00:00:00.510000000Z","Action":"fail","Package":"github.com/rezzminator/professor/pfm/internal/slow","Elapsed":0.5}
JSON
}

# One suite ("u") carries both fixture packages, nested under suites.u —
# every --check test below names --suite u so its completeness check (every
# BUDGETED package of suite "u" seen) resolves the same way each time.
sample_yml() {
  cat <<'YML'
# fixture budgets
tolerance: 1.25
fail_factor: 2
suites:
  u:
    wall_s: 5
    packages:
      github.com/rezzminator/professor/pfm/internal/quick: 5
      github.com/rezzminator/professor/pfm/internal/slow: 2
YML
}

# --measure's own concurrency guard (check_fence_free) reads the REAL `docker
# ps` — a fixture run must never depend on whether some unrelated fence
# container happens to be live on the host right now. NODOCK is a PATH with
# every tool test-timing.sh actually shells out to, symlinked in, MINUS
# docker: the guard's own "docker not on PATH — skipping" branch then kicks
# in deterministically, exactly like inside the real fence container.
NODOCK="$T/nodock-bin"
mkdir -p "$NODOCK"
for tool in bash jq python3 awk sed sort date mktemp cat grep wc mv rm mkdir cut tr head tail xargs comm dirname basename; do
  real="$(command -v "$tool" 2>/dev/null)" || continue
  ln -sf "$real" "$NODOCK/$tool"
done
run_sut() { env PATH="$NODOCK" bash "$SUT" "$@"; }

# ---- 1: --check on an over-budget package exits 1, names it ---------------

yml="$T/timing.yml"
sample_yml > "$yml"
out="$T/1.out"
if over_budget_json | run_sut --check --yml "$yml" --suite u --out "$T/1.tsv" >"$out" 2>&1; then
  bad "check-over-budget: expected exit 1" "$(cat "$out")"
else
  rc=$?
  if [ "$rc" -eq 1 ] && grep -q 'FAIL github.com/rezzminator/professor/pfm/internal/slow' "$out"; then
    ok "check-over-budget: exit 1, names github.com/rezzminator/professor/pfm/internal/slow"
  else
    bad "check-over-budget: rc=$rc" "$(cat "$out")"
  fi
fi

# ---- 2: --check on a package absent from the budget file: UNBUDGETED ------

out="$T/2.out"
if unbudgeted_json | run_sut --check --yml "$yml" --suite u --out "$T/2.tsv" >"$out" 2>&1; then
  bad "check-unbudgeted: expected exit 1" "$(cat "$out")"
else
  rc=$?
  if [ "$rc" -eq 1 ] && grep -q 'UNBUDGETED github.com/rezzminator/professor/pfm/internal/mystery' "$out"; then
    ok "check-unbudgeted: exit 1, UNBUDGETED names github.com/rezzminator/professor/pfm/internal/mystery"
  else
    bad "check-unbudgeted: rc=$rc" "$(cat "$out")"
  fi
fi

# ---- 3: --check on a clean, COMPLETE run exits 0 and confirms the SUITE ---

full_json() { good_json; printf '{"Time":"2024-01-01T00:00:00.510000000Z","Action":"run","Package":"github.com/rezzminator/professor/pfm/internal/slow","Test":"TestSlow"}\n{"Time":"2024-01-01T00:00:01.510000000Z","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/slow","Test":"TestSlow","Elapsed":1.0}\n{"Time":"2024-01-01T00:00:01.520000000Z","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/slow","Elapsed":1.0}\n'; }
out="$T/3.out"
if full_json | run_sut --check --yml "$yml" --suite u --out "$T/3.tsv" >"$out" 2>"$T/3.err"; then
  if grep -q 'SUITE(u) within budget' "$out"; then
    ok "check-clean: exit 0, SUITE(u) confirmed within budget on a complete run"
  else
    bad "check-clean: exit 0 but no SUITE confirmation" "$(cat "$out")"
  fi
else
  bad "check-clean: expected exit 0" "$(cat "$out")" "$(cat "$T/3.err")"
fi

# ---- 4: unparsable input is TIMING-UNREADABLE, exit 2 ----------------------

out="$T/4.out"
if printf 'not json\nat all\n' | run_sut --check --yml "$yml" --suite u --out "$T/4.tsv" >"$T/4.log" 2>"$out"; then
  bad "unreadable: expected non-zero exit" "$(cat "$out")"
else
  rc=$?
  if [ "$rc" -eq 2 ] && grep -q 'TIMING-UNREADABLE' "$out"; then
    ok "unreadable: exit 2, TIMING-UNREADABLE"
  else
    bad "unreadable: rc=$rc" "$(cat "$out")"
  fi
fi

# ---- 5: default mode (no --check) emits the TSV shape ---------------------

out="$T/5.tsv"
if good_json | run_sut --suite fixture5 --out "$out" >"$T/5.log" 2>&1; then
  header="$(head -1 "$out")"
  suite_row="$(grep '^SUITE' "$out" || true)"
  pkg_row="$(grep 'github.com/rezzminator/professor/pfm/internal/quick' "$out" || true)"
  if [ "$header" = "$(printf 'package\twall_s\ttests\tparallel_tests\tslowest_test\tslowest_s\tstatus')" ] \
    && [ -n "$suite_row" ] && [ -n "$pkg_row" ]; then
    ok "default: TSV header + package row + SUITE row present"
  else
    bad "default: TSV shape wrong" "header=[$header]" "suite=[$suite_row]" "pkg=[$pkg_row]"
  fi
else
  bad "default: expected exit 0" "$(cat "$T/5.log")"
fi

# ---- 6: an explicit "-" still means stdin -------------------------------

out="$T/5stdin.out"
if good_json | run_sut --suite fixture5 --out "$T/5stdin.tsv" - >"$out" 2>&1; then
  if grep -q '^SUITE' "$T/5stdin.tsv"; then
    ok "stdin-dash: explicit '-' reads the JSON stream from stdin"
  else
    bad "stdin-dash: TSV is missing its suite row" "$(cat "$T/5stdin.tsv")"
  fi
else
  bad "stdin-dash: expected exit 0" "$(cat "$out")"
fi

# ---- 6: a FAILING run never reads as "within budget" -----------------------

out="$T/6check.out"
if failing_json | run_sut --check --yml "$yml" --suite u --out "$T/6.tsv" >"$out" 2>&1; then
  bad "check-failing: expected non-zero exit on a failing run" "$(cat "$out")"
else
  rc=$?
  if [ "$rc" -eq 1 ] && grep -q 'TIMING TESTS-FAILED' "$out" && ! grep -qi 'within budget' "$out"; then
    ok "check-failing: TIMING TESTS-FAILED, never a budget verdict"
  else
    bad "check-failing: rc=$rc, wrong message shape" "$(cat "$out")"
  fi
fi

# ---- 7: --measure ratchets a package DOWN when all 3 runs beat its budget -

meas_yml="$T/measure.yml"
sample_yml > "$meas_yml"
out="$T/7.out"
if run_sut --yml "$meas_yml" --suite u --measure \
    <(good_json) <(good_json) <(good_json) >"$out" 2>&1; then
  new_budget="$(awk -F': ' '/github.com\/rezzminator\/professor\/pfm\/internal\/quick:/{print $2}' "$meas_yml")"
  if [ "$new_budget" = "1" ] && grep -q 'TIMING MEASURE github.com/rezzminator/professor/pfm/internal/quick 5s -> 1s' "$out"; then
    ok "measure: ratchets github.com/rezzminator/professor/pfm/internal/quick down to its ceil(median)"
  else
    bad "measure: expected quick ratcheted to 1s, got [$new_budget]" "$(cat "$out")" "$(cat "$meas_yml")"
  fi
else
  bad "measure: expected exit 0" "$(cat "$out")"
fi
# github.com/rezzminator/professor/pfm/internal/slow was NOT in any of these 3 runs — its budget must
# survive untouched, never dropped or zeroed.
if grep -q 'github.com/rezzminator/professor/pfm/internal/slow: 2' "$meas_yml"; then
  ok "measure: untouched package keeps its existing budget"
else
  bad "measure: github.com/rezzminator/professor/pfm/internal/slow budget changed or vanished" "$(cat "$meas_yml")"
fi

# ---- 8: --measure never RAISES a budget, and leaves OTHER suites alone ----

raise_yml="$T/raise.yml"
cat > "$raise_yml" <<'YML'
tolerance: 1.25
fail_factor: 2
suites:
  u:
    wall_s: 5
    packages:
      github.com/rezzminator/professor/pfm/internal/quick: 1
  e2e:
    wall_s: 99
    packages:
      github.com/rezzminator/professor/pfm/e2e: 99
YML
out="$T/8.out"
run_sut --yml "$raise_yml" --suite u --measure <(good_json) <(good_json) <(good_json) >"$out" 2>&1
after="$(awk -F': ' '/github.com\/rezzminator\/professor\/pfm\/internal\/quick:/{print $2}' "$raise_yml")"
if [ "$after" = "1" ]; then
  ok "measure: never raises an existing budget (stayed at 1s)"
else
  bad "measure: budget was raised to [$after]" "$(cat "$out")"
fi
if grep -q 'github.com/rezzminator/professor/pfm/e2e: 99' "$raise_yml"; then
  ok "measure: an unmeasured sibling suite (e2e) survives untouched"
else
  bad "measure: sibling suite e2e was dropped or changed" "$(cat "$raise_yml")"
fi

# ---- 9: --measure bootstraps a fresh file when none exists ----------------

boot_yml="$T/bootstrap.yml"
out="$T/9.out"
if run_sut --yml "$boot_yml" --suite u --measure <(good_json) <(good_json) <(good_json) >"$out" 2>&1; then
  if [ -f "$boot_yml" ] && grep -q 'github.com/rezzminator/professor/pfm/internal/quick: 1' "$boot_yml" && grep -q '^tolerance:' "$boot_yml"; then
    ok "measure: bootstraps a fresh budget file"
  else
    bad "measure: bootstrap file missing or malformed" "$(cat "$boot_yml" 2>&1)"
  fi
else
  bad "measure: bootstrap expected exit 0" "$(cat "$out")"
fi

# ---- 10: a package without its terminal summary is incomplete ------------

incomplete_json() {
  cat <<'JSON'
{"Time":"2024-01-01T00:00:00.000000000Z","Action":"run","Package":"github.com/rezzminator/professor/pfm/internal/quick","Test":"TestOK"}
{"Time":"2024-01-01T00:00:00.100000000Z","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/quick","Test":"TestOK","Elapsed":0.1}
JSON
}
out="$T/10.out"
if incomplete_json | run_sut --check --yml "$yml" --suite u --out "$T/10.tsv" >"$out" 2>&1; then
  bad "incomplete-summary: expected non-zero exit" "$(cat "$out")"
else
  rc=$?
  if [ "$rc" -eq 2 ] && grep -q 'TIMING-INCOMPLETE' "$out"; then
    ok "incomplete-summary: terminal package summary is required"
  else
    bad "incomplete-summary: rc=$rc" "$(cat "$out")"
  fi
fi

# ---- 11: malformed JSON-looking input is unreadable, never dropped -------

out="$T/11.out"
if { good_json; printf '{"Time":"broken"\n'; } | run_sut --check --yml "$yml" --suite u --out "$T/11.tsv" >"$out" 2>&1; then
  bad "malformed-json: expected non-zero exit" "$(cat "$out")"
else
  rc=$?
  if [ "$rc" -eq 2 ] && grep -q 'TIMING-UNREADABLE' "$out"; then
    ok "malformed-json: JSON-shaped corruption is reported as TIMING-UNREADABLE"
  else
    bad "malformed-json: rc=$rc" "$(cat "$out")"
  fi
fi

# ---- 12: --measure test failures do not mutate budgets --------------------

failed_measure_yml="$T/12.yml"
sample_yml > "$failed_measure_yml"
cp "$failed_measure_yml" "$T/12.before"
out="$T/12.out"
if run_sut --yml "$failed_measure_yml" --suite u --measure \
    <(failing_json) <(failing_json) <(failing_json) >"$out" 2>&1; then
  bad "measure-failing: expected non-zero exit" "$(cat "$out")"
else
  rc=$?
  if [ "$rc" -eq 1 ] && grep -q 'TIMING TESTS-FAILED' "$out" && cmp -s "$failed_measure_yml" "$T/12.before"; then
    ok "measure-failing: exits 1 and leaves the budget file byte-for-byte unchanged"
  else
    bad "measure-failing: rc=$rc or budget mutated" "$(cat "$out")" "$(diff -u "$T/12.before" "$failed_measure_yml" || true)"
  fi
fi

# ---- 13: differing package sets across captures are incomplete ------------

partial_measure_yml="$T/13.yml"
sample_yml > "$partial_measure_yml"
cp "$partial_measure_yml" "$T/13.before"
out="$T/13.out"
if run_sut --yml "$partial_measure_yml" --suite u --measure \
    <(good_json) <(full_json) <(good_json) >"$out" 2>&1; then
  bad "measure-differing-packages: expected non-zero exit" "$(cat "$out")"
else
  rc=$?
  if [ "$rc" -eq 1 ] && grep -q 'TIMING-INCOMPLETE' "$out" && cmp -s "$partial_measure_yml" "$T/13.before"; then
    ok "measure-differing-packages: exits 1 and does not mutate budgets"
  else
    bad "measure-differing-packages: rc=$rc or budget mutated" "$(cat "$out")" "$(diff -u "$T/13.before" "$partial_measure_yml" || true)"
  fi
fi

# ---- 14: invalid numeric YAML is a configuration error --------------------

bad_numeric_yml="$T/14.yml"
cat > "$bad_numeric_yml" <<'YML'
tolerance: NaN
suites:
  u:
    wall_s: 5
    packages:
      github.com/rezzminator/professor/pfm/internal/quick: -1
YML
out="$T/14.out"
if good_json | run_sut --check --yml "$bad_numeric_yml" --suite u --out "$T/14.tsv" >"$out" 2>&1; then
  bad "invalid-yaml-number: expected non-zero exit" "$(cat "$out")"
else
  rc=$?
if [ "$rc" -eq 2 ] && grep -q 'TIMING-CONFIG-INVALID' "$out"; then
    ok "invalid-yaml-number: nonfinite and negative values are rejected"
  else
    bad "invalid-yaml-number: rc=$rc" "$(cat "$out")"
  fi
fi

# ---- 15: a Docker probe error is distinct from no running container --------

dockerfail="$T/dockerfail-bin"
mkdir -p "$dockerfail"
cp -a "$NODOCK/." "$dockerfail/"
cat > "$dockerfail/docker" <<'SH'
#!/usr/bin/env bash
echo docker-daemon-unreachable >&2
exit 42
SH
chmod +x "$dockerfail/docker"
run_sut_dockerfail() { env PATH="$dockerfail" bash "$SUT" "$@"; }
out="$T/15.out"
if run_sut_dockerfail --yml "$yml" --suite u --measure \
    <(good_json) <(good_json) <(good_json) >"$out" 2>&1; then
  bad "docker-probe: expected non-zero exit" "$(cat "$out")"
else
  rc=$?
  if [ "$rc" -eq 2 ] && grep -q 'TIMING-ERROR: docker ps probe failed' "$out"; then
    ok "docker-probe: Docker probe failure is explicit and cannot look like an empty guard"
  else
    bad "docker-probe: rc=$rc or probe error missing" "$(cat "$out")"
  fi
fi

# ---- 16: measuring an absent suite is a config error and cannot write ------

absent_suite_yml="$T/16.yml"
cat > "$absent_suite_yml" <<'YML'
tolerance: 1.25
fail_factor: 2
suites:
  e2e:
    wall_s: 99
    packages:
      github.com/rezzminator/professor/pfm/e2e: 99
YML
cp "$absent_suite_yml" "$T/16.before"
out="$T/16.out"
if run_sut --yml "$absent_suite_yml" --suite u --measure \
    <(good_json) <(good_json) <(good_json) >"$out" 2>&1; then
  bad "measure-absent-suite: expected non-zero exit" "$(cat "$out")"
else
  rc=$?
  if [ "$rc" -eq 2 ] && grep -q 'TIMING-CONFIG-INVALID' "$out" && cmp -s "$absent_suite_yml" "$T/16.before"; then
    ok "measure-absent-suite: missing suite is explicit and leaves budgets unchanged"
  else
    bad "measure-absent-suite: rc=$rc or budget mutated" "$(cat "$out")" "$(diff -u "$T/16.before" "$absent_suite_yml" || true)"
  fi
fi

# ---- 17: chatter plus malformed JSON cannot be dropped into a green run ---

out="$T/17.out"
if {
  full_json
  printf 'build chatter line 1\n'
  printf 'build chatter line 2\n'
  printf 'build chatter line 3\n'
  printf 'build chatter line 4\n'
  printf 'build chatter line 5\n'
  printf 'build chatter line 6\n'
  printf 'build chatter line 7\n'
  printf 'build chatter line 8\n'
  printf '{"Time":"broken"\n'
} | run_sut --check --yml "$yml" --suite u --out "$T/17.tsv" >"$out" 2>&1; then
  bad "mixed-malformed: expected non-zero exit" "$(cat "$out")"
else
  rc=$?
  if [ "$rc" -eq 2 ] && grep -q 'TIMING-UNREADABLE' "$out"; then
    ok "mixed-malformed: chatter and malformed JSON are never dropped"
  else
    bad "mixed-malformed: rc=$rc" "$(cat "$out")"
  fi
fi

# ---- 18: every timestamp is validated, including interior events ----------

out="$T/18.out"
if {
  printf '{"Time":"2024-01-01T00:00:00.000000000Z","Action":"run","Package":"github.com/rezzminator/professor/pfm/internal/quick","Test":"TestOK"}\n'
  printf '{"Time":"2024-01-01T00:00:00.050bad","Action":"output","Package":"github.com/rezzminator/professor/pfm/internal/quick","Output":"fixture"}\n'
  printf '{"Time":"2024-01-01T00:00:00.100000000Z","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/quick","Test":"TestOK","Elapsed":0.1}\n'
  printf '{"Time":"2024-01-01T00:00:00.110000000Z","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/quick","Elapsed":0.1}\n'
} | run_sut --check --yml "$yml" --suite u --out "$T/18.tsv" >"$out" 2>&1; then
  bad "interior-timestamp: expected non-zero exit" "$(cat "$out")"
else
  rc=$?
  if [ "$rc" -eq 2 ] && grep -q 'TIMING-UNREADABLE' "$out"; then
    ok "interior-timestamp: invalid timestamps cannot hide between valid endpoints"
  else
    bad "interior-timestamp: rc=$rc" "$(cat "$out")"
  fi
fi

# ---- 19: no-test package skips pass; skipped tests remain named failures --

skip_yml="$T/19.yml"
cat > "$skip_yml" <<'YML'
tolerance: 1.25
fail_factor: 2
suites:
  u:
    wall_s: 1
    packages:
      github.com/rezzminator/professor/pfm/internal/empty: 1
YML
out="$T/19.out"
if printf '{"Time":"2024-01-01T00:00:00Z","Action":"skip","Package":"github.com/rezzminator/professor/pfm/internal/empty","Elapsed":0}\n' \
    | run_sut --check --yml "$skip_yml" --suite u --out "$T/19.tsv" >"$out" 2>&1; then
  if grep -q $'github.com/rezzminator/professor/pfm/internal/empty\t0\t0\t' "$T/19.tsv" && grep -q 'SUITE(u) within budget' "$out" \
      && ! grep -q 'TIMING TESTS-FAILED' "$out"; then
    ok "skip-no-tests: no-test package skip is a passing zero-cost row"
  else
    bad "skip-no-tests: legitimate no-test package was rejected" "$(cat "$out")" "$(cat "$T/19.tsv")"
  fi
else
  bad "skip-no-tests: expected exit 0" "$(cat "$out")"
fi

out="$T/20.out"
if {
  printf '{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"github.com/rezzminator/professor/pfm/internal/quick","Test":"TestSkipped"}\n'
  printf '{"Time":"2024-01-01T00:00:00Z","Action":"skip","Package":"github.com/rezzminator/professor/pfm/internal/quick","Test":"TestSkipped","Elapsed":0}\n'
  printf '{"Time":"2024-01-01T00:00:00Z","Action":"skip","Package":"github.com/rezzminator/professor/pfm/internal/quick","Elapsed":0}\n'
} | run_sut --check --yml "$yml" --suite u --out "$T/20.tsv" >"$out" 2>&1; then
  bad "skip-test: expected non-zero exit" "$(cat "$out")"
else
  rc=$?
  if [ "$rc" -eq 1 ] && grep -q 'TIMING TESTS-FAILED' "$out" && grep -q 'github.com/rezzminator/professor/pfm/internal/quick (skip)' "$out"; then
    ok "skip-test: skipped test is named and cannot pass the timing gate"
  else
    bad "skip-test: rc=$rc or skipped test was not named" "$(cat "$out")"
  fi
fi

over_budget_json >"$T/load.json"
load_record() {
  printf 'epoch_s\tvm_busy_s\town_s\tcpus\n1704067199\t0\t0\t2\n1704067211\t%s\t0\t2\n' "$1" >"$T/load.load"
}
load_record 19.2
rc=0; run_sut --check --yml "$yml" --suite u --out "$T/load.tsv" "$T/load.json" >"$T/load.out" 2>&1 || rc=$?
if [ "$rc" -eq 0 ] && grep -q 'TIMING CORRECTED github.com/rezzminator/professor/pfm/internal/slow .*other-load=80% limit=12.500s' "$T/load.out" && grep -q 'TIMING CORRECTED SUITE(u).*other-load=80% limit=31.250s' "$T/load.out" && grep -q 'within the load-corrected limit' "$T/load.out"; then ok load-corrected; else bad "load-corrected: rc=$rc" "$(cat "$T/load.out")"; fi

load_record 4.8
rc=0; run_sut --check --yml "$yml" --suite u --out "$T/load.tsv" "$T/load.json" >"$T/load.out" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && grep -q 'TIMING FAIL github.com/rezzminator/professor/pfm/internal/slow .*other-load=20% limit=3.125s' "$T/load.out"; then ok load-unexplained; else bad "load-unexplained: rc=$rc" "$(cat "$T/load.out")"; fi

load_record 0
rc=0; run_sut --check --yml "$yml" --suite u --out "$T/load.tsv" "$T/load.json" >"$T/load.out" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && grep -q 'TIMING FAIL github.com/rezzminator/professor/pfm/internal/slow .*other-load=0% limit=2.500s' "$T/load.out"; then ok load-zero; else bad "load-zero: rc=$rc" "$(cat "$T/load.out")"; fi

printf 'epoch_s\tvm_busy_s\town_s\tcpus\n1704067201\t0\t0\t2\n1704067211\t16\t0\t2\n' >"$T/load.load"
rc=0; run_sut --check --yml "$yml" --suite u --out "$T/load.tsv" "$T/load.json" >"$T/load.out" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && grep -q 'TIMING FAIL github.com/rezzminator/professor/pfm/internal/slow .*other-load=unmeasured' "$T/load.out"; then ok load-uncovered; else bad "load-uncovered: rc=$rc" "$(cat "$T/load.out")"; fi

printf 'epoch_s\tvm_busy_s\town_s\tcpus\nUNAVAILABLE\t/proc/stat: missing\n' >"$T/load.load"
rc=0; run_sut --check --yml "$yml" --suite u --out "$T/load.tsv" "$T/load.json" >"$T/load.out" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && grep -q 'TIMING: other load not measured — /proc/stat: missing; limits uncorrected' "$T/load.out"; then ok load-unavailable; else bad "load-unavailable: rc=$rc" "$(cat "$T/load.out")"; fi

rm "$T/load.load"
rc=0; run_sut --check --yml "$yml" --suite u --out "$T/load.tsv" "$T/load.json" >"$T/load.out" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && grep -q 'TIMING: other load not measured — load record .*load.load missing; limits uncorrected' "$T/load.out"; then ok load-missing; else bad "load-missing: rc=$rc" "$(cat "$T/load.out")"; fi
rc=0; over_budget_json | run_sut --check --yml "$yml" --suite u --out "$T/load.tsv" - >"$T/load.out" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && grep -q 'TIMING: other load not measured — stream read from stdin; limits uncorrected' "$T/load.out"; then ok load-stdin; else bad "load-stdin: rc=$rc" "$(cat "$T/load.out")"; fi

for case in header numeric time cpus; do
  case "$case" in
    header) printf 'bad\n1704067199\t0\t0\t2\n' >"$T/load.load" ;;
    numeric) printf 'epoch_s\tvm_busy_s\town_s\tcpus\n1704067199\tbad\t0\t2\n' >"$T/load.load" ;;
    time) printf 'epoch_s\tvm_busy_s\town_s\tcpus\n1704067199\t0\t0\t2\n1704067199\t1\t0\t2\n' >"$T/load.load" ;;
    cpus) printf 'epoch_s\tvm_busy_s\town_s\tcpus\n1704067199\t0\t0\t0\n' >"$T/load.load" ;;
  esac
  rc=0; run_sut --check --yml "$yml" --suite u --out "$T/load.tsv" "$T/load.json" >"$T/load.out" 2>&1 || rc=$?
  if [ "$rc" -eq 2 ] && grep -q "TIMING-UNREADABLE: load record $T/load.load:" "$T/load.out"; then ok "load-malformed-$case"; else bad "load-malformed-$case: rc=$rc" "$(cat "$T/load.out")"; fi
done

# The fail limit applies to each package and to the suite wall separately.
timed_json() {
  local wall="$1"
  printf '{"Time":"2024-01-01T00:00:00.000000000Z","Action":"run","Package":"github.com/rezzminator/professor/pfm/internal/quick","Test":"TestOK"}\n'
  printf '{"Time":"2024-01-01T00:00:00.%09dZ","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/quick","Test":"TestOK","Elapsed":%s}\n' "$2" "$wall"
  printf '{"Time":"2024-01-01T00:00:00.%09dZ","Action":"pass","Package":"github.com/rezzminator/professor/pfm/internal/quick","Elapsed":%s}\n' "$2" "$wall"
}
for scope in package suite; do
  tier_yml="$T/$scope-tier.yml"
  if [ "$scope" = package ]; then tier_suite=1; tier_pkg=0.1; tier_name='github.com/rezzminator/professor/pfm/internal/quick'
  else tier_suite=0.1; tier_pkg=1; tier_name='SUITE(u)'; fi
  cat > "$tier_yml" <<YML
tolerance: 1.25
fail_factor: 2
suites:
  u:
    wall_s: $tier_suite
    packages:
      github.com/rezzminator/professor/pfm/internal/quick: $tier_pkg
YML
  for tier in warn fail; do
    if [ "$tier" = warn ]; then wall=0.1875; nanos=187500000; expected_rc=0; expected_verdict=WARN
    else wall=0.2625; nanos=262500000; expected_rc=1; expected_verdict=FAIL; fi
    tier_out="$T/$scope-$tier.out"
    rc=0; timed_json "$wall" "$nanos" | run_sut --check --yml "$tier_yml" --suite u --out "$T/$scope-$tier.tsv" > "$tier_out" 2>&1 || rc=$?
    if [ "$rc" -eq "$expected_rc" ] && grep -q "^TIMING $expected_verdict $tier_name .*limit=0.125s fail-at=0.250s" "$tier_out"; then
      if [ "$tier" = warn ] && grep -q "^GATE-WARN 1 timing warning(s): $tier_name" "$tier_out" && grep -q 'over budget within the fail limit (fail at x2)' "$tier_out"; then
        ok "$scope warning stays green and names its fail limit"
      elif [ "$tier" = fail ]; then
        ok "$scope beyond the fail limit is an offender"
      else bad "$scope warning summary or gate marker" "$(cat "$tier_out")"; fi
    else bad "$scope $tier: rc=$rc" "$(cat "$tier_out")"; fi
  done
done

missing_factor_yml="$T/missing-factor.yml"
cat > "$missing_factor_yml" <<'YML'
tolerance: 1.25
suites:
  u:
    wall_s: 1
    packages:
      github.com/rezzminator/professor/pfm/internal/quick: 1
YML
rc=0; timed_json 0.1 100000000 | run_sut --check --yml "$missing_factor_yml" --suite u --out "$T/missing-factor.tsv" > "$T/missing-factor.out" 2>&1 || rc=$?
if [ "$rc" -eq 2 ] && grep -q 'TIMING-CONFIG-INVALID: .* is missing fail_factor' "$T/missing-factor.out"; then
  ok 'missing fail_factor is a named configuration error'
else bad "missing fail_factor: rc=$rc" "$(cat "$T/missing-factor.out")"; fi

# The judge the attribution cases ask: a copy of the real script over the fixture thresholds, handed to
# test-timing.sh as its PFM. REPO stays the real repository root, so PROJECT and the scratch base are as in
# run_sut.
JUDGE_PFM="$T/judge-pfm"
mkdir -p "$JUDGE_PFM/scripts"
cp "$ROOT/scripts/test-contention.sh" "$JUDGE_PFM/scripts/test-contention.sh"
printf '%s\n' 'tick_deficit_max: -0.05' 'cpu_psi_share_min: 0.20' 'spin_ratio_min: 1.5' 'run_delay_ratio_min: 0.5' 'min_samples: 4' \
  > "$JUDGE_PFM/.testcontention.yml"
run_judged() { env PATH="$NODOCK" PFM="$JUDGE_PFM" REPO="$(cd "$ROOT/.." && pwd)" bash "$SUT" "$@"; }

# ---- attribution: every over-limit verdict names what made it slow ---------
# Each case is a directory holding the stream (unit.json) and, beside it, the
# resources.tsv (and profile/) the judge reads; windows come from the stream's
# own event times, 2024-01-01T00:00:SSZ = epoch 1704067200 + SS.
QUICK_PKG=github.com/rezzminator/professor/pfm/internal/quick
SLOW_PKG=github.com/rezzminator/professor/pfm/internal/slow
E2E_PKG=github.com/rezzminator/professor/pfm/e2e
RES_HEADER=$'epoch_s\tuptime_s\tcg_cpu_s\tvm_busy_s\tcpus\tcg_io_read_mb\tcg_io_write_mb\tcg_mem_mb\tcg_mem_peak_mb\tcg_swap_mb\tcg_psi_cpu_some_s\tcg_psi_io_some_s\tcg_psi_io_full_s\tcg_psi_mem_some_s\tcg_psi_mem_full_s\tvm_psi_cpu_some_s\tvm_psi_io_some_s\tvm_psi_io_full_s\tvm_psi_mem_some_s\tvm_psi_mem_full_s\tspin_us\tload1'
mkdir -p "$T/at"

pkg_events() { # pkg_events <pkg> <from_s> <to_s>: one package ran from second <from_s> to second <to_s>
  printf '{"Time":"2024-01-01T00:00:%02dZ","Action":"run","Package":"%s","Test":"TestOK"}\n' "$2" "$1"
  printf '{"Time":"2024-01-01T00:00:%02dZ","Action":"pass","Package":"%s","Test":"TestOK","Elapsed":%s}\n' "$3" "$1" "$(($3 - $2))"
  printf '{"Time":"2024-01-01T00:00:%02dZ","Action":"pass","Package":"%s","Elapsed":%s}\n' "$3" "$1" "$(($3 - $2))"
}
res_rows() { # res_rows <file> <split_epoch> <cg1> <vm1> <cg2> <vm2>: 20 rows one second apart from epoch 1704067195 on 4 cpus;
  # the cgroup (cg) and VM (vm) cpu counters rise at the first rates up to the split epoch and at the second rates after it
  mkdir -p "$(dirname -- "$1")"
  { printf '%s\n' "$RES_HEADER"
    awk -v split_at="$2" -v cg1="$3" -v vm1="$4" -v cg2="$5" -v vm2="$6" 'BEGIN {
      cg = vm = 0
      for (i = 0; i < 20; i++) {
        t = 1704067195 + i
        if (i > 0) { cg += (t <= split_at ? cg1 : cg2); vm += (t <= split_at ? vm1 : vm2) }
        printf "%.6f\t%.6f\t%.6f\t%.6f\t4\t0\t0\t100\t150\t0\t%.6f\t0\t0\t0\t0\t0\t0\t0\t0\t0\t1000\t0.5\n", t, 1000 + i, cg, vm, 0.01 * i
      }
    }'
  } > "$1"
}
res_contention() { res_rows "$1" 0 0 0 3 0.5; } # the VM counted 0.5 cpu-s/s while the cgroup used 3: tick-deficit -62.5 %
res_code() { res_rows "$1" 0 0 0 3 3; }         # the two agree: tick-deficit +0.0 %
attr_yml="$T/attr.yml"
cat > "$attr_yml" <<YML
tolerance: 1.25
fail_factor: 2
suites:
  u:
    wall_s: 100
    packages:
      $QUICK_PKG: 4
YML
attr_run() { # attr_run <dir> <yml> [options…]: --check on <dir>/unit.json; stdout <dir>/out, stderr <dir>/err, status in rc
  local dir="$1" ymlfile="$2"; shift 2
  rc=0; run_judged --check --yml "$ymlfile" --suite u --out "$dir/unit.tsv" "$@" "$dir/unit.json" >"$dir/out" 2>"$dir/err" || rc=$?
}
first_line() { grep -m1 -E -- "$2" "$1" || true; } # first_line <file> <ERE>
CONTENTION_TAIL=' · attribution CONTENTION \(tick-deficit -62\.5% \(≤ -[0-9.]+%\) · .*\)'
CODE_TAIL=' · attribution CODE \(tick-deficit \+0\.0% \(> -[0-9.]+%\) · .* · run-delay NA \(not given\)\)'

# WARN: 7 s against a 5 s limit (budget 4 x 1.25), inside the 10 s fail limit.
d="$T/at/warn-contention"; mkdir -p "$d"; pkg_events "$QUICK_PKG" 0 7 > "$d/unit.json"; res_contention "$d/resources.tsv"
attr_run "$d" "$attr_yml"
if [ "$rc" -eq 0 ] && [ -n "$(first_line "$d/out" "^TIMING WARN $QUICK_PKG .*$CONTENTION_TAIL$")" ] && grep -q "^GATE-WARN 1 timing warning(s): $QUICK_PKG" "$d/out"; then ok attribution-warn-contention; else bad "attribution-warn-contention: rc=$rc" "$(cat "$d/out")" "$(cat "$d/err")"; fi
# The real thresholds file: the same case on a copy of its directory, through the unmodified test-timing.sh and its
# own judge. Whatever values pfm/.testcontention.yml holds, the file is found and accepted: the over-limit line is
# attributed (CODE or CONTENTION) and no judge failed.
d="$T/at/real-thresholds"; cp -R "$T/at/warn-contention" "$d"
rc=0; run_sut --check --yml "$attr_yml" --suite u --out "$d/unit.tsv" "$d/unit.json" >"$d/out" 2>"$d/err" || rc=$?
if [ "$rc" -eq 0 ] && [ -n "$(first_line "$d/out" "^TIMING WARN $QUICK_PKG .* · attribution (CODE|CONTENTION) \(")" ] && ! grep -q 'judge failed' "$d/out" "$d/err"; then ok attribution-real-thresholds-accepted; else bad "attribution-real-thresholds-accepted: rc=$rc" "$(cat "$d/out")" "$(cat "$d/err")"; fi
d="$T/at/warn-code"; mkdir -p "$d"; pkg_events "$QUICK_PKG" 0 7 > "$d/unit.json"; res_code "$d/resources.tsv"
attr_run "$d" "$attr_yml"
if [ "$rc" -eq 0 ] && [ -n "$(first_line "$d/out" "^TIMING WARN $QUICK_PKG .*$CODE_TAIL$")" ] && ! grep -q 'downgraded' "$d/out"; then ok attribution-warn-code; else bad "attribution-warn-code: rc=$rc" "$(cat "$d/out")" "$(cat "$d/err")"; fi

# FAIL: 11 s is past the 10 s fail limit. Contention downgrades it to a counted WARN; code leaves it a FAIL.
d="$T/at/fail-contention"; mkdir -p "$d"; pkg_events "$QUICK_PKG" 0 11 > "$d/unit.json"; res_contention "$d/resources.tsv"
attr_run "$d" "$attr_yml"
if [ "$rc" -eq 0 ] && [ -n "$(first_line "$d/out" "^TIMING WARN $QUICK_PKG .* fail-at=10\.000s$CONTENTION_TAIL"' · downgraded from FAIL$')" ] \
    && grep -q "^GATE-WARN 1 timing warning(s): $QUICK_PKG" "$d/out" && ! grep -q 'TIMING FAIL' "$d/out" "$d/err"; then
  ok attribution-fail-contention-downgraded
else bad "attribution-fail-contention-downgraded: rc=$rc" "$(cat "$d/out")" "$(cat "$d/err")"; fi
d="$T/at/fail-code"; mkdir -p "$d"; pkg_events "$QUICK_PKG" 0 11 > "$d/unit.json"; res_code "$d/resources.tsv"
attr_run "$d" "$attr_yml"
if [ "$rc" -eq 1 ] && [ -n "$(first_line "$d/err" "^TIMING FAIL $QUICK_PKG .*$CODE_TAIL$")" ] && ! grep -q 'TIMING WARN\|GATE-WARN' "$d/out"; then ok attribution-fail-code; else bad "attribution-fail-code: rc=$rc" "$(cat "$d/out")" "$(cat "$d/err")"; fi

# Unmeasured: no resources.tsv beside the stream. The FAIL stays a FAIL; a stream on stdin has none either.
d="$T/at/unmeasured"; mkdir -p "$d"; pkg_events "$QUICK_PKG" 0 11 > "$d/unit.json"
attr_run "$d" "$attr_yml"
if [ "$rc" -eq 1 ] && [ -n "$(first_line "$d/err" "^TIMING FAIL $QUICK_PKG .* · attribution not measured \($d/resources\.tsv absent\)$")" ]; then ok attribution-unmeasured-fail-stays-fail; else bad "attribution-unmeasured-fail-stays-fail: rc=$rc" "$(cat "$d/out")" "$(cat "$d/err")"; fi
pkg_events "$QUICK_PKG" 0 7 > "$d/unit.json"
attr_run "$d" "$attr_yml"
if [ "$rc" -eq 0 ] && [ -n "$(first_line "$d/out" "^TIMING WARN $QUICK_PKG .* · attribution not measured \($d/resources\.tsv absent\)$")" ]; then ok attribution-unmeasured-warn; else bad "attribution-unmeasured-warn: rc=$rc" "$(cat "$d/out")" "$(cat "$d/err")"; fi
rc=0; pkg_events "$QUICK_PKG" 0 11 | run_sut --check --yml "$attr_yml" --suite u --out "$d/stdin.tsv" - >"$d/out" 2>"$d/err" || rc=$?
if [ "$rc" -eq 1 ] && [ -n "$(first_line "$d/err" "^TIMING FAIL $QUICK_PKG .* · attribution not measured \(stream read from stdin\)$")" ]; then ok attribution-stdin-not-measured; else bad "attribution-stdin-not-measured: rc=$rc" "$(cat "$d/out")" "$(cat "$d/err")"; fi
# --resources and --profile name the record when the stream has no directory of its own.
d="$T/at/explicit"; mkdir -p "$d/elsewhere/prof/internal_quick.7"; res_code "$d/elsewhere/res.tsv"
printf '{"package":"internal_quick","helper_of":"","run_delay_s":9,"user_s":3,"sys_s":1}\n' > "$d/elsewhere/prof/internal_quick.7/summary.json"
rc=0; pkg_events "$QUICK_PKG" 0 7 | run_judged --check --yml "$attr_yml" --suite u --out "$d/stdin.tsv" --resources "$d/elsewhere/res.tsv" --profile "$d/elsewhere/prof" - >"$d/out" 2>"$d/err" || rc=$?
if [ "$rc" -eq 0 ] && [ -n "$(first_line "$d/out" "^TIMING WARN $QUICK_PKG .* · attribution CONTENTION \(.* · run-delay 2\.25 \(≥ [0-9.]+\)\)$")" ]; then ok attribution-explicit-resources-and-profile; else bad "attribution-explicit-resources-and-profile: rc=$rc" "$(cat "$d/out")" "$(cat "$d/err")"; fi

# CORRECTED: a load-explained overage carries the attribution too (unit.load: 67% other load -> 15 s limit).
d="$T/at/corrected"; mkdir -p "$d"; pkg_events "$QUICK_PKG" 0 7 > "$d/unit.json"; res_contention "$d/resources.tsv"
printf 'epoch_s\tvm_busy_s\town_s\tcpus\n1704067199\t0\t0\t2\n1704067208\t12\t0\t2\n' > "$d/unit.load"
attr_run "$d" "$attr_yml"
if [ "$rc" -eq 0 ] && [ -n "$(first_line "$d/out" "^TIMING CORRECTED $QUICK_PKG .* other-load=67% limit=15\.000s$CONTENTION_TAIL$")" ]; then ok attribution-corrected; else bad "attribution-corrected: rc=$rc" "$(cat "$d/out")" "$(cat "$d/err")"; fi

# SUITE: quick (:00-:03) is within budget, slow (:04-:12) is over, the suite wall (12 s) is over its 7.5 s. The
# resources quiet down at :06 for the cgroup but not the VM, so the SUITE verdict reads the whole stream's window
# (-25.0 %) and slow's own line reads only its own (-37.5 %).
suite_yml="$T/attr-suite.yml"
cat > "$suite_yml" <<YML
tolerance: 1.25
fail_factor: 2
suites:
  u:
    wall_s: 6
    packages:
      $QUICK_PKG: 100
      $SLOW_PKG: 4
YML
d="$T/at/suite"; mkdir -p "$d"; { pkg_events "$QUICK_PKG" 0 3; pkg_events "$SLOW_PKG" 4 12; } > "$d/unit.json"; res_rows "$d/resources.tsv" 1704067206 3 3 3 1
attr_run "$d" "$suite_yml"
if [ "$rc" -eq 0 ] && [ -n "$(first_line "$d/out" '^TIMING WARN SUITE\(u\) .* · attribution CONTENTION \(tick-deficit -25\.0% ')" ] \
    && [ -n "$(first_line "$d/out" "^TIMING WARN $SLOW_PKG .* · attribution CONTENTION \(tick-deficit -37\.5% ")" ]; then
  ok attribution-suite-whole-stream-window
else bad "attribution-suite-whole-stream-window: rc=$rc" "$(cat "$d/out")" "$(cat "$d/err")"; fi

# Run delay: the package's non-helper summaries are summed (3 + 3 s delayed over 2 + 2 s of cpu = 1.50); a helper
# process, a summary with no run_delay_s and an unreadable summary add nothing. Quiet resources alone read CODE.
d="$T/at/rundelay"; mkdir -p "$d"; pkg_events "$QUICK_PKG" 0 7 > "$d/unit.json"; res_code "$d/resources.tsv"
summary() { mkdir -p "$d/profile/internal_quick.$1"; printf '%s\n' "$2" > "$d/profile/internal_quick.$1/summary.json"; }
summary 101 '{"package":"internal_quick","helper_of":"","event":"exit","run_delay_s":3,"user_s":1.5,"sys_s":0.5}'
summary 102 '{"package":"internal_quick","helper_of":"","event":"exit","run_delay_s":3,"user_s":1,"sys_s":1}'
summary 103 '{"package":"internal_quick","helper_of":"internal_quick.101","event":"helper-exit","run_delay_s":0,"user_s":100,"sys_s":0}'
summary 104 '{"package":"internal_quick","helper_of":"","event":"exit","run_delay_s":null,"run_delay_error":"no schedstat","user_s":50,"sys_s":0}'
summary 105 'not json'
mkdir -p "$d/profile/internal_quick_other.106" && printf '%s\n' '{"package":"internal_quick_other","helper_of":"","run_delay_s":900,"user_s":1,"sys_s":0}' > "$d/profile/internal_quick_other.106/summary.json"
attr_run "$d" "$attr_yml"
if [ "$rc" -eq 0 ] && [ -n "$(first_line "$d/out" "^TIMING WARN $QUICK_PKG .* · attribution CONTENTION \(tick-deficit \+0\.0% .* · run-delay 1\.50 \(≥ [0-9.]+\)\)$")" ] \
    && grep -q "test-timing: profile summary $d/profile/internal_quick.105/summary.json unreadable" "$d/err"; then
  ok attribution-run-delay-summed
else bad "attribution-run-delay-summed: rc=$rc" "$(cat "$d/out")" "$(cat "$d/err")"; fi

# A judge that fails (here: a resources.tsv with no header, which the judge refuses with exit 2) leaves every
# over-limit line `not measured` with the judge's own first stderr line, names the judge once, and is never CODE.
d="$T/at/judge-broken"; mkdir -p "$d"; pkg_events "$QUICK_PKG" 0 11 > "$d/unit.json"; printf 'not a header\n' > "$d/resources.tsv"
attr_run "$d" "$attr_yml"
if [ "$rc" -eq 1 ] && [ -n "$(first_line "$d/err" "^TIMING FAIL $QUICK_PKG .* · attribution not measured \(judge failed: test-contention: .*resources\.tsv.*\)$")" ] \
    && [ "$(grep -c '^test-timing: contention judge .*test-contention\.sh failed (exit 2): test-contention: ' "$d/err")" -eq 1 ] \
    && ! grep -q 'attribution CODE\|attribution CONTENTION' "$d/out" "$d/err"; then
  ok attribution-judge-broken
else bad "attribution-judge-broken: rc=$rc" "$(cat "$d/out")" "$(cat "$d/err")"; fi

# Within budget nothing is asked: the same broken resources.tsv says nothing and the suite line is unchanged.
d="$T/at/within"; mkdir -p "$d"; pkg_events "$QUICK_PKG" 0 3 > "$d/unit.json"; printf 'not a header\n' > "$d/resources.tsv"
attr_run "$d" "$attr_yml"
if [ "$rc" -eq 0 ] && [ ! -s "$d/err" ] && grep -qx 'TIMING: SUITE(u) within budget (3.000s <= 100.000s x1.25)' "$d/out" && ! grep -q 'attribution' "$d/out"; then ok attribution-within-budget-unasked; else bad "attribution-within-budget-unasked: rc=$rc" "$(cat "$d/out")" "$(cat "$d/err")"; fi

# e2e judged: e2e.load beside e2e.json corrects the e2e limits as unit.load does the unit's.
e2e_yml="$T/attr-e2e.yml"
cat > "$e2e_yml" <<YML
tolerance: 1.25
fail_factor: 2
suites:
  e2e:
    wall_s: 100
    packages:
      $E2E_PKG: 4
YML
d="$T/at/e2e"; mkdir -p "$d"; pkg_events "$E2E_PKG" 0 7 > "$d/e2e.json"; res_contention "$d/resources.tsv"
printf 'epoch_s\tvm_busy_s\town_s\tcpus\n1704067199\t0\t0\t2\n1704067208\t12\t0\t2\n' > "$d/e2e.load"
rc=0; run_judged --check --yml "$e2e_yml" --suite e2e --out "$d/e2e.tsv" "$d/e2e.json" >"$d/out" 2>"$d/err" || rc=$?
if [ "$rc" -eq 0 ] && [ -n "$(first_line "$d/out" "^TIMING CORRECTED $E2E_PKG .* other-load=67% limit=15\.000s$CONTENTION_TAIL$")" ] && ! grep -q 'load record' "$d/out" "$d/err"; then ok e2e-load-record-judged; else bad "e2e-load-record-judged: rc=$rc" "$(cat "$d/out")" "$(cat "$d/err")"; fi

shtest_end
