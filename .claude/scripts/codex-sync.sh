#!/usr/bin/env bash
set -euo pipefail

# Mirror auto-compile — the deterministic "always compile after framework edits".
# Compiles BOTH runtime mirrors from the same Claude sources:
#   Codex — `pfm codex build` (single writer) + `pfm codex check`
#   OpenCode — `.claude/scripts/build-opencode.mjs generate` + `check`
#   mark — PostToolUse(Edit|Write): when the edited file is a Claude source either
#          mirror compiles from (.claude/**, any CLAUDE.md, $HOME/.claude/commands/**),
#          drop the repo-scoped dirty flag tmp/professor_codex_dirty.
#   sync — Stop: if the flag is present, run both mirrors' build+check; success
#          clears the flag silently, failure BLOCKS turn end (exit 2, reason on
#          stderr) so a broken mirror is fixed, never silently shipped. Respects
#          stop_hook_active — a block never loops; on a suppressed block the flag
#          stays set so the next turn retries. The block names WHICH of the four
#          stages failed and prints only that stage's output: build failure and
#          check failure are different defects with different repairs, and one
#          message covering both reports a compile broken while the writer says
#          PASS. When this script is itself broken, the stage name is what says
#          so — a bare "a mirror failed" is indistinguishable from any of them.
# Coverage (declared): sees Edit/Write TOOL calls only. A Bash-driven write (sed,
# redirect) to a Claude source does NOT set the flag — `pfm codex check` and
# `build-opencode.mjs check` in the pfm `structure` audit scope remain the
# backstop for that shape.
#
# `pfm codex build` is the SINGLE writer of the Codex mirror; build-opencode.mjs
# of the OpenCode mirror. Cross-runtime markers are not shared: each writer only
# reclaims files carrying its own (or its declared predecessors') marker.

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
    OUT=$(pfm codex build "$REPO_ROOT" 2>&1) && CODEX_BUILD=0 || CODEX_BUILD=$?
    CHK=$(pfm codex check "$REPO_ROOT" 2>&1) && CODEX_CHECK=0 || CODEX_CHECK=$?
    OGEN=""; OCHK=""
    # The Stop hook runs with the harness's PATH, which carries no mise shim
    # dir: a node installed through mise is invisible here. Add the shim and
    # user-bin dirs when node is not already on PATH (the user's login shell
    # sees the same node).
    if ! command -v node >/dev/null 2>&1; then
      for shim_dir in "$HOME/.local/share/mise/shims" "$HOME/.local/bin"; do
        [[ -d "$shim_dir" ]] && PATH="$shim_dir:$PATH"
      done
      export PATH
    fi
    if command -v node >/dev/null 2>&1; then
      OGEN=$(node "$REPO_ROOT/.claude/scripts/build-opencode.mjs" generate 2>&1) && OC_BUILD=0 || OC_BUILD=$?
      OCHK=$(node "$REPO_ROOT/.claude/scripts/build-opencode.mjs" check 2>&1) && OC_CHECK=0 || OC_CHECK=$?
    else
      OC_BUILD=127; OC_CHECK=127; OGEN="node not found"; OCHK="node not found"
    fi
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
    (( OC_BUILD != 0 )) && FAILED="${FAILED:+$FAILED, }opencode generate"
    (( OC_CHECK != 0 )) && FAILED="${FAILED:+$FAILED, }opencode check"
    printf 'codex-sync: %s failed after this turn'\''s framework edits — fix before ending the turn.\n' "$FAILED" >&2
    (( CODEX_BUILD != 0 )) && printf 'codex build:\n%s\n' "${OUT:-}" >&2
    (( CODEX_CHECK != 0 )) && printf 'codex check:\n%s\n' "${CHK:-}" >&2
    (( OC_BUILD != 0 )) && printf 'opencode generate:\n%s\n' "${OGEN:-}" >&2
    (( OC_CHECK != 0 )) && printf 'opencode check:\n%s\n' "${OCHK:-}" >&2
    exit 2
    ;;
esac
exit 0
