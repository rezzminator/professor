#!/usr/bin/env bash
set -euo pipefail

# Mirror auto-compile — the deterministic "always compile after framework edits".
# Compiles BOTH runtime mirrors from the same Claude sources:
#   Codex — `pfm codex build` (single writer) + `pfm codex check`
#   OpenCode — `pfm opencode build` + `check`
#   mark — PostToolUse(Edit|Write): when the edited file is a Claude source either
#          mirror compiles from (.claude/**, any CLAUDE.md, $HOME/.claude/commands/**),
#          drop the repo-scoped dirty flag tmp/professor_codex_dirty.
#   sync — Stop: if the flag is present, run both mirrors' build+check; success
#          clears the flag silently, failure WARNS (exit 1, reason on stderr —
#          shown to the user, the turn ends) and leaves the flag set so the
#          next turn retries: a broken mirror is visible every turn until fixed,
#          never silently shipped, and never a wall the chat cannot end a turn
#          past (a blocking exit 2 took whole sessions down on a stale $HOME
#          link). Respects stop_hook_active. The warning names WHICH of the four
#          stages failed and prints only that stage's output: build failure and
#          check failure are different defects with different repairs, and one
#          message covering both reports a compile broken while the writer says
#          PASS. When this script is itself broken, the stage name is what says
#          so — a bare "a mirror failed" is indistinguishable from any of them.
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
    REPO_ROOT=$(git -C "$(dirname "$FILE_PATH")" rev-parse --show-toplevel 2>/dev/null) \
      || REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
    command -v pfm >/dev/null 2>&1 || exit 0
    mkdir -p "$REPO_ROOT/tmp"
    touch "$REPO_ROOT/tmp/professor_codex_dirty"
    ;;
  sync)
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
    FLAG="$REPO_ROOT/tmp/professor_codex_dirty"
    [[ -f "$FLAG" ]] || exit 0
    PFM_BIN="$REPO_ROOT/tmp/timing/pfm-dev-bin"
    [[ -x "$PFM_BIN" ]] || PFM_BIN=$(command -v pfm 2>/dev/null || true)
    if [[ -z "$PFM_BIN" || ! -x "$PFM_BIN" ]]; then
      printf 'codex-sync: compiler unavailable — mirrors were not checked; dirty flag retained\n' >&2
      exit 1
    fi
    OUT=$("$PFM_BIN" codex build "$REPO_ROOT" 2>&1) && CODEX_BUILD=0 || CODEX_BUILD=$?
    CHK=$("$PFM_BIN" codex check "$REPO_ROOT" 2>&1) && CODEX_CHECK=0 || CODEX_CHECK=$?
    OGEN=$("$PFM_BIN" opencode build "$REPO_ROOT" 2>&1) && OC_BUILD=0 || OC_BUILD=$?
    OCHK=$("$PFM_BIN" opencode check "$REPO_ROOT" 2>&1) && OC_CHECK=0 || OC_CHECK=$?
    if (( CODEX_BUILD == 0 && CODEX_CHECK == 0 && OC_BUILD == 0 && OC_CHECK == 0 )); then
      rm -f "$FLAG"
      exit 0
    fi
    STOP_ACTIVE=$(printf '%s' "$INPUT" | jq -r '.stop_hook_active // false' 2>/dev/null || echo false)
    [[ "$STOP_ACTIVE" == "true" ]] && exit 0
    # Name the stage that actually failed, and print only that stage's output.
    # A build failure and a check failure demand different repairs: reporting
    # "failed to compile" when the writer printed PASS and only the verifier
    # objected sends the reader to fix something that is not broken.
    FAILED=""
    (( CODEX_BUILD != 0 )) && FAILED="${FAILED:+$FAILED, }codex build"
    (( CODEX_CHECK != 0 )) && FAILED="${FAILED:+$FAILED, }codex check"
    (( OC_BUILD != 0 )) && FAILED="${FAILED:+$FAILED, }opencode build"
    (( OC_CHECK != 0 )) && FAILED="${FAILED:+$FAILED, }opencode check"
    printf 'codex-sync WARNING: %s failed after this turn'\''s framework edits — the mirror is stale; fix it next turn (the flag stays set and this warning repeats until it passes).\n' "$FAILED" >&2
    (( CODEX_BUILD != 0 )) && printf 'codex build:\n%s\n' "${OUT:-}" >&2
    (( CODEX_CHECK != 0 )) && printf 'codex check:\n%s\n' "${CHK:-}" >&2
    (( OC_BUILD != 0 )) && printf 'opencode build:\n%s\n' "${OGEN:-}" >&2
    (( OC_CHECK != 0 )) && printf 'opencode check:\n%s\n' "${OCHK:-}" >&2
    exit 1
    ;;
esac
exit 0
