#!/usr/bin/env bash
# Journey lane API (source this file; runner supplies JOURNEY_* and PFM_RUN):
# beat SLUG ID... && { action; assert ID EXPECTED ACTUAL [SOURCE]; observe; pass; }
# assert compares exact strings; golden ID RELATIVE-GOLDEN ACTUAL-FILE compares bytes.
# For engine-owned input, SOURCE=engine-input:ABSOLUTE-RECEIPT verifies the recorded input slice.
# For owner receipts, SOURCE=owner-input:ABSOLUTE-PREFIX compares literal compact JSON argv
# with PREFIX.dispatch.json .argv; separately assert parsed output and exit status.
# Owner receipts also admit only the exact argv ["claude","--version"]; all dispatch/context/completion guards remain.
# pass [DETAIL] | fail REASON | known GAP | blocked PREDECESSOR | skip-because REASON
# need LABEL CHECK-FUNCTION CREATE-FUNCTION; checks return 0 present, 1 absent, 2 error.
# spends ACCOUNT | expect-log REGEX; the latter must match an error in this beat.
# poll LABEL SECONDS COMMAND [ARG...]; bounded condition, 0 ready/1 pending/2 unreadable.
# observe [TARGET-JSON] captures MERGE §3, including every carried chat and foreign sentinel.
# carry.observer_picker is the dedicated empty-query observation picker; carry.picker stays the actor.
# fork DIMENSION 'VALUE ...' CALLBACK [ARG...] walks depth-first, preserving shared state.
# Keys: lane/beat@os+terminal+place+engine... in fork order; callback receives VALUE first.
# Carry: JOURNEY_CARRY JSON {chats:[{name,session_id,seat_id,terminal}],foreign:{...},picker}.
# Native seatless Codex omits seat_id; tmux_launch holds actual status/terminal/births, native_rollout the verified native ID/path.
# Terminal JSON: {backend:"herdr",pane} or {backend:"tmux",socket,pane}.
# Set JOURNEY_TARGET to that JSON file before a beat; observe writes JOURNEY_OBSERVATION.
# Every beat needs a verdict; failed lanes block later beats, and the runner keeps going.
# Unexecuted blocked rows reference their failed predecessor evidence; no fresh bundle or action credit.
# PFM_BEAT_FILE is atomically updated at beat entry; long-lived processes stamp its current context.
# Assert actions from observation.activity.attributed by both action and door; diagnostics give no credit.
# Internal names start _journey_; no other function is a supported lane API.
# Supported helper CLIs (python3 "${BASH_SOURCE[0]%/*}/NAME.py" ...):
# terminal.py type TARGET TEXT | command TARGET ARG... | receipt TARGET PREFIX ARG...
# terminal.cli_command(ARGV) builds native engine-tool shell input with command-scoped PFM_DOOR=cli; typed owner input stays typed.
# terminal.py keys TARGET KEY... | read TARGET | screen TARGET; TARGET is a JSON file.
# terminal.py create [SHELL [CWD]] prints a plain PTY target; close TARGET releases it.
# herdr.py request SOCKET METHOD JSON; activity.py observed-action BUNDLE ACTION DOOR.
# activity.cursor(PATH), activity.records(PATH,CURSOR) read the rotation-safe log slice; PATH is JOURNEY_ACTIVITY.
# machine.py gap-only STATE-JSON SOURCE [SOURCE...] requires failures, all from approved sources, and empty errors.
# source mcp-stdio.sh; mcp_stdio_exchange WANT-ID BOUND-SECONDS FRAMES COMMAND ARG...
# chat.py select|need|refresh BRANCH ORIGIN; 0 found/live, 1 absent, 2 unreadable.
# chat.py picker-ready TARGET rearms an owner picker from the installed binary in a prelude, retaining action attribution.
# observation.reload_ready(BEFORE-BUNDLE,CURRENT-TERMINAL,PRIOR-CARRY) proves exactly one replacement engine birth; carried(BUNDLE,SID) and birth_presence(BIRTH,PROCESSES) share its identity rules.
# observation.process_state(PID) returns actual {pid,start,state}, native Linux /proc stat or Darwin ps, with before/after birth validation; unreadable is an error.
# chat.py reload-ready BEFORE-BUNDLE PRIOR-CARRY TARGET [CURRENT-TERMINAL-OUT] exits 0 ready, 1 pending, 2 unreadable; reads only the actual terminal before final full observation.
# chat.py ensure BRANCH ORIGIN [FIXTURE] uses public new-from-cli when absent; never fakes typed birth.
# chat.py shell BRANCH TARGET-OUT [WORKSPACE [CWD]] creates a parent shell (need prelude only).
# chat.branch_cwd(BRANCH,PROJECT='atlas') resolves the requested project/subfolder/worktree input directory.
# chat.py scenario TARGET BRANCH ORIGIN FIXTURE [PROJECT [WORKSPACE]] stages only inputs.
# chat.py before TARGET RECEIPT; capture TARGET BRANCH ORIGIN RECEIPT [PROJECT [WORKSPACE]].
# chat.py ensure-role BRANCH ROLE NAME FIXTURE [PROJECT [WORKSPACE]] creates a distinct CLI role.
# chat.py annotate BRANCH ROLE PATCH-JSON supports reload_records:boolean; selectors use role//origin.
# chat_carry.temporary_expectation(BRANCH,ROLE,EXPECT,before=None) scopes expectations; BEFORE bundle retains actual prior births.
# It restores expectations by stable key after refresh; historical birth evidence remains retained.
# chat_carry.expect(CHAT,LIFECYCLE,before=None,visibility='visible') writes owned/foreign expectations; BEFORE must contain actual engine births.
# faults.interruptible() scopes the existing SIGTERM interruption handler and restores the prior handler.
# faults.fresh_target(TARGET) scopes an unregistered run-owned native Herdr/tmux terminal, proving socket, PFM_RUN, physical identity and birth.
# It refuses carried/foreign aliases, follows the same birth across moves, rejects replacement, and revokes authority finally without carry writes; owned_target remains strict outside the scope.
# isolation.environment_d(RECEIPT) runs the native Linux systemd generator with both Claude config selectors unset; retains argv/stdout/stderr/status.
# observation.managed_unit('theme') reads native Darwin manager/kernel identity and the separately labelled on-disk plist definition.
# bootstrap.run_managed_unit('theme',ARGV,PREFIX,before=None,after=None,bound=30) requires an unloaded owned job; scope faults.unit_state(LABEL,False) to restore an existing job.
# Its before(JOB) callback runs after native launchd readiness, before public argv; the factory restores original plist bytes/mode and unloaded state.
# after(JOB,RECEIPT) runs after child completion before teardown/restoration; actual child argv has PFM_DOOR=cli.
# faults.empty_tmux(SOCKET,SESSION,RESTORE) scopes an empty auxiliary server, retaining its birth/socket and restoring its session/options.
# faults.retired_indicator(BIRTH,SOCKET,RESTORE,*,pane=None) waits for the proved owned daemon to exit; RESTORE runs finally. Wrap in interruptible().
# faults.herdr_capabilities(PREFIX), file_read_error(PATH,kind='directory-link'|'socket'), aged_files(PATHS,age_seconds=301,kind='regular'|'socket') are public scoped faults.
# faults.file_symlink(PATH,TARGET) scopes an owned native symlink/self-loop and restores the original entry; foreign/directory targets are refused.
# faults.unlistable_directory(PATH) scopes a native ELOOP self-link and restores the original directory; conflicting replacements retain both entries and fail explicitly.
# faults.stopped_for_move(TARGET,BIRTH,MOVE,RESTORE,receipt=None) stops/moves/revalidates/kills the actual birth; optional new owned receipt retains native source/prekill displays, then yields moved target and restores once.
# chat_carry.temporary_hostless(BRANCH,ROLE,BEFORE-BUNDLE,RECEIPT-PATH) scopes validated moved-Herdr observation after exact supervisor death; carry/store remain unchanged, actual engine states and strict badge equality remain observed.
# foreign_transition.py adopt restart|uninstall|install BEFORE AFTER RECEIPT [BASELINE [EVIDENCE]] validates explicit baseline transitions.
# After owner-typed pfm chat end SID: chat.py expect-ended BRANCH ROLE BEFORE-BUNDLE validates actual death and retains the ended role.
# lifecycle=transient requires actual dead/lost status+seat and expected_prior_births death proof, preserving real terminal.
# chat.py inline TARGET STEPS-FILE [PROMPT] submits live mock steps through the engine's prompt.
# chat.pick_new(TARGET,PROJECT,ENGINE,ACCOUNT,CACHE) / chat.py pick-new TARGET PROJECT ENGINE ACCOUNT CACHE opens the owner picker, selects its New chat project row, returns actual wizard observations.
# chat.stage_role(TARGET,BRANCH,ROLE,FIXTURE,PROJECT="atlas",WORKSPACE=None) / chat.py stage-role TARGET BRANCH ROLE FIXTURE [PROJECT [WORKSPACE]] stages the same CLI-origin role as ensure-role; launch then use existing capture.
# observation_process.environment includes nonsecret PFM_COST_LEDGER/PFM_COST_LEDGER_PID, OTEL_EXPORTER_OTLP_ENDPOINT and CLAUDE_CODE_ENABLE_TELEMETRY.
# chat.py shell-ready TARGET uses the existing shell_ready observer: 0 ready, 1 pending, 2 read error.
# chat.py wizard TARGET ENGINE ACCOUNT 1h|5m [OBSERVED-RECEIPT] records actual initial chips before confirming.
# chat.py pick TARGET BRANCH ORIGIN [KEY] opens from shell, clears search, verifies rendered session, keys.
# chat.py reopen TARGET BRANCH ORIGIN types public chat open; refresh after observed identity change.
# chat.py adopt-child CALLER-CARRY CHILD-SESSION MCP-RESULT [ROLE] binds actual child and parent edge; adopt-cli-child PARENT SESSION RECEIPT-PREFIX ROLE proves a public CLI child.
# chat.py adopt-branch PARENT-CARRY ORIGIN SESSION RECEIPT-PREFIX verifies actual branch lineage.
# chat.py adopt-continuation TARGET BRANCH ROLE STAGED-ORIGIN BEFORE [PROJECT [WORKSPACE]]
# replaces the stable role after predecessor exit, using observed native continuation without a parent edge.
# chat.py snapshot TARGET OUTPUT writes the complete read-only bundle; errors exit 2.
# Supported Python reads: observation.bundle(TARGET_FILE), terminal(TARGET_DICT), report(ARGV), process_snapshot().
# observation.reporter_activity(CHAT) reads session activity including integer main_stopped_at; account_stats_cache(CHAT) reads owned account caches.
# observation_paced.paced_worker(CHAT) joins actual session input with the owned paced worker birth.
# continuation.indexed(CHAT) returns actual launch_id/tail_id/path/chain from the cache, not native writer state.
# continuation.native(CHAT) returns the actual live Claude pid/sessionId/cwd record from its exact sessions/<pid>.json path.
# observation.indicator_daemon(snapshot=None) reads the owned daemon birth and kernel CPU/write counters.
# observation.process_cwd(PID) returns actual kernel cwd, checking pid/start before and after the read.
# account_stats_cache values include document (full parsed JSON) beside byte count and schema summary.
# Process environments expose allowlisted tool/CI/pager and Herdr attach markers, never credential keys.
# observation.expected_environment_denial(RECEIPT_PATH) scopes one birth-bound, native EACCES with its actual fault receipt.
# observation.read_actual_sidechains(CHAT) reads engine-owned subagent transcript identities.
# observation.read_actual_codex_session(CHAT) proves the public transcript native session_meta ID/path.
# observation.read_actual_codex_children(CHAT) reads native Codex child rollout IDs, paths and parent IDs.
# process_snapshot rows include portable ps stat and its primary state (e.g. Z for a zombie).
# observation_process.engine_births(OBSERVED_CHAT,ENGINE) recognizes native names or exact stored.recipe.binary.
# observation.terminal_births(DISPLAY) combines actual terminal/shield births; observation_process.environment(PID) is allowlisted.
# chat.seats(), chat.shell_ready(TARGET), chat.socket_path(SOCKET) are public read/setup predicates.
# chat_carry.stored(SELECT_SQL,PARAMETERS=()) is the read-only SQL door; bind exact owned session/socket parameters.
# observation summaries key stored by chat.key or session_id; native kills are stored.hidden rows.
# Runner supplies JOURNEY_OS=linux|macos; use its value in branch keys.
# Supported Python input: terminal.text(TARGET_DICT,TEXT), keys(TARGET_DICT,KEYS),
# type_command(TARGET_DICT,ARGV,receipt=None); runtime.read_json/write_json are the artifact doors.
# runtime.write_text(PATH,TEXT,mode=None) atomically writes text, preserving mode unless supplied.
# runtime.start(ARGV,**OPTIONS) is the native process door for owned NEED preludes; caller must reap it.
# projects.worktree(PROJECT,NAME) / projects.py worktree PROJECT NAME creates a real linked, run-owned auxiliary worktree.
# runtime.now_ms() is the clock door; bootstrap.wait(LABEL,PREDICATE,bound=15) is bounded polling.
# bootstrap.observer_picker(TARGET) rearms only the dedicated observer in a prelude or after mapped server restart.
# bootstrap.tmux_server(socket_name='default',session='owner') creates/reuses an owned PFM_TMUX_DIR server and returns its terminal target.
# faults.tmux_birth(SOCKET) reads its real birth; dead_tmux(SOCKET,restore) scopes an auxiliary server death and restores via callback.
# faults.refusing_socket(SOCKET) returns native {socket:canonical,errno:ECONNREFUSED} for an owned retained socket; live, absent, non-socket, replaced inode and other errors fail.
# JOURNEY_ROOT/previous-install/<account>/settings.json retains actual prior-install bytes read-only; provenance.json records version, paths and hashes.
# claude_versions.prior_version() is a scoped input prelude yielding old/successor/source_sha256.
# Publicly launch then verify the recorded old binary; faults.temporarily_missing_file(old) supplies pruning.
# Carry .chats[] keeps stable key=chat-birth:BRANCH:ORIGIN, branch=os+terminal+place+engine,
# origin, engine, place, terminal_kind, name/session_id/seat_id/terminal/project/account;
# names may change; select branch+origin, preserve all successful origins, invent no gap ids.
# Extra origins are unique action slugs (storm-N/forecast-...), preserving the four canonical origins.
# scenario binds an input UUID and PFM_TEST_FRESH_SOCKET; actual session_id always comes from the store.
# .expect lifecycle/terminal/visibility preserves ended/archived carries; reopen refuses those lifecycles.
# Bundles include process_births[].chain/environment, terminal.agent_status/indicators and stored lineage.

