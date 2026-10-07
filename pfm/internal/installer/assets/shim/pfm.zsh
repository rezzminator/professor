#!/usr/bin/env zsh
# PFM shell action adapter. A missing binary reports an error and stops sourcing.

if [[ ! -x "$HOME/.local/bin/pfm" ]]; then
  print -u2 -- "pfm: $HOME/.local/bin/pfm is missing or not executable"
  return 1
fi

export CLAUDE_DISABLE_ADOPT=1
export CLAUDE_CODE_DISABLE_BG_EXIT_HANDOFF=1
export CLAUDE_CODE_STOP_HOOK_BLOCK_CAP=2

# Re-sourcing an updated install also removes legacy functions and aliases
# already loaded in this shell. External executables (including /usr/bin/cc)
# are untouched.
if (( ${+precmd_functions} )); then
  precmd_functions=("${(@)precmd_functions:#_cc_auto_open}")
fi
for _pfm_retired in cc cc1 cc2 cc3 cc4 cc-clean cc-ls cc-open cc-revive cc-swap \
  _cc_accounts _cc_acct _cc_agents _cc_ago _cc_arm1h _cc_c1h_tty _cc_cachew \
  _cc_cfgdir _cc_dir _cc_fleet_eval _cc_has _cc_hsize _cc_in_bunker _cc_isbg \
  _cc_isgpt _cc_label _cc_lastprompt _cc_medal _cc_meta _cc_metac _cc_newprompts \
  _cc_open_gate _cc_open_lock _cc_own_terminal _cc_owns_terminal _cc_primary \
  _cc_resume_acct _cc_run _cc_selfswitch _cc_solo _cc_stat _cc_tui_call _cc_auto_open \
  cc_children_args cc_detach cc_epoch cc_find_meta cc_find_newest cc_listening \
  cc_mem cc_mtime cc_mtime0 cc_pane_of cc_penv cc_pfiles cc_ppid cc_pstart \
  cc_sed_i cc_session_live cc_size cc_size0 cc_timeout cc_trylock cc_unlock \
  cc_master_item cc_account_meta cc_item_sfx cc_read_item cc_expires_of \
  cc_freshest_token cc_promote _pfm_eval _pfm_primary; do
  unfunction "$_pfm_retired" 2>/dev/null || true
  unalias "$_pfm_retired" 2>/dev/null || true
done
unset _pfm_retired _cc_auto_what PFM_CLAUDE_PROMPTED

# cx — a CODEX chat on the same per-chat-server pattern, socket prefix cx-* instead of cc-*.
# The prefix IS the engine marker: codex writes no statusline breadcrumbs and no ~/.claude
# transcript, so pfm recognizes (and lists) a live Codex chat by socket name alone. Claude
# accounts / ⚡1h don't apply — codex has its own single auth (~/.codex).
cx() {
  # A non-interactive shell (an agent tool shell replaying a snapshot, a script) has no terminal
  # to attach or hand back, and a snapshot may have dropped every `_`-prefixed helper — so cx
  # refuses there, loudly, and names the door that starts a detached Codex chat instead.
  [[ -o interactive ]] || {
    print -u2 -r -- "cx: needs an interactive terminal; from a script use: pfm chat new {name} --engine codex"
    return 1
  }
  local sock="cx-$(date +%s)-$$-$RANDOM"
  # PER-ELEMENT quoting, then join: "${(q)@}" joins the
  # array into ONE word FIRST and quotes that, so `cx --resume abc123` arrives as a single
  # escaped argv element ("--resume\ abc123") and codex rejects it as one unknown flag.
  local run="\"$HOME/.local/bin/pfm\" internal codex-launch ${(j: :)${(@q)@}}"
  _cx_server "$sock" "$PWD" "$run" || return
  if _pfm_selfswitch "$sock"; then :                          # already inside it → switch, never nest
  elif _pfm_in_bunker; then TMUX= exec tmux -L "$sock" attach # viewport dies with the tab
  elif _pfm_owns_terminal; then TMUX= exec tmux -L "$sock" attach # bare terminal dies with the harness
  else
    TMUX= tmux -L "$sock" attach
    _pfm_own_terminal $?   # bare terminal: the chat took it, the chat closes it
  fi
}

# _cx_server <sock> <cwd> <run> — create the chat's tmux server DETACHED through pfm's one
# chat-server creator, the same one every other door calls: it is born with the tmux.titles
# policy (so the VS Code tab follows the window name name-sync converges onto the codex
# thread_name), automatic-rename off, and its engine's window name. A failure is pfm's own
# stderr line and a nonzero return, so `cx` never attaches to a server that does not exist.
_cx_server() { "$HOME/.local/bin/pfm" internal chat-server "$@"; }

