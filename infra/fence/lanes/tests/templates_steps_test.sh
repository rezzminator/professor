#!/usr/bin/env bash
set -euo pipefail

# Exercise both registration paths, not a second expected step catalogue.
REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../../.." && pwd -P)"
CATALOGUE=${1:-"$REPO_ROOT/infra/fence/checks.sh"}
[[ -f "$CATALOGUE" ]] || { echo "ERROR templates steps: catalogue missing: $CATALOGUE" >&2; exit 2; }
ROOT=$(mktemp -d "${TMPDIR:-/tmp}/templates-steps.XXXXXX")
trap 'rm -rf "$ROOT"' EXIT
TMP_BASE="$ROOT"
source "$CATALOGUE"

# The caller-owned CI policy suite must be reached through the shared Python
# fixture step, not merely exist on disk or run in only one scheduler.
fixture_commands="$( (
  run() { shift 2; printf '%s\n' "$*"; }
  head_() { :; }
  checks_templates_refresh_scope
) )"
grep -Fxq "python3 $REPO_ROOT/scripts/test-verify-leak-policy.py" <<< "$fixture_commands" || {
  echo 'FAIL templates steps: verify leak policy suite is not invoked by the shared Python fixture step' >&2; exit 1;
}

# Keep the real coordinators and mirror group; replace only leaf checks.
while read -r _ _ name; do
  case "$name" in
    checks_templates|checks_templates_steps|checks_templates_step_serial|checks_templates_mirrors) ;;
    checks_templates_*) eval "$name() { printf '%s\\n' '$name' \"\$*\" | paste -sd ' ' -; }" ;;
  esac
done < <(declare -F)

checks_templates > "$ROOT/serial"
# These have equivalent gate registrations, inspected separately below.
for name in check_map_serial lanes_serial demo_serial; do
  [[ "$(grep -c "^checks_templates_$name $" "$ROOT/serial" || true)" == 1 ]] || {
    echo "FAIL templates steps: serial $name must run once" >&2; exit 1;
  }
done
sed -i '/^checks_templates_\(check_map_serial\|lanes_serial\|demo_serial\) /d' "$ROOT/serial"

(
  phase=0
  need_tool() { :; }
  timing_run_dir() { printf '%s\n' "$ROOT"; }
  timing_run_note() { :; }
  steps_reset() { :; }
  steps_barrier() { phase=$((phase + 1)); }
  fail_step() { echo "ERROR templates steps: $*" >&2; exit 2; }
  steps_add() {
    printf '%s\t%s\t%s\n' "$1" "$phase" normal >> "$ROOT/registrations"
    shift
    case "$1" in checks_templates_*) "$@" ;; esac
  }
  steps_add_heavy() {
    printf '%s\t%s\t%s\n' "$1" "$phase" heavy >> "$ROOT/registrations"
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
# Keep the gate placement contract as well as the shared check membership.
awk -F '\t' '
  $1 == "templates.check-map" { map++; if ($2 != 1 || $3 != "heavy") bad=1; next }
  $1 ~ /^templates\.(lanes|demo)\./ { if ($2 != 0 || $3 != "normal") bad=1; next }
  { if ($2 != 1 || $3 != "normal") bad=1 }
  END { exit (bad || map != 1) }
' "$ROOT/registrations" || { echo 'FAIL templates steps: gate phase or heavy check-map placement changed' >&2; exit 1; }
for family in lanes demo; do
  expected=0
  fixtures="$REPO_ROOT/infra/fence/lanes/tests"
  [[ "$family" != demo ]] || fixtures="$REPO_ROOT/infra/demo/tests"
  for f in "$fixtures"/*_test.sh; do
    [[ -f "$f" ]] || { echo "ERROR templates steps: no $family fixtures" >&2; exit 2; }
    expected=$((expected + 1))
  done
  actual=$(grep -c "^templates\.$family\." "$ROOT/registrations" || true)
  [[ "$actual" == "$expected" ]] || { echo "FAIL templates steps: $family fixtures $actual registered, $expected expected" >&2; exit 1; }
done
if [[ -n "$(cut -f1 "$ROOT/registrations" | sort | uniq -d)" ]]; then
  echo 'FAIL templates steps: duplicate gate registration' >&2; exit 1
fi
printf 'PASS templates steps: serial/gate parity (%s checks), fixture phases and heavy check-map hold\n' "$(wc -l < "$ROOT/serial.sorted" | tr -d ' ')"
