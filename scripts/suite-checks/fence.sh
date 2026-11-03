#!/usr/bin/env bash
# Sourced by the fence catalogue; failures reach its normal run/fail_step doors.

checks_gate_pfm_binary() { # run directory, caller's fallback binary name
  CHECKS_SUITE_PFM="$1/bin/pfm"
  if [[ ! -x "$CHECKS_SUITE_PFM" ]]; then
    CHECKS_SUITE_PFM="$1/$2-pfm"
    if ! go -C pfm build -o "$CHECKS_SUITE_PFM" ./cmd/pfm; then
      fail_step "templates: $2 pfm build FAILED"
      return 1
    fi
  fi
}

checks_gate_suite() { # run directory
  local run_dir="$1"
  run "templates: operation census matches dispatch" -- bash -c 'cd "$1/pfm" && go run ./scripts/helpops -check' _ "$REPO_ROOT"
  run "templates: journey check self-tests" -- bash "$REPO_ROOT/scripts/suite-checks.sh" --self-test --output "$run_dir/suite-checks"
  checks_gate_pfm_binary "$run_dir" suite-checks || return 1
  run "templates: journey suite checks" -- bash "$REPO_ROOT/scripts/suite-checks.sh" --pfm "$CHECKS_SUITE_PFM" --runs "$run_dir/suite-checks"
}

checks_templates_suite() {
  local run_dir="$TMP_BASE/suite-checks"
  mkdir -p "$run_dir" || { fail_step "templates: suite-checks run directory unreadable"; return 1; }
  checks_gate_suite "$run_dir"
}