_JOURNEY_LIB="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
export PYTHONDONTWRITEBYTECODE=1

_journey_state() { python3 "$_JOURNEY_LIB/beatstate.py" "$@"; }

beat() {
  local slug="$1" rc
  shift
  export JOURNEY_SOURCE="${BASH_SOURCE[1]}:${BASH_LINENO[0]}"
  export PFM_BEAT="$JOURNEY_LANE/$slug${JOURNEY_BRANCH:+@$JOURNEY_BRANCH}"
  export JOURNEY_OBSERVATION="$JOURNEY_OUT/beats/$PFM_BEAT/observation.json"
  _journey_state start "$slug" "$@"; rc=$?
  return "$rc"
}

assert() { _journey_state assert "$@"; }
golden() { _journey_state golden "$@"; }
pass() { _journey_state finish pass "${1:-asserted}"; }
fail() { _journey_state finish fail "$1"; }
known() { _journey_state finish known "$1"; }
blocked() { _journey_state finish blocked "blocked-by $1"; }
skip-because() { _journey_state finish skip-because "$1"; }
spends() { _journey_state spends "$1"; }
expect-log() { _journey_state expect-log "$1"; }
observe() { _journey_state observe "${1:-${JOURNEY_TARGET:?observe requires a terminal target}}"; }

