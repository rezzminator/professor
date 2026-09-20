#!/usr/bin/env bash
set -euo pipefail

# Codex-mirror auto-compile — the deterministic "always compile after framework edits".
#   mark — PostToolUse(Edit|Write): when the edited file is a Claude source the mirror
#          compiles from (.claude/**, any CLAUDE.md, $HOME/.claude/commands/**), drop
#          the repo-scoped dirty flag tmp/professor_codex_dirty.
#   sync — Stop: if the flag is present, run both Codex and OpenCode build+check
#          pairs; success clears the flag silently. Failure blocks the stop ONCE:
#          exit 0 with JSON {decision: block, reason} so the reason reaches the
#          model, which repairs the mirror in the same turn; when the model stops
#          again with stop_hook_active set, the hook lets the turn end with a
#          user-visible systemMessage warning and leaves the flag set so the next
#          turn checks again. Never exit 1 (a red stop-hook error the user sees
#          and the model never does) and never an unguarded exit 2 (a blocking
#          loop with no way out).
# Coverage (declared): sees Edit/Write TOOL calls only. A Bash-driven write (sed,
# redirect) to a Claude source does NOT set the flag — `pfm codex check` in the
# pfm `structure` audit scope remains the backstop for that shape.
#
# `pfm codex build` and `pfm opencode build` are the SINGLE writers of their
# mirrors. The legacy JavaScript compiler is retired; no second writer may stamp
# a competing marker into the same OpenCode outputs.

MODE="${1:-mark}"
INPUT=$(cat 2>/dev/null || true)
PFM_BIN=$(command -v pfm 2>/dev/null || echo "$HOME/.local/bin/pfm")

case "$MODE" in
  mark)
    FILE_PATH=$(printf '%s' "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null || true)
    [[ -z "$FILE_PATH" ]] && exit 0
    case "$FILE_PATH" in
      "$HOME"/.claude/commands/*) ;;
      */.claude/*|*/CLAUDE.md|*/.mcp.json) ;;
      *) exit 0 ;;
    esac
    # The mirror is compiled from the harness PROJECT root (its .claude/codex-build.json
    # governs), never from a nested git root: a CLAUDE.md inside a submodule resolves
    # to the submodule's toplevel, which has no CLAUDE.md and cannot build.
    REPO_ROOT="${CLAUDE_PROJECT_DIR:-}"
    [[ -n "$REPO_ROOT" ]] || REPO_ROOT=$(git -C "$(dirname "$FILE_PATH")" rev-parse --show-superproject-working-tree 2>/dev/null)
    [[ -n "$REPO_ROOT" ]] || REPO_ROOT=$(git -C "$(dirname "$FILE_PATH")" rev-parse --show-toplevel 2>/dev/null) \
      || REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
    [[ -x "$PFM_BIN" ]] || exit 0
    mkdir -p "$REPO_ROOT/tmp"
    touch "$REPO_ROOT/tmp/professor_codex_dirty"
    ;;
  sync)
    REPO_ROOT="${CLAUDE_PROJECT_DIR:-}"
    [[ -n "$REPO_ROOT" ]] || REPO_ROOT=$(git rev-parse --show-superproject-working-tree 2>/dev/null)
    [[ -n "$REPO_ROOT" ]] || REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
    FLAG="$REPO_ROOT/tmp/professor_codex_dirty"
    [[ -f "$FLAG" ]] || exit 0
    if [[ ! -x "$PFM_BIN" ]]; then
      jq -n --arg m 'codex-sync WARNING: compiler unavailable — mirrors were not checked; dirty flag retained' '{systemMessage: $m}'
      exit 0
    fi
    OUT=$("$PFM_BIN" codex build "$REPO_ROOT" 2>&1) && BUILD=0 || BUILD=$?
    CHK=$("$PFM_BIN" codex check "$REPO_ROOT" 2>&1) && CHECK=0 || CHECK=$?
    OGEN=$("$PFM_BIN" opencode build "$REPO_ROOT" 2>&1) && OC_BUILD=0 || OC_BUILD=$?
    OCHK=$("$PFM_BIN" opencode check "$REPO_ROOT" 2>&1) && OC_CHECK=0 || OC_CHECK=$?
    if (( BUILD == 0 && CHECK == 0 && OC_BUILD == 0 && OC_CHECK == 0 )); then
      rm -f "$FLAG"
      exit 0
    fi
    # Name the stage that actually failed, and carry only that stage's output.
    # A build failure and a check failure demand different repairs: reporting
    # "failed to compile" when the writer printed PASS and only the verifier
    # objected sends the reader to fix something that is not broken.
    FAILED=""
    (( BUILD != 0 )) && FAILED="${FAILED:+$FAILED, }codex build"
    (( CHECK != 0 )) && FAILED="${FAILED:+$FAILED, }codex check"
    (( OC_BUILD != 0 )) && FAILED="${FAILED:+$FAILED, }opencode build"
    (( OC_CHECK != 0 )) && FAILED="${FAILED:+$FAILED, }opencode check"
    DETAIL=""
    (( BUILD != 0 )) && DETAIL+="codex build:"$'\n'"${OUT:-}"$'\n'
    (( CHECK != 0 )) && DETAIL+="codex check:"$'\n'"${CHK:-}"$'\n'
    (( OC_BUILD != 0 )) && DETAIL+="opencode build:"$'\n'"${OGEN:-}"$'\n'
    (( OC_CHECK != 0 )) && DETAIL+="opencode check:"$'\n'"${OCHK:-}"$'\n'
    DETAIL=${DETAIL:0:4000}
    STOP_ACTIVE=$(printf '%s' "$INPUT" | jq -r '.stop_hook_active // false' 2>/dev/null || echo false)
    if [[ "$STOP_ACTIVE" == "true" ]]; then
      # The model already had its repair turn for this stop: let the turn end,
      # warn the user, keep the flag so the next turn checks again.
      jq -n --arg m "codex-sync WARNING: $FAILED still failing after the repair turn — the mirror is stale; the flag stays set and the check repeats next turn." '{systemMessage: $m}'
      exit 0
    fi
    jq -n --arg r "codex-sync: $FAILED failed after this turn's framework edits — the mirror is stale. Repair it now, then end the turn: a dangling ~/.claude link → pfm install --yes (prunes orphaned global-command links; a stale ~/.claude/agents link is removed by hand); a stale or orphan mirror file → pfm codex build . and pfm opencode build .; then pfm codex check . and pfm opencode check . must both pass. Output of the failing stage(s):
$DETAIL" '{decision: "block", reason: $r}'
    exit 0
    ;;
esac
exit 0
