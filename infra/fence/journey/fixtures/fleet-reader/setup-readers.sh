#!/usr/bin/env bash
# Owned reader actors are born through the shared public chat prelude.
_reader_statusline_account_ready() {
  local result
  python3 "$HERE/chat.py" refresh "$BRANCH" "$READER_STATUSLINE_ROLE" >"$JOURNEY_ROOT/reader-statusline.json"
  result=$?
  case "$result" in
    0) [ "$(jq -r '.account' "$JOURNEY_ROOT/reader-statusline.json")" = 2 ] ;;
    1) return 1 ;;
    *) return 2 ;;
  esac
}

_reader_statusline_role() {
  case "$SLUG" in
    statusline-renders) printf '%s\n' reader-statusline ;;
    forecast-harness-payload-omits-usage-window-shared-cache-present-fresh-engine-claude-code) printf '%s\n' reader-statusline-cache ;;
    forecast-os-unix-caller-lifetime-long-lived-child-state-detached-exited-unreaped) printf '%s\n' reader-paced-refresh ;;
    statusline-warns-on-partial-account-read) printf '%s\n' reader-statusline-account ;;
    *) return 1 ;;
  esac
}

_reader_statusline_prelude() {
  READER_STATUSLINE_ROLE="$(_reader_statusline_role)" || return 2
  local seed="$JOURNEY_ROOT/$READER_STATUSLINE_ROLE-$BRANCH" actor="$JOURNEY_ROOT/reader-statusline.json"
  python3 "$FIXTURES/statusline.py" seed "$seed" || return 2
  python3 "$HERE/chat.py" ensure-role "$BRANCH" "$READER_STATUSLINE_ROLE" "$READER_STATUSLINE_ROLE-${BRANCH//+/-}-${PFM_RUN: -8}" "$seed.seed.json" >"$actor" || return 2
  if [ "$(jq -r '.account' "$actor")" != 2 ]; then
    if [ "$(jq -r '.terminal.backend' "$actor")" = tmux ]; then
      python3 "$HERE/terminal.py" command "$READER_SHELL" pfm chat reload --sock "$(jq -r '.terminal.socket' "$actor")" --pane "$(jq -r '.terminal.pane' "$actor")" --account 2 || return 2
    else
      python3 "$HERE/terminal.py" command "$READER_SHELL" pfm chat reload --pane "$(jq -r '.terminal.pane' "$actor")" --account 2 || return 2
    fi
    poll reader-statusline-account-two 40 _reader_statusline_account_ready || return 2
  fi
}

_reader_partial_prelude() {
  local engine folder="$JOURNEY_ROOT/reader-partial" branch="$READER_OS+$HERDR+project+claude"
  case "$SLUG" in
    headless-names-partial-usage|headless-retains-valid-answer-after-partial-read) return 0 ;;
    statusline-warns-on-partial-account-read) _reader_statusline_prelude; return "$?" ;;
  esac
  python3 "$HERE/chat.py" ensure-role "$branch" reader-partial-bad "reader-bad-${PFM_RUN: -8}" "$FIXTURES/partial-seed.json" >"$folder-bad.json" || return 2
  case "$SLUG" in
    index-keeps-good-engine-sessions-after-bad-record)
      for engine in claude codex opencode; do
        python3 "$HERE/chat.py" ensure-role "$READER_OS+$HERDR+project+$engine" reader-index-good "reader-index-$engine-${PFM_RUN: -8}" "$FIXTURES/partial-seed.json" >"$folder-$engine.json" || return 2
      done ;;
    tokens-names-partial-transcript-read)
      python3 "$HERE/chat.py" ensure-role "$branch" reader-token-good "reader-token-${PFM_RUN: -8}" "$FIXTURES/partial-token-seed.json" >"$folder-token.json" || return 2 ;;
    name-sync-preserves-good-name-after-partial-read)
      python3 "$HERE/chat.py" ensure-role "$branch" reader-name-good "reader-name-${PFM_RUN: -8}" "$FIXTURES/partial-seed.json" >"$folder-name.json" || return 2 ;;
  esac
}

_reader_process_prelude() {
  local role=reader-watch
  case "$SLUG" in
    reap-preview-retains-live-foreign-owner) role=reader-reap ;;
    theme-keeps-healthy-rows-beside-stopped-tmux) role=reader-theme ;;
  esac
  python3 "$HERE/chat.py" ensure-role "$BRANCH" "$role" "$role-${BRANCH//+/-}-${PFM_RUN: -8}" "$HERE/fixtures/chat-mesh/seed.json" >"$JOURNEY_ROOT/reader-process.json"
}