need() {
  local label="$1" check="$2" create="$3" rc
  "$check"; rc=$?
  case "$rc" in
    0) printf '%s need %s present (no-op)\n' "$JOURNEY_LANE" "$label"; return 0 ;;
    1) ;;
    *) printf '%s ERROR need %s could not inspect (exit %s)\n' "$JOURNEY_LANE" "$label" "$rc" >&2; return 2 ;;
  esac
  "$create" || { printf '%s ERROR need %s creation failed\n' "$JOURNEY_LANE" "$label" >&2; return 2; }
  "$check" || { printf '%s ERROR need %s still absent after creation\n' "$JOURNEY_LANE" "$label" >&2; return 2; }
  printf '%s need %s created\n' "$JOURNEY_LANE" "$label"
}

poll() {
  local label="$1" bound="$2" start="$SECONDS" rc
  shift 2
  case "$bound" in ''|*[!0-9]*) printf 'POLL-BOUND %s: %s\n' "$label" "$bound" >&2; return 2 ;; esac
  while :; do
    "$@"; rc=$?
    case "$rc" in
      0) return 0 ;;
      1) ;;
      *) printf 'POLL-UNREADABLE %s exit=%s\n' "$label" "$rc" >&2; return 2 ;;
    esac
    if [ "$((SECONDS - start))" -ge "$bound" ]; then
      printf 'POLL-TIMEOUT %s bound=%ss\n' "$label" "$bound" >&2
      return 1
    fi
    # POLL-STEP: only wait while the named predicate remains pending.
    sleep 0.1
  done
}

fork() {
  local dimension="$1" values="$2" callback="$3" previous="${JOURNEY_BRANCH:-}" value rc=0
  shift 3
  case "$dimension" in os|terminal|place|engine|shell-start|workspace-state|mcp-route|recipient-state|peer) ;;
    *) printf 'FORK-DIMENSION %s\n' "$dimension" >&2; return 2 ;;
  esac
  [ -n "$values" ] || { printf 'FORK-EMPTY %s\n' "$dimension" >&2; return 2; }
  for value in $values; do
    case "$value" in ''|*[!a-z0-9-]*) printf 'FORK-VALUE %s\n' "$value" >&2; return 2 ;; esac
    export JOURNEY_BRANCH="${previous:+$previous+}$value"
    "$callback" "$value" "$@" || rc=1
  done
  export JOURNEY_BRANCH="$previous"
  return "$rc"
}
