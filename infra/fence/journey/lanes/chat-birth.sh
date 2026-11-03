#!/usr/bin/env bash
# Births retain every successful owner entry for the later journey lanes.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
. "$HERE/lib.sh"
export PYTHONPATH="$HERE"
export JOURNEY_LANE=chat-birth
FIXTURE="$HERE/fixtures/chat-birth"
: "${JOURNEY_OS:?runner must select linux or macos}"
export JOURNEY_TARGET="$JOURNEY_ROOT/birth-target.json"
SHELL_TARGET="$JOURNEY_ROOT/birth-shell.json"
CHAT="$JOURNEY_ROOT/birth-chat.json"
OWNER="$JOURNEY_ROOT/birth-owner.json"
jq '.owner' "$JOURNEY_CARRY" >"$OWNER" || exit 2
cp "$OWNER" "$JOURNEY_TARGET" || exit 2

. "$FIXTURE/entry.sh"
. "$FIXTURE/storm.sh"
. "$FIXTURE/stopped.sh"
. "$FIXTURE/forecast.sh"
. "$FIXTURE/boot.sh"
. "$FIXTURE/edge.sh"
. "$FIXTURE/topology.sh"
. "$FIXTURE/escapes.sh"

_birth_selector() { jq -er '.role // .origin' "$CHAT"; }

