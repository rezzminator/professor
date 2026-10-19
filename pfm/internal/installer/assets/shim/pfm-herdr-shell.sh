#!/bin/sh
# Herdr's login builder replaces SHELL with default_shell and uses a login
# argv0. A script loses that argv0 at its shebang: recover both the real shell
# and login intent here. Non-login launches retain the user's original SHELL.
pfm_wrapper=__PFM_WRAPPER__
pfm_real_shell=__PFM_REAL_SHELL__
if [ "${SHELL:-}" = "$pfm_wrapper" ]; then
  SHELL=$pfm_real_shell
  set -- -l "$@"
else
  SHELL=${SHELL:-$pfm_real_shell}
fi
# A misconfigured installation must not exec itself recursively.
if [ "$SHELL" = "$pfm_wrapper" ] || [ "$SHELL" = "$0" ]; then
  SHELL=/bin/sh
fi
if [ ! -x "$SHELL" ]; then
  printf 'pfm herdr shell: shell %s is not executable; using /bin/sh — check your account login shell and rerun pfm install\n' "$SHELL" >&2
  SHELL=/bin/sh
fi
export SHELL

# Only the installed zsh shim consumes the first-prompt request.
pfm_interactive=0
case "${SHELL##*/}" in zsh) pfm_interactive=1 ;; esac
for pfm_arg in "$@"; do
  case "$pfm_arg" in
    --login|--interactive) ;;
    --*) pfm_interactive=0 ;;
    -*c*|-*s*) pfm_interactive=0 ;;
    -*) ;;
    *) pfm_interactive=0 ;;
  esac
done
# The shim consumes these before arming its one-shot first-prompt hook.
# Clear inherited requests too: a command pane must never arm the picker.
unset PFM_AUTO_OPEN CC_AUTO_OPEN VSCODE_AUTO_CC
# Scope the guard to this pane: a Herdr server started from a pane may inherit
# it, but a new pane has its own identity and must still open the picker.
pfm_pane=${HERDR_SOCKET_PATH:-}:${HERDR_PANE_ID:-1}
if [ "$pfm_interactive" = 1 ] && [ -t 0 ] && [ -t 1 ] &&
   [ "${PFM_HERDR_SHELL:-}" != "$pfm_pane" ] && [ -x "$HOME/.local/bin/pfm" ]; then
  PFM_AUTO_OPEN=pfm
  PFM_HERDR_SHELL=$pfm_pane
  export PFM_AUTO_OPEN PFM_HERDR_SHELL
fi
exec "$SHELL" "$@"
