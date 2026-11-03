#!/usr/bin/env python3
"""Native conditions refuse borrowed identities and restore observed placements."""
from contextlib import contextmanager, redirect_stderr, redirect_stdout
import io
import copy
import json
from pathlib import Path
import runpy
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import native_forecasts as native
SOURCE = Path(__file__).with_name("native_forecasts.py")
from runtime import read_json, write_json


class NativeForecastTests(unittest.TestCase):
    def test_shield_rename_metadata_still_requires_the_real_bound_frontend(self):
        chat = {"terminal": {"pane": "owned"}}
        good = {"shield": {"holder": {"holder_live": True, "binding": {"pane": "owned"}}},
                "births": [{"chain": [{"argv": "/owned/herdr-shield"}]}]}
        cases = [(None, None, True, []), ("births", [{"chain": [{"argv": "/bin/zsh -l"}]}], True, ["native-foreground-attach-client"]),
                 ("shield", {"holder": {"holder_live": False, "binding": {"pane": "owned"}}}, True, ["native-shield-live"]),
                 ("shield", {"holder": {"holder_live": True, "binding": {"pane": "foreign"}}}, True, ["native-shield-bound-pane"]),
                 (None, None, False, ["native-wrapper-alias"])]
        for field, value, alias, expected in cases:
            with self.subTest(expected=expected):
                actual = copy.deepcopy(good)
                if field:
                    actual[field] = value
                self.assertEqual(expected, [row["field"] for row in native.shield_checks(chat, actual, alias) if row["expected"] != row["actual"]])

    def chat(self):
        return {"key": "owned", "name": "actor", "engine": "claude", "branch": "linux+herdr-shield+project+claude",
                "origin": "new-from-cli", "role": "native", "session_id": "owned-session",
                "terminal": {"backend": "herdr", "pane": "source-pane"}}

    def test_plans_require_exact_native_factors(self):
        cases = [
            ("herdr_indicators=on,engine=opencode", ("indicators-opencode", "list")),
            ("pane_state=unknown,multiplexer=herdr", ("unknown", "list")),
            ("herdr_indicators=on,pane_state=idle", ("indicators-idle", "list")),
            ("seat_event=pane_moved,door=whoami", ("moved", "identity")),
            ("env_pane=birth_id_inherited,herdr_event=pane_moved_workspace,seat_supervisor=ancestor_in_chain", ("moved-inherited", "identity")),
            ("process_table=kern_procargs2,caller=resolve_lookup,os=macos", ("darwin-lookup", "identity")),
            ("process_state=stopped,tmux_server=job_control_stopped,caller_context=outside_pane", ("outside-stopped", "outside")),
            ("seat_lookup=not_in_chat,process_environ_read=permission_denied,engine=codex", ("outside-denied-codex", "denied")),
            ("engine=claude_code,account_source=no_launch_record_no_live_seat,transcript_state=absent_before_first_prompt", ("outside-before-prompt", "list")),
            ("hook=claude_usage_hook,invoker=claude_code,config_state=invalid", ("outside-usage-hook", "native-list")),
            ("exit_empty=off,socket_holder=live_process,tmux_server=running_no_session", ("empty-server", "list")),
            ("pane_wrapper=herdr_keep,engine=claude_code,herdr_detection=attach_client_foreground", ("shield-foreground", "list")),
            ("herdr_pane=herdr_keep_protected,herdr_keep_listing=per_socket_query,foreground_process=herdr_keep_attach_client", ("shield-socket", "list")),
            ("os=linux,login_env_source=environment_d,claude_origin=outside_pfm", ("outside-login-default", "list")),
            ("supervisor_pid=dead_or_reused,herdr_pane=recycled_or_moved,seat_row=stale", ("stale-seat-moved", "list")),
            ("supervisor_state=absent,scan_mode=writable,herdr_pane=pane_not_found", ("stale-seat-missing-pane", "list")),
            ("backend=tmux,theme_prior=failed_read_as_unset,tmux_answer=invalid_option_unset", ("unset-theme", "tmux-theme")),
            ("shell_exit=nested_zsh_ctrl_d,herdr=drops_agent_report_on_bare_shell,shell_state=bare_shell_foreground", ("nested-shell-release", "shell-transition")),
            ("pane_owner=parent_shell_done,herdr_title=unsequenced_metadata,shell_nesting=nested_zsh_exit", ("nested-parent-title", "shell-transition")),
            ("herdr_pane=replaced_by_new_client,pane_state=moved,client_registry=stale_tmux_client", ("moved-replaced-client", "shell-client")),
            ("caller_identity=bare_seat_id,seat_name=unnamed,lookup=chat_mcp_transcript_id", ("unnamed-transcript", "unnamed-mcp")),
            ("pane_fronted=false,pane_env=inherited_stale,backend=tmux_shell", ("stale-tmux-shell", "shell-birth")),
            ("tab_label=user_hand_set,backend=herdr_with_tmux,chat_launch=in_place_exec", ("user-tab-exec", "shell-birth")),
            ("settings_json=unreadable,engine=claude_code,telemetry_env=user_otlp_set", ("launch-unreadable-settings", "launch-reader")),
            ("tmux_server=sigstopped,caller=claude_launch_outside_pane", ("outside-launcher-version", "launcher")),
            ("chat_row=resumable_or_bg,tmux_socket=gone_or_refusing,picker_state=parked", ("quiet-picker-dead-server", "presence-reader")),
            ("account_discovery=settings_unlistable,os=macos,service_manager=launchd_watch_paths", ("macos-watch-unlistable", "macos-theme")),
            ("launchd_watch=reduced_set,account_discovery=read_failed,os=macos", ("macos-watch-failure", "macos-theme")),
            ("os=macos,service_manager=launchd_watch_paths,settings_layout=symlinked_settings_json", ("macos-watch-symlink", "macos-theme")),
            ("service_manager=launchd,launchd_job=follower_own_job,os=macos", ("macos-own-job", "macos-theme")),
            ("job_context=follower_inside_own_job,service_manager=launchd,os=macos", ("macos-own-job", "macos-theme")),
            ("recipe_origin=mcp_new_chat,os=macos,launcher=launchd_daemon", ("macos-mcp-recipe", "macos-mcp")),
            ("pane_env=herdr_pane_terminal,recipe_env=no_terminal_captured,launcher=launchd_daemon", ("macos-mcp-terminal", "macos-mcp")),
        ]
        for value, expected in cases:
            with self.subTest(value=value):
                self.assertEqual(expected, native.plan(value))
        for value in ("herdr_indicators=off,engine=opencode", "seat_event=pane_reused,door=whoami", "os=macos"):
            with self.subTest(value=value), self.assertRaisesRegex(ValueError, "^NATIVE-PLAN-UNDEFINED:"):
                native.plan(value)
        self.assertEqual({"plan": "moved", "entry": "identity", "resources": ["id", "placeholder", "destination_workspace"],
                          "engine": "claude", "argv": ["pfm", "whoami", "--json"]},
                         native.describe("seat_event=pane_moved,door=whoami"))

    def test_plan_cli_distinguishes_unsupported_from_unreadable(self):
        cases = [("os=linux", 1, 'NATIVE-PLAN-UNDEFINED: {"os": "linux"}'),
                 ("os=linux,os=macos", 2, "NATIVE-FORECAST-ERROR: FORECAST-FACTORS: 'os=linux,os=macos'"),
                 ('"os=linux', 2, "NATIVE-FORECAST-ERROR: unexpected end of data")]
        for value, code, diagnostic in cases:
            with self.subTest(value=value), patch.object(sys, "argv", [str(str(SOURCE)), "supports", value]), \
                    redirect_stdout(io.StringIO()) as output, redirect_stderr(io.StringIO()) as errors:
                with self.assertRaises(SystemExit) as stopped:
                    runpy.run_path(str(SOURCE), run_name="__main__")
                self.assertEqual(code, stopped.exception.code)
                self.assertEqual("", output.getvalue())
                self.assertEqual(diagnostic, errors.getvalue().strip())


    def test_owned_chat_requires_exact_key_session_engine_and_terminal(self):
        chat = self.chat()
        with patch.object(native, "carry", return_value={"chats": [chat]}), \
                patch.object(native.faults, "owned_target", return_value={"identity": "native"}) as target:
            self.assertEqual({"identity": "native"}, native.owned_chat(chat))
            target.assert_called_once_with(chat["terminal"])
            for field, value in (("session_id", "foreign"), ("engine", "codex"), ("terminal", {"backend": "herdr", "pane": "peer"})):
                with self.subTest(field=field), self.assertRaisesRegex(ValueError, "^NATIVE-CHAT-IDENTITY:"):
                    native.owned_chat({**chat, field: value})

    def test_failed_precondition_retains_exact_evidence_and_stops(self):
        with tempfile.TemporaryDirectory() as folder:
            prefix = str(Path(folder) / "native")
            evidence = {"checks": []}
            native.require(prefix, evidence, [native.check("engine", "opencode", "opencode")])
            with self.assertRaisesRegex(ValueError, "^NATIVE-PRECONDITION:.*expected.*actual"):
                native.require(prefix, evidence, [native.check("pane-state", "unknown", "idle")])
            self.assertEqual([{"field": "engine", "expected": "opencode", "actual": "opencode"},
                              {"field": "pane-state", "expected": "unknown", "actual": "idle"}], read_json(prefix + ".checks.json"))

    def mover(self):
        chat = self.chat()
        before = {"identity": {"workspace": "source-workspace", "tab": "source-tab", "pane": "source-pane"},
                  "pane": {"terminal_id": "physical-terminal"}}
        current = {"pane_id": "source-pane", "workspace_id": "source-workspace", "tab_id": "source-tab", "terminal_id": "physical-terminal"}
        calls = []
        def request(socket, method, value):
            calls.append((method, value))
            if method == "pane.get":
                return {"result": {"pane": {"pane_id": "placeholder", "tab_id": "source-tab"}}}
            if method == "pane.list":
                return {"result": {"panes": [dict(current), {"pane_id": "foreign", "terminal_id": "foreign-terminal"}]}}
            if method == "pane.move":
                destination = value["destination"]
                if destination["type"] == "new_tab":
                    current.update(pane_id="actual-moved-pane", workspace_id="destination", tab_id="actual-new-tab")
                else:
                    current.update(pane_id="actual-restored-pane", workspace_id="source-workspace", tab_id="source-tab")
                return {"result": {"pane_id": current["pane_id"]}}
            raise AssertionError(method)
        return chat, before, current, calls, request

    def test_move_uses_actual_physical_terminal_and_preserved_source_tab(self):
        chat, before, current, calls, request = self.mover()
        with patch.object(native, "owned_chat", return_value=before), patch.object(native, "request", side_effect=request), \
                patch.object(native, "carry", return_value={"workspaces": {"source": {"id": "source-workspace"}, "other": {"id": "destination"}}}), \
                patch.dict(native.os.environ, {"HERDR_SOCKET_PATH": "owned-socket"}):
            mover = native.PaneMove(chat, {"placeholder": "placeholder", "destination_workspace": "destination"})
            self.assertEqual({"backend": "herdr", "pane": "actual-moved-pane"}, mover.move({"pane": "stale-alias"}, "destination"))
            self.assertEqual({"backend": "herdr", "pane": "actual-restored-pane"}, mover.move({"pane": "another-stale-alias"}, "source-workspace"))
        self.assertEqual([
            ("pane.move", {"pane_id": "source-pane", "destination": {"type": "new_tab", "workspace_id": "destination"}, "focus": False}),
            ("pane.move", {"pane_id": "actual-moved-pane", "destination": {"type": "tab", "tab_id": "source-tab", "target_pane_id": "placeholder", "split": "right"}, "focus": False}),
        ], [row for row in calls if row[0] == "pane.move"])

    def test_move_refuses_same_workspace_or_subject_as_placeholder(self):
        chat, before, current, calls, request = self.mover()
        with patch.object(native, "owned_chat", return_value=before), patch.object(native, "request", side_effect=request), \
                patch.object(native, "carry", return_value={"workspaces": {"source": {"id": "source-workspace"}, "other": {"id": "destination"}}}), \
                patch.dict(native.os.environ, {"HERDR_SOCKET_PATH": "owned-socket"}):
            for resources, error in (({"placeholder": "placeholder", "destination_workspace": "source-workspace"}, "WORKSPACE"),
                                     ({"placeholder": "source-pane", "destination_workspace": "destination"}, "PLACEHOLDER")):
                with self.subTest(resources=resources), self.assertRaisesRegex(ValueError, "^NATIVE-MOVE-" + error):
                    native.PaneMove(chat, resources)
        self.assertEqual([], [row for row in calls if row[0] == "pane.move"])

    def test_explicit_subject_uses_the_same_move_door_without_borrowing_a_chat(self):
        chat, before, current, calls, request = self.mover()
        resources = {"placeholder": "placeholder", "destination_workspace": "destination"}
        with patch.object(native, "owned_chat") as registered, patch.object(native.faults, "owned_target", return_value=before) as owned, \
                patch.object(native, "request", side_effect=request), patch.object(native, "carry", return_value={"workspaces": {"other": {"id": "destination"}}}), \
                patch.dict(native.os.environ, {"HERDR_SOCKET_PATH": "owned-socket"}):
            mover = native.PaneMove(None, resources, subject=chat["terminal"])
            self.assertEqual({"backend": "herdr", "pane": "actual-moved-pane"}, mover.move(None, "destination"))
            owned.assert_called_once_with(chat["terminal"])
            registered.assert_not_called()
            with self.assertRaisesRegex(ValueError, "^NATIVE-MOVE-REBIND:"):
                mover.rebind(chat)
            for registered_chat, subject in ((None, None), (chat, chat["terminal"])):
                with self.subTest(subject=subject), self.assertRaisesRegex(ValueError, "^NATIVE-MOVE-SUBJECT:"):
                    native.PaneMove(registered_chat, resources, subject=subject)

    def test_move_rebind_requires_same_carried_identity_and_uses_the_actual_replacement(self):
        chat, before, current, calls, request = self.mover()
        with patch.object(native, "owned_chat", return_value=before) as owned, patch.object(native, "request", side_effect=request), \
                patch.object(native, "carry", return_value={"workspaces": {"source": {"id": "source-workspace"}, "other": {"id": "destination"}}}), \
                patch.dict(native.os.environ, {"HERDR_SOCKET_PATH": "owned-socket"}):
            mover = native.PaneMove(chat, {"placeholder": "placeholder", "destination_workspace": "destination"})
            replacement = {**chat, "terminal": {"backend": "herdr", "pane": "actual-reopened-pane"}}
            owned.return_value = {"pane": {"terminal_id": "actual-new-physical"}}
            mover.rebind(replacement)
            self.assertEqual((replacement, "actual-new-physical", before), (mover.chat, mover.terminal_id, mover.before))
            for field in ("key", "session_id", "engine"):
                with self.subTest(field=field), self.assertRaisesRegex(ValueError, "^NATIVE-MOVE-REBIND:"):
                    mover.rebind({**replacement, field: "foreign"})

    def test_engine_entry_refuses_other_argv_before_dispatch(self):
        with patch.object(native, "command") as dispatch:
            for arguments, capability in ((["pfm", "internal", "stale"], "chat.whoami-cli"),
                                          (["pfm", "whoami", "--json"], "")):
                with self.subTest(arguments=arguments), self.assertRaisesRegex(ValueError, "^NATIVE-ENGINE-"):
                    native.engine_action("chat.json", arguments, "prefix", capability)
            dispatch.assert_not_called()

    def test_agent_restore_requires_its_actual_identity_and_idle_report(self):
        chat = self.chat()
        output = {"id": "owned-session", "backend": "herdr", "pane": "source-pane", "engine": "cc"}
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "chat.json"
            write_json(path, chat)
            with patch.object(native, "engine_action", return_value={"exit_code": 0, "output": json.dumps(output)}), \
                    patch.object(native, "terminal", return_value={"agent_status": {"pane": "idle"}}) as display:
                native.restore_agent(path, "prefix", "chat.ls-cli")
                display.assert_called_once_with(chat["terminal"])
            with patch.object(native, "engine_action", return_value={"exit_code": 0, "output": json.dumps({**output, "id": "peer"})}), \
                    patch.object(native, "terminal") as display:
                with self.assertRaisesRegex(ValueError, "^NATIVE-AGENT-RESTORE-IDENTITY:"):
                    native.restore_agent(path, "prefix", "chat.ls-cli")
                display.assert_not_called()

    def test_refreshed_waits_for_the_actual_session_pane_and_workspace_commit(self):
        chat = self.chat()
        actual = {**chat, "terminal": {"backend": "herdr", "pane": "actual-moved"}, "workspace": "destination"}
        for stale in (None, {**actual, "session_id": "peer"}, chat, {**actual, "workspace": "old-workspace"}):
            with self.subTest(stale=stale), patch.object(native, "refresh", side_effect=[stale, actual]) as refreshed:
                self.assertEqual(actual, native.refreshed(chat, {"backend": "herdr", "pane": "actual-moved"}, "destination"))
                self.assertEqual(2, refreshed.call_count)

    def test_move_rebinds_restored_carry_even_when_the_public_entry_fails(self):
        chat = self.chat()
        moved = {**chat, "workspace": "destination", "terminal": {"backend": "herdr", "pane": "actual-moved-pane"}}
        restored = {**chat, "workspace": "source-workspace", "terminal": {"backend": "herdr", "pane": "actual-restored-pane"}}
        before = {"identity": {"workspace": "source-workspace", "tab": "source-tab"}}
        mover = SimpleNamespace(destination="destination", terminal_id="physical", before=before, move=None,
                                current=lambda: {"pane_id": "actual-restored-pane"})
        events = []
        @contextmanager
        def moved_scope(*arguments):
            try:
                yield moved["terminal"]
            finally:
                events.append("native-move-restored")
        def current_terminal(target):
            changed = target["pane"] == "actual-moved-pane"
            return {"identity": {"workspace": "destination" if changed else "source-workspace", "tab": "new-tab" if changed else "source-tab"},
                    "pane": {"terminal_id": "physical"}}
        with tempfile.TemporaryDirectory() as folder:
            path, prefix = Path(folder) / "chat.json", Path(folder) / "proof"
            write_json(path, chat)
            with patch.object(native, "owned_chat", return_value=before), patch.object(native, "PaneMove", return_value=mover), \
                    patch.object(native.faults, "moved_pane", side_effect=moved_scope), \
                    patch.object(native, "refresh", side_effect=[{**moved, "session_id": "peer"}, chat,
                        {**moved, "workspace": "source-workspace"}, moved, moved,
                        {**restored, "workspace": "destination"}, restored]) as refresh, \
                    patch.object(native, "engine_births", return_value=[{"pid": 32, "start": "native-engine"}]), \
                    patch.object(native, "terminal_births", return_value=[]), \
                    patch.object(native, "terminal", side_effect=current_terminal):
                with self.assertRaisesRegex(RuntimeError, "^public entry failed$"):
                    with native.condition("moved", {}, path, {}, {}, prefix,
                            {"checks": [], "before": {"chats": [{"name": "actor", "status": {"session_id": "owned-session"}, "stored": {}}]}}) as active:
                        self.assertEqual(moved, active)
                        raise RuntimeError("public entry failed")
                self.assertEqual(7, refresh.call_count)
            self.assertEqual(restored, read_json(path))
            self.assertEqual(restored["terminal"], read_json(str(path) + ".target"))
        self.assertEqual(["native-move-restored"], events)


if __name__ == "__main__":
    unittest.main()