# _pfm_in_bunker — true when this shell IS a vsct bunker pane. Chat opens from a bunker exec
# INTO the tmux client: the viewport dies with the tab instead of lingering as an orphaned
# husk that the next fresh terminal adopts (the ORCHESTRATOR ambush). Chats themselves live
# on their own cc-* servers and are untouched. Never true inside a chat pane or a plain
# shell — those keep child-spawned clients (a Bash-tool shell must never be exec'd away).
_pfm_in_bunker() { [[ "${TMUX%%,*}" == */vsct ]] }

# _pfm_owns_terminal — true when replacing this shell is safe and gives the
# harness structural ownership of the terminal. Scripts, pipes and shells
# inside another chat never qualify.
_pfm_owns_terminal() {
  [[ -o interactive && -t 0 && -t 1 ]] || return 1
  [[ -z "$TMUX" ]] || _pfm_in_bunker
}

# _pfm_own_terminal <status> — the terminal belongs to the harness that was typed into it, so
# a harness that ENDS takes the terminal with it (the VS Code tab closes) instead of falling
# back to a prompt nobody asked for. Three guards keep it from eating anything else: only an
# interactive shell on a real tty is ever ended, so a script, a Bash-tool shell and a piped
# run return untouched; only a caller that actually ran a harness calls it, so the picker
# (pfm) still escapes to the prompt on Esc; and a NON-ZERO exit holds the terminal for a
# keypress first — a crash the tab swallowed is a crash that never happened. zsh's refusal to
# exit while jobs are suspended is the right refusal, and is left alone.
_pfm_own_terminal() {
  local exit_status="${1:-0}"
  _pfm_owns_terminal || return "$exit_status"
  # A bare terminal or a bunker pane exists to hold a harness, so it goes when the harness
  # goes. Inside any OTHER tmux — a chat pane, an ad-hoc work window — the terminal is
  # someone else's and closing it would take the host down too.
  if (( exit_status )); then
    print -u2 -- "pfm: harness exited $exit_status — press any key to close this terminal"
    read -k1 -s
  fi
  exit "$exit_status"
}

# _pfm_tui_call — true when these arguments make the harness take over the screen. A print,
# version or subcommand call writes its answer INTO this terminal and hands it back, so that
# terminal is not the harness's to close. Resume flags are absent on purpose: `claude -r` and
# `codex resume` open the full TUI and own the terminal like any other chat.
_pfm_tui_call() {
  local argument
  for argument in "$@"; do
    case "$argument" in
      -p|--print|-v|--version|-h|--help|doctor|update|install|mcp|config|migrate-installer|login|logout|exec|proto|apply)
        return 1 ;;
    esac
  done
  return 0
}

# A harness typed straight into a terminal closes that terminal on exit, exactly as a fleet
# chat does. claude bypasses recursion by calling the managed absolute launcher path, while
# codex uses `command` to resolve the external command without re-entering this wrapper.
# Only this shell's own typing is affected — pfm, hooks and scripts exec the binary and never
# see these functions. An agent tool shell replays a snapshot that keeps `claude`/`codex` but drops
# every `_`-prefixed helper, and is not interactive — the guard keeps it from calling them.
claude() {
  "$HOME/.local/bin/claude" "$@"
  local exit_status=$?
  [[ -o interactive ]] && _pfm_tui_call "$@" && _pfm_own_terminal "$exit_status"
  return "$exit_status"
}

codex() {
  command codex "$@"
  local exit_status=$?
  [[ -o interactive ]] && _pfm_tui_call "$@" && _pfm_own_terminal "$exit_status"
  return "$exit_status"
}

