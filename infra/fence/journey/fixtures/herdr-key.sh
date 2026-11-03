#!/usr/bin/env bash
# The owner's foreground-size wrapper, over this run's attached Herdr client.
set -euo pipefail
socket="$PFM_TMUX_DIR/default"
tmux -S "$socket" resize-window -t owner:herdr-client -x 160 -y 50
case "$1" in
  tab) exec herdr --session "$JOURNEY_SESSION" tab create --workspace "$HERDR_ACTIVE_WORKSPACE_ID" --cwd "$HOME" --env PFM_AUTO_OPEN=pfm --focus ;;
  workspace) exec herdr --session "$JOURNEY_SESSION" workspace create --cwd "$HOME" --env PFM_AUTO_OPEN=pfm --focus ;;
  split|split-down)
    direction=right; [ "$1" != split-down ] || direction=down
    exec herdr --session "$JOURNEY_SESSION" pane split "$HERDR_ACTIVE_PANE_ID" --direction "$direction" --cwd "$HOME" --env PFM_AUTO_OPEN=pfm --focus ;;
  *) printf 'herdr-key: unknown operation %s\n' "$1" >&2; exit 2 ;;
esac
