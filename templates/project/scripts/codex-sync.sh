#!/usr/bin/env bash
set -euo pipefail

# Codex-mirror auto-compile — the deterministic "always compile after framework edits".
#   mark — PostToolUse(Edit|Write): when the edited file is a Claude source the mirror
#          compiles from (.claude/**, any CLAUDE.md, $HOME/.claude/commands/**), drop
#          the repo-scoped dirty flag tmp/professor_codex_dirty.
#   sync — Stop: if the flag is present, run `pfm codex build` then `pfm codex
#          check`; success clears the flag silently, failure BLOCKS
#          turn end (exit 2, reason on stderr) so a broken mirror is fixed, never
#          silently shipped. Respects stop_hook_active — a block never loops; on a
#          suppressed block the flag stays set so the next turn retries.
# Coverage (declared): sees Edit/Write TOOL calls only. A Bash-driven write (sed,
# redirect) to a Claude source does NOT set the flag — `pfm codex check` in the
# pfm `structure` audit scope remains the backstop for that shape.
#
# `pfm codex build` is the SINGLE writer of the mirror. The legacy JS compiler is
# retained unwired for reference only: two writers stamp different generated
# markers into the same files, so each rewrites the other's output on every run.

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
    # A host without pfm silently skips the auto-compile — clear the flag so it
    # never blocks turn end (mirror rebuild happens on the next pfm-equipped run).
    [[ -x "$PFM_BIN" ]] || { rm -f "$FLAG"; exit 0; }
    OUT=$("$PFM_BIN" codex build "$REPO_ROOT" 2>&1) && BUILD=0 || BUILD=$?
    CHK=$("$PFM_BIN" codex check "$REPO_ROOT" 2>&1) && CHECK=0 || CHECK=$?
    if (( BUILD == 0 && CHECK == 0 )); then
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
    (( BUILD != 0 )) && FAILED="${FAILED:+$FAILED, }codex build"
    (( CHECK != 0 )) && FAILED="${FAILED:+$FAILED, }codex check"
    printf 'codex-sync: %s failed after this turn'\''s framework edits — fix before ending the turn.\n' "$FAILED" >&2
    (( BUILD != 0 )) && printf 'codex build:\n%s\n' "${OUT:-}" >&2
    (( CHECK != 0 )) && printf 'codex check:\n%s\n' "${CHK:-}" >&2
    exit 2
    ;;
esac
exit 0