# pfm — the picker typed into a bare terminal hands that terminal to the chat it opens, as `cx`
# does. With stdout captured, pfm draws the picker on /dev/tty and prints the chosen action as
# one line (action.Dispatch); this shell runs it, then closes when the chat ends or is detached,
# so no prompt is left behind to read the terminal's late colour-query replies as typed input.
# Esc prints nothing and returns to the prompt; a failed pfm returns its status. Any argument, a
# script, a shell inside another tmux, or a snapshot shell without the helpers runs pfm as is.
pfm() {
  if (( $# )) || [[ ! -o interactive ]] || (( ! ${+functions[_pfm_owns_terminal]} )) ||
    ! _pfm_owns_terminal; then
    "$HOME/.local/bin/pfm" "$@"
    return
  fi
  local line exit_status
  line="$("$HOME/.local/bin/pfm")"
  exit_status=$?
  (( exit_status == 0 )) && [[ -n "$line" ]] || return "$exit_status"
  eval "$line"
  _pfm_own_terminal $?
}

# _pfm_selfswitch prevents attaching a Codex server inside itself.
_pfm_selfswitch() {
  local sock="$1" w
  [[ -n "${TMUX:-}" && "${TMUX%%,*}" == */"$sock" ]] || return 1
  # the chat's window = the one whose pane runs the engine, in DESCENDING order of certainty and
  # lowest window index first within each tier. An exact engine name is proof; `node` and the bare
  # version string are only hints — codex renders as its node wrapper and claude as a bare version
  # in some states, but a shell window running any node process wears the same shape, and matching
  # it first would switch the operator to that shell, i.e. the very "wrong window" this fixes.
  local panes; panes="$(tmux -L "$sock" list-panes -a -F '#{window_index}'$'\t''#{pane_current_command}' 2>/dev/null | sort -n)"
  w="$(print -r -- "$panes" | awk -F'\t' '$2 ~ /^(claude|codex)$/ { print $1; exit }')"
  [[ -n "$w" ]] || w="$(print -r -- "$panes" | awk -F'\t' '$2 ~ /^node$/ || $2 ~ /^[0-9]+\./ { print $1; exit }')"
  # engine window unidentifiable (an unexpected pane command) → the lowest window index, where every
  # cc-/cx- server puts the chat at birth. Announce only what actually happened: claiming a switch
  # that never ran would send the operator back to a screen that did not change.
  [[ -n "$w" ]] || w="$(print -r -- "$panes" | awk -F'\t' 'NR==1 { print $1 }')"
  if [[ -n "$w" ]] && tmux select-window -t "$w" 2>/dev/null; then
    print -u2 -- "pfm: already inside this chat's tmux — switched to its window (a session must never nest inside itself)"
  else
    print -u2 -- "pfm: already inside this chat's tmux — refusing to nest it inside itself; switch windows yourself (prefix + w)"
  fi
  return 0
}

# Launch once at the first prompt, after the complete shell startup. Legacy
# terminal-profile variables are consumed without reviving retired commands.
if [[ -o interactive && -n "${PFM_AUTO_OPEN:-}${CC_AUTO_OPEN:-}${VSCODE_AUTO_CC:-}" ]]; then
  if [[ -n "${CLAUDECODE:-}" ]]; then
    # Only a terminal profile sets these variables, and they are unset before arming, so they never
    # reach a chat's own shells (which are never interactive anyway). Both markers together mean
    # the APP that opened this terminal was itself launched from inside a chat (VS Code relaunched
    # by a chat's `code .`, for one) and handed the chat's identity to every terminal it opens.
    # Skipping here turned every new terminal into a dead prompt; clear what the app leaked — the
    # same keys the VS Code profile nulls — and open as normal. Say so on a real tty only.
    [[ -t 2 ]] && print -u2 -- "pfm: cleared a chat environment (CLAUDECODE=1) this terminal inherited from its app — relaunch the app outside any chat to stop it"
    unset CLAUDECODE CLAUDE_CODE_SESSION_ID CLAUDE_CODE_CHILD_SESSION TMUX TMUX_PANE
  fi
  # Read the value, then unset BEFORE arming, both spellings. The variable is exported by the
  # terminal profile, so it is inherited: a shell opened inside the chat would open a chat
  # inside the chat.
  typeset -g _pfm_auto_what="${PFM_AUTO_OPEN:-${CC_AUTO_OPEN:-$VSCODE_AUTO_CC}}"
  unset PFM_AUTO_OPEN CC_AUTO_OPEN VSCODE_AUTO_CC
  autoload -Uz add-zsh-hook
  _pfm_auto_open() {
    # Disarm FIRST. The command below RETURNS — when the chat exits, or when the picker is
    # dismissed — and the shell then draws another prompt. A hook still registered at that
    # moment is a terminal that reopens whatever you just closed, forever.
    add-zsh-hook -d precmd _pfm_auto_open
    unfunction _pfm_auto_open
    # The value chooses, from a WHITELIST — never `eval`, and never run as-is. It arrives from
    # the environment, which a terminal profile, a parent process or an ssh client can set.
    local cmd
    case "$_pfm_auto_what" in
      cx|codex)     cmd=cx ;;                 # a fresh Codex chat
      *)           cmd=pfm ;;                # retired and unknown values open the picker
    esac
    unset _pfm_auto_what
    $cmd
  }
  add-zsh-hook precmd _pfm_auto_open
fi
