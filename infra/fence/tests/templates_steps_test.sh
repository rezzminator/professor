#!/usr/bin/env bash
set -euo pipefail

# Exercise both registration paths, not a second expected step catalogue.
REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
CATALOGUE=${1:-"$REPO_ROOT/infra/fence/checks.sh"}
[[ -f "$CATALOGUE" ]] || { echo "ERROR templates steps: catalogue missing: $CATALOGUE" >&2; exit 2; }
ROOT=$(mktemp -d "${TMPDIR:-/tmp}/templates-steps.XXXXXX")
trap 'rm -rf "$ROOT"' EXIT
TMP_BASE="$ROOT"
# The catalogue under test is the whole one: a CI slice's filter (ci.yml) must not reach it.
unset PFM_GATE_ONLY PFM_GATE_SKIP
source "$CATALOGUE"

plugin_log_commands="$( (
  run() { shift 2; printf '%s\n' "$*"; }
  checks_templates_plugin_logs
) )"
grep -Fxq "bash $REPO_ROOT/scripts/test-plugin-logs.sh" <<< "$plugin_log_commands" ||
  { echo "FAIL templates steps: plugin logs step does not run its self-test" >&2; exit 1; }
grep -Fxq "bash $REPO_ROOT/scripts/test-platform.sh" <<< "$plugin_log_commands" ||
  { echo "FAIL templates steps: plugin logs step does not run the platform door self-test" >&2; exit 1; }
grep -Fxq "bash $REPO_ROOT/scripts/test-install.sh" <<< "$plugin_log_commands" ||
  { echo "FAIL templates steps: plugin logs step does not run the release installer self-test" >&2; exit 1; }

# The plugin lab's step lints its shell, then runs every self-test it ships.
plugin_lab_commands="$( (
  run() { shift 2; printf '%s\n' "$*"; }
  need_tool() { :; }
  checks_templates_plugin_lab
) )" || { echo 'FAIL templates steps: plugin-lab step did not run' >&2; exit 1; }
grep -Fq "shellcheck -x" <<< "$plugin_lab_commands" &&
  grep -Fq "$REPO_ROOT/infra/plugin-lab" <<< "$plugin_lab_commands" &&
  grep -Fq '_test.sh' <<< "$plugin_lab_commands" || {
  echo 'FAIL templates steps: plugin-lab shellcheck and self-tests are not invoked' >&2; exit 1;
}

# Keep the real coordinators and mirror group; replace only leaf checks.
while read -r _ _ name; do
  case "$name" in
    checks_templates|checks_templates_steps|checks_templates_step_serial|checks_templates_mirrors) ;;
    checks_templates_*) eval "$name() { printf '%s\\n' '$name' \"\$*\" | paste -sd ' ' -; }" ;;
  esac
done < <(declare -F)

checks_templates > "$ROOT/serial"
# The fence suites have equivalent gate registrations, inspected separately below.
[[ "$(grep -c "^checks_templates_fence_serial $" "$ROOT/serial" || true)" == 1 ]] || {
  echo "FAIL templates steps: serial fence_serial must run once" >&2; exit 1;
}
sed -i '/^checks_templates_fence_serial /d' "$ROOT/serial"

(
  need_tool() { :; }
  timing_run_dir() { printf '%s\n' "$ROOT"; }
  timing_run_note() { :; }
  steps_reset() { :; }
  fail_step() { echo "ERROR templates steps: $*" >&2; exit 2; }
  steps_add() {
    printf '%s\t%s\n' "$1" normal >> "$ROOT/registrations"
    shift
    case "$1" in checks_templates_*) "$@" ;; esac
  }
  steps_add_heavy() {
    printf '%s\t%s\n' "$1" heavy >> "$ROOT/registrations"
  }
  # Stop at the registration boundary: never run suites, builds or samplers.
  gate_derive_bound() { exit 0; }
  unset PFM_GATE_FIXTURES
  gate_run templates
) > "$ROOT/gate"

sort "$ROOT/serial" > "$ROOT/serial.sorted"
sort "$ROOT/gate" > "$ROOT/gate.sorted"
if ! diff -u "$ROOT/serial.sorted" "$ROOT/gate.sorted"; then
  echo 'FAIL templates steps: serial and gate template checks differ' >&2
  exit 1
fi
[[ -s "$ROOT/serial.sorted" ]] || { echo 'FAIL templates steps: no template check was registered' >&2; exit 1; }
# Keep the gate placement contract as well as the shared check membership: no template step takes a heavy slot.
awk -F '\t' '$2 != "normal" { bad=1 } END { exit bad }' "$ROOT/registrations" ||
  { echo 'FAIL templates steps: a template step is registered heavy' >&2; exit 1; }
expected=0
for f in "$REPO_ROOT"/infra/fence/tests/*_test.sh; do
  [[ -f "$f" ]] || { echo "ERROR templates steps: no fence fixtures" >&2; exit 2; }
  expected=$((expected + 1))
done
actual=$(grep -c "^templates\.fence\." "$ROOT/registrations" || true)
[[ "$actual" == "$expected" ]] || { echo "FAIL templates steps: fence fixtures $actual registered, $expected expected" >&2; exit 1; }
if [[ -n "$(cut -f1 "$ROOT/registrations" | sort | uniq -d)" ]]; then
  echo 'FAIL templates steps: duplicate gate registration' >&2; exit 1
fi
printf 'PASS templates steps: serial/gate parity (%s checks) and placement hold\n' "$(wc -l < "$ROOT/serial.sorted" | tr -d ' ')"
