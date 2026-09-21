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
for tool in bash jq python3 awk sed sort date mktemp cat grep wc mv rm mkdir cut tr head tail xargs comm dirname basename; do
  real="$(command -v "$tool" 2>/dev/null)" || continue
  ln -sf "$real" "$dockerfail/$tool"
done
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

shtest_end
