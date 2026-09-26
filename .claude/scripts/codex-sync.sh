#!/usr/bin/env bash
set -euo pipefail

# Mirror auto-compile — the deterministic "always compile after framework edits".
# Compiles BOTH runtime mirrors from the same Claude sources:
#   Codex — `pfm codex build` (single writer) + `pfm codex check`
#   OpenCode — `pfm opencode build` + `check`
#   mark — PostToolUse(Edit|Write): when the edited file is a Claude source either
#          mirror compiles from (.claude/**, any CLAUDE.md, $HOME/.claude/commands/**),
#          drop the repo-scoped dirty flag /tmp/<project>/guard/codex_dirty
#          (<project> = the repo directory's basename, leading dot stripped).
#   sync — Stop: if the flag is present, run both mirrors' build+check; success
#          clears the flag silently. Failure blocks the stop ONCE: exit 0 with
#          JSON {decision: block, reason} so the reason reaches the model, which
#          repairs the mirror in the same turn; when the model stops again with
#          stop_hook_active set, the hook lets the turn end with a user-visible
#          systemMessage warning and leaves the flag set so the next turn checks
#          again. Never exit 1 (a red stop-hook error the user sees and the model
#          never does) and never an unguarded exit 2 (a blocking loop took whole
#          sessions down on a stale $HOME link). The message names WHICH of the
#          four stages failed and carries only that stage's output: build failure
#          and check failure are different defects with different repairs. When
#          this script is itself broken, the stage name is what says so.
# Coverage (declared): sees Edit/Write TOOL calls only. A Bash-driven write (sed,
# redirect) to a Claude source does NOT set the flag — `pfm codex check` and
# `pfm opencode check` in the pfm `structure` audit scope remain the backstop
# for that shape.
#
# `pfm codex build` and `pfm opencode build` are the SINGLE writers of their
# mirrors. Cross-runtime markers are not shared: each writer only reclaims files
# carrying its own (or its declared predecessors') marker.

MODE="${1:-mark}"
INPUT=$(cat 2>/dev/null || true)

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
    command -v pfm >/dev/null 2>&1 || exit 0
    PROJECT="$(basename "$REPO_ROOT")"; PROJECT="${PROJECT#.}"
    mkdir -p "/tmp/$PROJECT/guard"
    touch "/tmp/$PROJECT/guard/codex_dirty"
    ;;
  sync)
    REPO_ROOT="${CLAUDE_PROJECT_DIR:-}"
    [[ -n "$REPO_ROOT" ]] || REPO_ROOT=$(git rev-parse --show-superproject-working-tree 2>/dev/null)
    [[ -n "$REPO_ROOT" ]] || REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
    PROJECT="$(basename "$REPO_ROOT")"; PROJECT="${PROJECT#.}"
    FLAG="/tmp/$PROJECT/guard/codex_dirty"
    [[ -f "$FLAG" ]] || exit 0
    # The fence's templates lane builds this dev binary into the shared timing
    # scratch for Linux; the host takes it only when it actually runs here.
    PFM_BIN="/tmp/$PROJECT/timing/pfm-dev-bin"
    { [[ -x "$PFM_BIN" ]] && "$PFM_BIN" --version >/dev/null 2>&1; } || PFM_BIN=$(command -v pfm 2>/dev/null || true)
    if [[ -z "$PFM_BIN" || ! -x "$PFM_BIN" ]]; then
      jq -n --arg m 'codex-sync WARNING: compiler unavailable — mirrors were not checked; dirty flag retained' '{systemMessage: $m}'
      exit 0
    fi
    OUT=$("$PFM_BIN" codex build "$REPO_ROOT" 2>&1) && CODEX_BUILD=0 || CODEX_BUILD=$?
    CHK=$("$PFM_BIN" codex check "$REPO_ROOT" 2>&1) && CODEX_CHECK=0 || CODEX_CHECK=$?
    OGEN=$("$PFM_BIN" opencode build "$REPO_ROOT" 2>&1) && OC_BUILD=0 || OC_BUILD=$?
    OCHK=$("$PFM_BIN" opencode check "$REPO_ROOT" 2>&1) && OC_CHECK=0 || OC_CHECK=$?
    if (( CODEX_BUILD == 0 && CODEX_CHECK == 0 && OC_BUILD == 0 && OC_CHECK == 0 )); then
      rm -f "$FLAG"
      exit 0
    fi
    # Name the stage that actually failed, and carry only that stage's output.
    # A build failure and a check failure demand different repairs: reporting
    # "failed to compile" when the writer printed PASS and only the verifier
    # objected sends the reader to fix something that is not broken.
    FAILED=""
    (( CODEX_BUILD != 0 )) && FAILED="${FAILED:+$FAILED, }codex build"
    (( CODEX_CHECK != 0 )) && FAILED="${FAILED:+$FAILED, }codex check"
    (( OC_BUILD != 0 )) && FAILED="${FAILED:+$FAILED, }opencode build"
    (( OC_CHECK != 0 )) && FAILED="${FAILED:+$FAILED, }opencode check"
    DETAIL=""
    (( CODEX_BUILD != 0 )) && DETAIL+="codex build:"$'\n'"${OUT:-}"$'\n'
    (( CODEX_CHECK != 0 )) && DETAIL+="codex check:"$'\n'"${CHK:-}"$'\n'
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