_birth_snapshot_ready() {
  python3 "$HERE/chat.py" snapshot "$JOURNEY_TARGET" "$JOURNEY_ROOT/birth-settled.json" >/dev/null || return 2
  python3 "$FIXTURE/projection.py" chat "$JOURNEY_ROOT/birth-settled.json" "$CHAT" >"$JOURNEY_ROOT/birth-settled-chat.json" || return 2
  test "$(jq -r '.status.state' "$JOURNEY_ROOT/birth-settled-chat.json")" = idle
}
_birth_action() {
  local id="$1" action="$2" door="$3"
  assert "$id" "$action@$door" \
    "$(python3 "$HERE/activity.py" observed-action "$JOURNEY_OBSERVATION" "$action" "$door")" "$JOURNEY_OBSERVATION"
}
_birth_assert() {
  local id="$1" name="$2" engine_code backend expected_cwd field
  engine_code="$(case "$ENGINE" in claude) printf cc ;; codex) printf cx ;; opencode) printf ox ;; esac)"
  backend="$(jq -er '.terminal.backend' "$CHAT")" || return 2
  expected_cwd="$CWD"
  case "$ENTRY" in new-from-picker) expected_cwd="$HOME/work/$PROJECT" ;; esac
  python3 "$FIXTURE/projection.py" chat "$JOURNEY_OBSERVATION" "$CHAT" >"$JOURNEY_ROOT/birth-observed.json" || return 2
  OBSERVED="$JOURNEY_ROOT/birth-observed.json"
  assert "$id" "$engine_code" "$(jq -r '.status.engine' "$OBSERVED")" "$OBSERVED"
  assert "$id" idle "$(jq -r '.status.state' "$OBSERVED")" "$OBSERVED"
  assert "$id" "$(jq -nc --arg name "$name" --arg cwd "$expected_cwd" --arg kind "live-$ENGINE" --arg project "$PROJECT" \
    '{name:$name,cwd:$cwd,kind:$kind,project:$project,account:"1",killed:"false"}')" \
    "$(jq -c '.rows | map({name,cwd,kind,project,account,killed}) | if length == 1 then .[0] else . end' "$OBSERVED")" "$OBSERVED"
  assert "$id" '[]' "$(jq -c --arg sid "$(jq -r '.session_id' "$CHAT")" '.stored.hidden | map(select(.uuid == $sid))' "$OBSERVED")" "$OBSERVED"
  assert "$id" "$(jq -r '.session_id' "$CHAT")" "$(jq -r --arg sid "$(jq -r '.session_id' "$CHAT")" \
    '.stored.launch | map(select(.session_id == $sid) | .session_id) | unique | join(",")' "$OBSERVED")" "$OBSERVED"
  assert "$id" '[1]' "$(jq -c '.stored.launch | map(.account) | unique' "$OBSERVED")" "$OBSERVED"
  assert "$id" '[1]' "$(jq -c '.stored.launch | map(.cache1h) | unique' "$OBSERVED")" "$OBSERVED"
  assert "$id" false "$(jq '.picker | any(.hidden or .killed)' "$OBSERVED")" "$OBSERVED"
  assert "$id" '["idle"]' "$(jq -c '.picker | map(.badge.state) | unique' "$OBSERVED")" "$OBSERVED"
  assert "$id" "$(jq -r '.terminal.pane' "$CHAT")" "$(jq -r '.terminal.identity.pane' "$OBSERVED")" "$OBSERVED"
  python3 "$FIXTURE/projection.py" engine-births "$JOURNEY_OBSERVATION" "$CHAT" >"$JOURNEY_OUT/beats/$PFM_BEAT/engine-births.json" || return 2
  assert "$id" 1 "$(jq 'length' "$JOURNEY_OUT/beats/$PFM_BEAT/engine-births.json")" "$JOURNEY_OUT/beats/$PFM_BEAT/engine-births.json"
  if [ "$ENGINE" = claude ]; then
    assert "$id" "[\"$HOME/.cc/1\"]" "$(jq -c 'map(.environment.CLAUDE_CONFIG_DIR) | unique' "$JOURNEY_OUT/beats/$PFM_BEAT/engine-births.json")" "$JOURNEY_OUT/beats/$PFM_BEAT/engine-births.json"
  fi
  if [ "$ENGINE" = codex ]; then
    assert "$id" "[\"$HOME/.codex\"]" "$(jq -c 'map(.environment.CODEX_HOME) | unique' "$JOURNEY_OUT/beats/$PFM_BEAT/engine-births.json")" "$JOURNEY_OUT/beats/$PFM_BEAT/engine-births.json"
  fi
  case "$ENTRY" in new-*|open-from-foreign-workspace)
    assert "$id" '[]' "$(jq -c --slurpfile before "${SHELL_TARGET%/*}/terminal-before.json" \
      'map(. as $born | select(any($before[0].births[]; .pid == $born.pid and .start == $born.start)))' \
      "$JOURNEY_OUT/beats/$PFM_BEAT/engine-births.json")" "${SHELL_TARGET%/*}/terminal-before.json" ;;
  esac
  if [ -z "$name" ] && [ "$ENTRY" != new-from-picker ]; then
    if [ "$ENTRY" = open-from-foreign-workspace ]; then
      poll foreign-caller-shell 10 python3 "$HERE/chat.py" shell-ready "$SHELL_TARGET" || return 2
    fi
    python3 "$HERE/terminal.py" read "$SHELL_TARGET" >"$JOURNEY_OUT/beats/$PFM_BEAT/caller-after.json" || return 2
  else cp "${SHELL_TARGET%/*}/terminal-before.json" "$JOURNEY_OUT/beats/$PFM_BEAT/caller-after.json" || return 2; fi
  python3 "$FIXTURE/projection.py" labels "$JOURNEY_OBSERVATION" "$CHAT" "$ENTRY" \
    "${SHELL_TARGET%/*}/terminal-before.json" "$JOURNEY_OUT/beats/$PFM_BEAT/caller-after.json" "$expected_cwd" "$name" \
    >"$JOURNEY_OUT/beats/$PFM_BEAT/labels.json" || return 2
  assert "$id" "$(jq -r '.expected' "$JOURNEY_OUT/beats/$PFM_BEAT/labels.json")" \
    "$(jq -r '.actual' "$JOURNEY_OUT/beats/$PFM_BEAT/labels.json")" "$(jq -r '.rule' "$JOURNEY_OUT/beats/$PFM_BEAT/labels.json"):$JOURNEY_OUT/beats/$PFM_BEAT/labels.json"
  if [ -z "$name" ] && [ "$ENTRY" != new-from-picker ]; then
    assert "$id" "$(jq -r '.caller.expected' "$JOURNEY_OUT/beats/$PFM_BEAT/labels.json")" \
      "$(jq -r '.caller.actual' "$JOURNEY_OUT/beats/$PFM_BEAT/labels.json")" "$JOURNEY_OUT/beats/$PFM_BEAT/labels.json"
  fi
  if [ "$ENTRY" = open-from-foreign-workspace ]; then
    python3 "$FIXTURE/projection.py" foreign "$JOURNEY_OBSERVATION" "$CHAT" \
      "${SHELL_TARGET%/*}/terminal-before.json" "$JOURNEY_OUT/beats/$PFM_BEAT/caller-after.json" \
      >"$JOURNEY_OUT/beats/$PFM_BEAT/foreign-workspace.json" || return 2
    assert "$id" "$(jq -cS '.expected' "$JOURNEY_OUT/beats/$PFM_BEAT/foreign-workspace.json")" \
      "$(jq -cS '.actual' "$JOURNEY_OUT/beats/$PFM_BEAT/foreign-workspace.json")" "$JOURNEY_OUT/beats/$PFM_BEAT/foreign-workspace.json"
  fi
  if [ "$backend" = herdr ]; then
    assert "$id" "$PROJECT" "$(jq -r '.terminal.workspace.workspace.label' "$OBSERVED")" "$OBSERVED"
    case "$EXPECTED_WORKSPACE" in label:*)
      assert "$id" false "$(jq --arg previous "$WORKSPACE" '.terminal.identity.workspace == $previous' "$OBSERVED")" "$OBSERVED" ;;
    *) assert "$id" "$EXPECTED_WORKSPACE" "$(jq -r '.terminal.identity.workspace' "$OBSERVED")" "$OBSERVED" ;;
    esac
    assert "$id" "$expected_cwd" "$(jq -r '.terminal.pane.cwd' "$OBSERVED")" "$OBSERVED"
    assert "$id" idle "$(jq -r '.terminal.agent_status.pane' "$OBSERVED")" "$OBSERVED"
    assert "$id" '{}' "$(jq -c '.terminal.indicators.pane | with_entries(select(.key == "pfm_working" or .key == "pfm_blocked"))' "$OBSERVED")" "$OBSERVED"
    assert "$id" live "$(jq -r '.stored.seat | map(select(.state == "live")) | if length == 1 then .[0].state else . end' "$OBSERVED")" "$OBSERVED"
    assert "$id" "$expected_cwd" "$(jq -r '.stored.recipe.cwd' "$OBSERVED")" "$OBSERVED"
    if [ "$ENGINE" = claude ]; then
      assert "$id" "CLAUDE_CONFIG_DIR=$HOME/.cc/1" "$(jq -r '.stored.recipe.env[] | select(startswith("CLAUDE_CONFIG_DIR="))' "$OBSERVED")" "$OBSERVED"
    fi
  else
    assert "$id" n/a:tmux "$(jq -r '.terminal.workspace' "$OBSERVED")" "$OBSERVED"
    assert "$id" n/a:tmux "$(jq -r '.terminal.tab' "$OBSERVED")" "$OBSERVED"
    assert "$id" n/a:tmux "$(jq -r '.terminal.agent_status' "$OBSERVED")" "$OBSERVED"
    assert "$id" n/a:tmux "$(jq -r '.terminal.indicators' "$OBSERVED")" "$OBSERVED"
    assert "$id" "$expected_cwd" "$(jq -r '.terminal.pane.pane_current_path' "$OBSERVED")" "$OBSERVED"
    assert "$id" "$(jq -r '.terminal.socket' "$CHAT")" "$(jq -r '.terminal.identity.socket' "$OBSERVED")" "$OBSERVED"
  fi
  python3 "$FIXTURE/projection.py" composer "$JOURNEY_OBSERVATION" "$CHAT" >"$JOURNEY_OUT/beats/$PFM_BEAT/composer.txt" || return 2
  case "$FACTORS" in
    trust_dialog_rows=unnumbered,*) golden "$id" chat-birth/trust-unnumbered.txt "$JOURNEY_OUT/beats/$PFM_BEAT/composer.txt" ;;
    launch_state=folder_trust_dialog,*) golden "$id" chat-birth/trust-numbered.txt "$JOURNEY_OUT/beats/$PFM_BEAT/composer.txt" ;;
    *) golden "$id" "chat-birth/$ENGINE-composer.txt" "$JOURNEY_OUT/beats/$PFM_BEAT/composer.txt" ;;
  esac
}
_birth_select() {
  local rc
  if [ -n "$BIRTH_SELECTION" ]; then
    python3 "$HERE/chat.py" select "$JOURNEY_BRANCH" "$BIRTH_SELECTION" >"$CHAT" || return $?
    jq '.terminal' "$CHAT" >"$JOURNEY_TARGET"
    return
  fi
  python3 "$HERE/chat.py" select "$JOURNEY_BRANCH" new-typed >"$CHAT"; rc=$?
  if [ "$rc" -eq 1 ]; then python3 "$HERE/chat.py" select "$JOURNEY_BRANCH" new-from-cli >"$CHAT" || return $?;
  elif [ "$rc" -ne 0 ]; then return "$rc"; fi
  jq '.terminal' "$CHAT" >"$JOURNEY_TARGET"
}
_birth_refresh() {
  python3 "$HERE/chat.py" refresh "$JOURNEY_BRANCH" "$(_birth_selector)" >"$CHAT.tmp" || return 2
  mv "$CHAT.tmp" "$CHAT" && jq '.terminal' "$CHAT" >"$JOURNEY_TARGET"
}
_birth_named() {
  _birth_refresh || return 2
  test "$(jq -r '.name' "$CHAT")" = "$WANT_NAME"
}
_birth_rename() {
  local id="$1" entry="$2" sid record offset=0 receipt
  _birth_select || { blocked "new-typed@$JOURNEY_BRANCH"; return 1; }
  _birth_snapshot_ready || { fail 'rename predecessor could not be observed idle'; return 1; }
  if _birth_escape agentless; then _birth_agentless "$id" || { fail 'agentless rename evidence differs'; return 1; }; fi
  python3 "$FIXTURE/projection.py" engine-births "$JOURNEY_ROOT/birth-settled.json" "$CHAT" >"$JOURNEY_OUT/beats/$PFM_BEAT/rename-before.json" || return 2
  sid="$(jq -er '.session_id' "$CHAT")" || return 2
  record="$(jq -er '.records' "$CHAT")/prompts" || return 2
  [ ! -f "$record" ] || offset="$(wc -c <"$record")"
  case "$entry" in
    rename-from-cli) WANT_NAME=atlas-review; _birth_input command "$OWNER" pfm chat name "$sid" "$WANT_NAME" ;;
    rename-from-mcp) WANT_NAME=atlas-desk; python3 "$HERE/chat.py" inline "$JOURNEY_TARGET" "$FIXTURE/name-mcp.json" ;;
    rename-inside-engine-updates-tab)
      WANT_NAME=atlas-notes
      if [ "$ENGINE" = opencode ]; then
        python3 "$HERE/chat.py" inline "$JOURNEY_TARGET" "$FIXTURE/opencode-retitle.json" birth-retitle
      else _birth_input type "$JOURNEY_TARGET" "/rename $WANT_NAME"; fi ;;
    rename-after-session-replaced)
      WANT_NAME=atlas-renamed
      _birth_input type "$JOURNEY_TARGET" /clear && poll replaced-session 10 _birth_session_replaced "$sid" &&
        _birth_input command "$OWNER" pfm chat name "$(jq -r '.session_id' "$CHAT")" "$WANT_NAME" ;;
  esac || { fail "rename owner entry failed: $entry"; return 1; }
  poll renamed-carried-chat 2 _birth_named || { fail 'the carried name did not update within two seconds'; return 1; }
  poll renamed-chat-idle 5 _birth_snapshot_ready || { fail 'renamed chat did not settle idle'; return 1; }
  observe || { fail 'renamed chat bundle could not be read'; return 1; }
  _birth_assert "$id" "$WANT_NAME" || { fail 'renamed chat projection differs'; return 1; }
  assert "$id" "$(cat "$JOURNEY_OUT/beats/$PFM_BEAT/rename-before.json")" \
    "$(cat "$JOURNEY_OUT/beats/$PFM_BEAT/engine-births.json")" "$JOURNEY_OUT/beats/$PFM_BEAT/engine-births.json"
  if [ "$entry" = rename-inside-engine-updates-tab ] || [ "$entry" = rename-after-session-replaced ]; then
    receipt="$JOURNEY_OUT/beats/$PFM_BEAT/engine-input.txt"
    tail -c "+$((offset + 1))" "$record" >"$receipt" || { fail 'native rename input receipt unreadable'; return 1; }
    if [ "$entry" = rename-inside-engine-updates-tab ]; then
      if [ "$ENGINE" = opencode ]; then
        assert "$id" 'birth-retitle mock-engine: [{"type":"native_title","name":"atlas-notes"},{"type":"turn","reply":"birth-retitled","busy_ms":0}]' \
          "$(cat "$receipt")" "engine-input:$receipt"
      else assert "$id" '/rename atlas-notes' "$(cat "$receipt")" "engine-input:$receipt"; fi
    else
      assert "$id" /clear "$(cat "$receipt")" "engine-input:$receipt"
      assert "$id" "$sid" "$(jq -r '.lineage_ids | join(",")' "$CHAT")" "$CHAT"
      assert "$id" "$sid" "$(jq -r '.stored.hidden | map(.uuid) | unique | join(",")' "$OBSERVED")" "$OBSERVED"
    fi
  fi
  case "$entry" in rename-from-cli|rename-after-session-replaced) _birth_action "$id" chat.name typed ;;
    rename-from-mcp) _birth_action "$id" chat.name mcp ;;
  esac
  pass
}
_birth_session_replaced() { _birth_refresh && test "$(jq -r '.session_id' "$CHAT")" != "$1"; }
_birth_factors_ready() {
  if _birth_edge_factor || _birth_topology_factor || _birth_cancel_factor || _birth_early_factor; then return 0; fi
  case "$FACTORS" in
    none|multiplexer=herdr,door=spawn|multiplexer=herdr,chat_name=unnamed|multiplexer=herdr,runtime_event=rename|engine=codex,runtime_event=rename|codex_home=ambient_real_home,engine=codex) return 0 ;;
    trust_dialog_rows=unnumbered,engine=claude_code,spawn_order=readiness_before_trust|launch_state=folder_trust_dialog,backend=tmux_remain_on_exit,engine=claude_code) return 0 ;;
    process_group=private_pgid,descendant_state=holds_stdout_pipe,helper=codex_app_server) return 0 ;;
    last_pane=closed,backend=tmux,child_exit=before_waiter_connects|launcher_exit=before_wait_registered,backend=tmux_wait_for) return 0 ;;
    *) return 1 ;;
  esac
}

INDEX=0
while IFS=$'\t' read -r SLUG BRANCH IDS ENTRY SHELL_START WORKSPACE_STATE FACTORS; do
  case "$BRANCH" in "$JOURNEY_OS"+*) ;; *) continue ;; esac
  INDEX=$((INDEX + 1))
  export JOURNEY_BRANCH="$BRANCH"
  IFS=+ read -r OS TERMINAL PLACE ENGINE <<<"$BRANCH"
  CWD="$HOME/work/atlas"
  case "$PLACE" in subfolder) CWD="$CWD/docs/specs" ;; worktree) CWD="$CWD/.worktrees/flight-export" ;; esac
  PROJECT=atlas; WORKSPACE=atlas; WORKSPACE_KEY=; START_CWD="$CWD"; BIRTH_SELECTION=
  EXPECTED_WORKSPACE="$(jq -r '.workspaces.atlas.id' "$JOURNEY_CARRY")"
  [ "$SHELL_START" != home ] || START_CWD="$HOME"
  [ "$ENTRY" != open-from-foreign-workspace ] || WORKSPACE=beacon
  PREPARED=0
  case "$ENTRY" in
    new-typed-tmux-server-stopped)
      if [ ! -e "$JOURNEY_OUT/$JOURNEY_LANE.failed" ]; then _birth_stopped_prepare; PREPARED=$?; fi ;;
    storm-of-births)
      if [ ! -e "$JOURNEY_OUT/$JOURNEY_LANE.failed" ]; then _birth_storm_prepare; PREPARED=$?; fi ;;
    new-from-picker|new-typed|new-from-cli|open-from-foreign-workspace)
      if _birth_factors_ready && [ ! -e "$JOURNEY_OUT/$JOURNEY_LANE.failed" ]; then
        _birth_prepare "$SLUG"; PREPARED=$?
      fi ;;
  esac
  if [ "$PREPARED" -eq 0 ] && _birth_factors_ready && [ ! -e "$JOURNEY_OUT/$JOURNEY_LANE.failed" ]; then
    _birth_forecast_prepare; PREPARED=$?
  fi
  if beat "$SLUG" $IDS; then
    if [ "$PREPARED" -ne 0 ]; then fail 'birth shell/scenario prelude could not establish the requested branch'; continue; fi
    if ! _birth_factors_ready; then
      fail "required isolated setup is not available: workspace=$WORKSPACE_STATE factors=$FACTORS"
      continue
    fi
    _birth_workspace_assert "${IDS%% *}" before || { fail 'workspace factors differ before entry'; continue; }
    _birth_forecast_assert "${IDS%% *}" || { fail 'forecast factors differ before entry'; continue; }
    if [ "$FACTORS" = multiplexer=herdr,door=spawn ]; then
      assert "${IDS%% *}" herdr "$(jq -r '.backend' "${SHELL_TARGET%/*}/terminal-before.json")" "${SHELL_TARGET%/*}/terminal-before.json" || {
        fail 'Herdr spawn forecast did not start from a Herdr pane'; continue;
      }
    fi
    case "$ENTRY" in
      new-typed-tmux-server-stopped) _birth_stopped "$IDS" ;;
      storm-of-births) _birth_storm "$IDS" ;;
      new-from-picker|new-typed|new-from-cli|open-from-foreign-workspace) _birth_new "$SLUG" "$IDS" "$ENTRY" ;;
      rename-*) _birth_rename "$IDS" "$ENTRY" ;;
      *) fail "required birth entry has no public setup door: $ENTRY" ;;
    esac
  fi
done <"$FIXTURE/rows.tsv"
test ! -e "$JOURNEY_OUT/$JOURNEY_LANE.failed"
