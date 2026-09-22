#!/usr/bin/env bash
set -euo pipefail

# Failure-biased filter for test-runner output — keeps failures, errors, tracebacks,
# the summary block, and coverage totals; drops passing noise. TWO entry modes:
#
#   PIPE:  log=$(mktemp); <test cmd> > "$log" 2>&1; rc=$?; filter-test-output.sh -p < "$log"
#     Reads RAW test output on stdin, prints the filtered subset to stdout. Run a suite
#     in this shape so the runner's own rc is captured before filtering.
#
#   HOOK (default, no args):  wired in settings.json PostToolUse(Bash). Reads the hook JSON envelope on stdin and
#     returns updatedToolOutput — fires for the main loop AND for sub-agents. It acts
#     ONLY when the Bash command itself invokes a test runner (a `grep pytest` or a
#     `pip install pytest` is left alone), and it only ever REMOVES lines: when nothing
#     is recognized or nothing would be dropped it stays silent and the raw output
#     stands untouched — the hook never adds text to the model's context.

# Shared filter: raw test output on stdin -> failure-biased subset on stdout.
# Prints nothing when no failure/summary line is recognized.
_filter() {
  local out
  out=$(cat)
  [[ -z "$out" ]] && return 0
  printf '%s\n' "$out" \
    | grep -iE 'fail|error|exception|assert|✗|FAILED|ERROR|Traceback|warning|coverage|TOTAL|All files|passed|failed|[0-9]+ (passed|failed|error)' \
    | tail -200 || true
}

# Is this Bash command an actual test-runner invocation? Split into simple commands
# (`;`, `&&`, `||`, `|`, subshell parens), strip leading VAR=val assignments and the
# wrappers (timeout/time/env/nice/sudo), then require the runner as the command word:
# a bare/path runner, `python -m pytest`, `uv run pytest`, `{pm} [run] test|integration|e2e`,
# `npx|pnpm [exec] vitest|jest|mocha|cucumber|playwright test`, a toolchain's own test verb
# (`go|cargo|dotnet|swift|mix|deno|bun test`, `cargo nextest`), `rspec` / `bundle exec rspec`,
# `phpunit`, `ctest`, `mvn|gradle … test`, and `make test|check`. A runner missing here is
# never filtered — its raw output stands; add its pattern to this regex.
_is_test_cmd() {
  printf '%s\n' "$1" \
    | sed -E 's/(&&|\|\||\||;|\(|\))/\n/g' \
    | sed -E 's/^[[:space:]]+//; s/^([A-Za-z_][A-Za-z0-9_]*=[^[:space:]]*[[:space:]]+)*//; s/^((timeout|time|env|nice|sudo)([[:space:]]+-[^[:space:]]+)*([[:space:]]+[0-9]+[smhd]?)?[[:space:]]+)*//' \
    | grep -qE '^([^[:space:]]*/)?(pytest|vitest|jest|mocha|cucumber(-js)?)([[:space:]]|$)|^([^[:space:]]*/)?python[0-9.]*[[:space:]]+-m[[:space:]]+pytest([[:space:]]|$)|^uv[[:space:]]+run[[:space:]]+(-[^[:space:]]+[[:space:]]+([^-][^[:space:]]*[[:space:]]+)?)*pytest([[:space:]]|$)|^(pnpm|npm|yarn|bun)[[:space:]]+(run[[:space:]]+)?(test|integration|e2e)([:[:space:]]|$)|^(npx|pnpm|bunx)[[:space:]]+(exec[[:space:]]+)?(playwright[[:space:]]+test|vitest|jest|mocha|cucumber(-js)?)([[:space:]]|$)|^([^[:space:]]*/)?playwright[[:space:]]+test([[:space:]]|$)|^([^[:space:]]*/)?(go|cargo|dotnet|swift|mix|deno|bun)[[:space:]]+test([[:space:]]|$)|^cargo[[:space:]]+nextest[[:space:]]|^(bundle[[:space:]]+exec[[:space:]]+)?([^[:space:]]*/)?(rspec|phpunit|ctest)([[:space:]]|$)|^([^[:space:]]*/)?(mvnw?|gradlew?)([[:space:]]+[^[:space:]]+)*[[:space:]]+test([[:space:]]|$)|^make[[:space:]]+(test|check)([[:space:]]|$)'
}

# --- PIPE mode ---
# Exit status is DEFENSE-IN-DEPTH, never the run verdict — the runner's own rc,
# captured per the project's testing manual § Run commands, is the verdict. 3 = no pass/fail summary
# recognized (runner crashed/killed), 1 = a failure indicator recognized, 0 = green
# summary seen. A wrapper keying on this exit can no longer read a failing or
# absent run as success.
if [[ "${1:-}" == "-p" || "${1:-}" == "--pipe" ]]; then
  raw=$(cat)
  out=$(printf '%s\n' "$raw" | _filter)
  if [[ -z "$out" ]]; then
    # A green run ALWAYS emits an "N passed" summary the keep-grep catches, so an empty
    # result means the runner crashed, was killed (timeout/OOM), or printed an
    # unrecognized format — surface the raw tail so a real failure is never reported green.
    printf '%s\n' "(no pass/fail summary recognized — runner may have crashed or been killed; raw tail follows)"
    printf '%s\n' "$raw" | tail -40
    exit 3
  fi
  printf '%s\n' "$out"
  if printf '%s' "$out" | grep -qiE '(^|[^0-9])[1-9][0-9]* (failed|failures?)|FAILED|Traceback|✗'; then
    exit 1
  fi
  exit 0
fi

# --- HOOK mode ---
INPUT=$(cat)

TOOL=$(echo "$INPUT" | jq -r '.tool_name // empty')
[[ "$TOOL" == "Bash" ]] || exit 0

CMD=$(echo "$INPUT" | jq -r '.tool_input.command // empty')
_is_test_cmd "$CMD" || exit 0

# Bash's output is an object ({stdout, stderr, interrupted, ...}); a bare string is
# rejected by the harness ("does not match Bash's output shape") and silently ignored.
# The field name varies by version (tool_output vs tool_response).
OUTPUT=$(echo "$INPUT" | jq -r '
  (.tool_output // .tool_response // empty) as $o
  | if ($o | type) == "object"
    then ([$o.stdout, $o.stderr] | map(select(. != null and . != "")) | join("\n"))
    else ($o // "") end
')
[[ -z "$OUTPUT" ]] && exit 0

FILTERED=$(printf '%s\n' "$OUTPUT" | _filter)
# Nothing recognized, or nothing to drop: stay silent — the raw output stands.
[[ -z "$FILTERED" || "$FILTERED" == "$OUTPUT" ]] && exit 0

echo "$INPUT" | jq -c --arg out "$FILTERED" '
  (.tool_output // .tool_response) as $o
  | (if ($o | type) == "object" then $o else {} end) + {stdout: $out, stderr: ""}
  | {hookSpecificOutput: {hookEventName: "PostToolUse", updatedToolOutput: .}}
'
