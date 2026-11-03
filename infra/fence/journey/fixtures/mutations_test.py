#!/usr/bin/env python3
"""Watch harness guards fail in one-statement mutations of a private fence copy."""
import os
from pathlib import Path
import shutil
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from runtime import command


def main():
    if os.environ.get("PFM_DEV_FENCE") != "1":
        raise RuntimeError("mutations require the fence")
    source = Path(__file__).resolve().parents[1]
    mutations = [
        ("chat_capture.py", '"scoped_seats": scoped', '"scoped_seats": []',
         "chat_test.py", "CarriedChat.test_capture_is_scoped_and_preserves_every_origin"),
        ("observation.py", 'if lifecycle.hostless_scope(chat) is not None:', 'if False:',
         "observation_hostless_test.py", "HostlessObservation.test_full_projection_keeps_native_hostless_evidence_and_strict_badge_equality"),
        ("machine.py", 'if actual["picker"]["state"] != wanted:', 'if False:',
         "machine_test.py", "InstalledMachine.test_idle_receipt_waits_for_the_actual_picker_badge"),
        ("observation_bundle.py", 'error.projection if isinstance(error, observation.lifecycle.ProjectionMismatch) else {"error": detail}', '{"error": detail}',
         "observation_test.py", "Observation.test_bundle_keeps_independent_chat_evidence_when_picker_badges_fail"),
        ("observation_process.py", 'return {"pid": pid, "start": birth, "state": state}', 'return {"pid": pid + 1, "start": birth, "state": state}',
         "observation_process_test.py", "Observation.test_process_snapshot_preserves_birth_and_process_metrics_are_actual"),
        ("observation_transition.py", 'return len(current) == 1 and birth_presence(prior[0], {current[0]["pid"]: current[0]}) == "ended"', 'return False',
         "observation_process_test.py", "Observation.test_reload_readiness_requires_one_actual_replacement_birth"),
        ("chat_capture.py", 'return bool(row["herdr_pane"] if row["backend"] == "herdr" else row["tmux_socket"] and row["tmux_pane"])', 'return True',
         "chat_test.py", "CarriedChat.test_capture_is_scoped_and_preserves_every_origin"),
        ("bootstrap.py", "'◆ pfm ' in observation.screen(target) or shell_ready(target)", "'New chat' in observation.screen(target) or shell_ready(target)",
         "bootstrap_test.py", "TmuxServers.test_upgrade_rearms_only_the_dedicated_observer"),
        ("observation.py", "races.append(error.process)", "raise error",
         "observation_process_test.py", "Observation.test_terminal_recaptures_only_confirmed_process_exits_and_retains_evidence"),
        ("runtime.py", "started(child)", "None",
         "runtime_test.py", "JourneyRuntime.test_command_start_receipt_and_callback_failure_reap_the_child"),
        ("observation_picker.py", "return None\n    try:", "raise failure\n    try:",
         "observation_picker_test.py", "PickerObservation.test_picker_walk_binds_duplicate_names_and_restores_selection"),
        ("isolation.py", "environment.pop(key, None)", "pass",
         "harness_test.py", "JourneyHarness.test_native_environment_generator_removes_inherited_selectors"),
        ("terminal.py", "PFM_DOOR=cli", "PFM_DOOR=typed",
         "terminal_test.py", "TerminalInput.test_native_cli_input_scopes_its_door_without_changing_parent"),
        ("artifacts.py", "copied.append(name)", 'copied.append("wrong-" + name)',
         "artifacts_test.py", "RetainedArtifacts.test_native_receipts_and_carry_are_copied_with_explicit_absence"),
        ("projects.py", "return str(target)", "return str(source)",
         "projects_test.py", "ProjectWorktrees.test_auxiliary_worktree_is_linked_and_confined"),
        ("bootstrap.py", "if observer else 'pfm'", "if True else 'pfm'",
         "bootstrap_test.py", "TmuxServers.test_upgrade_rearms_only_the_dedicated_observer"),
        ("results.py", 'and total.get("exit", 0) != 0', 'and True',
         "results_test.py", "RunVerdict.test_final_lifecycle_rows_use_the_final_dispatch_budget"),
        ("bootstrap.py", 'evidence[label] = reader()', 'evidence[label] = None',
         "bootstrap_test.py", "TmuxServers.test_missing_composer_retains_native_seat_and_server_evidence"),
        ("beatstate.py", '("assertions", "goldens", "dispatches", "engine_input", "owner_input", "errors", "expected_logs")',
         '("assertions", "goldens", "engine_input", "owner_input", "errors", "expected_logs")',
         "harness_test.py", "JourneyHarness.test_blocked_before_input_retains_header_without_fresh_observation"),
        ("observation_process.py", 'return dict(scope["evidence"])', 'return {}',
         "observation_process_test.py", "Observation.test_expected_environment_denial_is_native_and_visible_to_worker_threads"),
        ("chat.py", 'arguments += ["--name", name or "need-" + metadata["scenario_session_id"]]',
         'arguments += ["--name", name] if name else []',
         "chat_test.py", "CarriedChat.test_distinct_cli_roles_and_reload_annotations"),
        ("runtime.py", 'stderr=stderr, status=child.returncode', 'stderr="", status=child.returncode',
         "runtime_test.py", "JourneyRuntime.test_console_and_artifact_receive_the_complete_large_write"),
        ("machine.py", 'return all(states)', 'return True',
         "machine_test.py", "InstalledMachine.test_install_waits_for_running_jobs_and_refuses_unreadable_state"),
        ("runtime.py", 'output.write(chunk)', 'output.write(chunk[:8192])',
         "runtime_test.py", "JourneyRuntime.test_console_and_artifact_receive_the_complete_large_write"),
        ("beatstate.py", 'not state.get("blocked_evidence") and not state["observation"]', 'not state["observation"]',
         "harness_test.py", "JourneyHarness.test_a_mismatch_cannot_pass_and_blocks_the_next_beat"),
        ("chat_carry.py", "observation.herdr.request, True)", "observation.herdr.request, False)",
         "chat_test.py", "CarriedChat.test_ended_annotation_requires_observed_death_and_preserves_the_role"),
        ("observation_process.py", '"main_stopped_at": stopped', '"main_stopped_at": 0',
         "observation_test.py", "Observation.test_reporter_activity_projects_product_owner_children_and_sequence"),
        ("runtime.py", "child.kill()", "child.kill(); child.communicate()", "runtime_test.py",
         "JourneyRuntime.test_command_timeout_with_inherited_pipe_writers"),
        ("terminal_pty.py", "environment.pop(key)",
         "pass", "terminal_test.py", "PlainTerminal.test_configured_login_shell_environment_and_kernel_terminal"),
        ("terminal_pty.py", '[shell, "-l", "-i"]',
         '[shell, "-i"]', "terminal_test.py", "PlainTerminal.test_configured_login_shell_environment_and_kernel_terminal"),
        ("terminal_pty.py", 'fcntl.ioctl(0, termios.TIOCSCTTY, 0)',
         'pass', "terminal_test.py", "PlainTerminal.test_configured_login_shell_environment_and_kernel_terminal"),
        ("terminal_pty.py", '"start": " ".join(command(["ps", "-p", str(child.pid), "-o", "lstart="]).split())',
         '"start": "wrong-birth"', "terminal_test.py", "PlainTerminal.test_configured_login_shell_environment_and_kernel_terminal"),
        ("terminal_pty.py", 'row.rstrip() for row in display.display',
         'row.rstrip().replace("atlas", "wrong-atlas") for row in display.display',
         "terminal_test.py", "PlainTerminal.test_visible_and_raw_reads_keys_and_wrong_birth"),
        ("terminal_pty.py", 'base64.b64encode(raw).decode()',
         'base64.b64encode(raw.replace(b"beacon", b"wrong")).decode()',
         "terminal_test.py", "PlainTerminal.test_visible_and_raw_reads_keys_and_wrong_birth"),
        ("terminal_pty.py", 'if any(message["target"].get(key) != target[key] for key in IDENTITY):',
         'if False:', "terminal_test.py", "PlainTerminal.test_visible_and_raw_reads_keys_and_wrong_birth"),
        ("terminal_pty.py", 'if len(payload) > 65536:',
         'if False:', "terminal_test.py", "PlainTerminal.test_visible_and_raw_reads_keys_and_wrong_birth"),
        ("terminal_pty.py", 'display.write_process_input = lambda value: write_input(master, value.encode())',
         'pass', "terminal_test.py", "PlainTerminal.test_visible_and_raw_reads_keys_and_wrong_birth"),
        ("terminal_pty.py", 'if os.getsid(pid) == child.pid:',
         'if False:', "terminal_test.py", "PlainTerminal.test_public_receipt_remains_responsive_while_owned_tmux_is_stopped"),
        ("terminal_pty.py", 'except (BrokenPipeError, ConnectionResetError, TimeoutError):',
         'except ValueError:', "terminal_test.py", "PlainTerminal.test_visible_and_raw_reads_keys_and_wrong_birth"),
        ("ssh.py", "if not 1024 <= port <= 65535 or port == int(os.environ['JOURNEY_MCP_PORT']):",
         "if False:", "ssh_test.py", "PrivateSSH.test_run_identity_and_port_guards"),
        ("ssh.py", "saved.chmod(0o600)",
         "saved.chmod(0o644)", "ssh_test.py", "PrivateSSH.test_native_key_login_has_a_pty_and_only_the_owned_environment"),
        ("ssh.py", "str(identity or endpoint['identity'])",
         "str(endpoint['identity'])", "ssh_test.py", "PrivateSSH.test_native_key_login_has_a_pty_and_only_the_owned_environment"),
        ("activity.py", "unexpected = [row for row in errors if not any(re.search(pattern, json.dumps(row)) for pattern in expected)]",
         "unexpected = []", "harness_test.py", "JourneyHarness.test_error_records_require_a_declared_match"),
        ("activity.py", "content += generations[generation].read_bytes()",
         "content = generations[generation].read_bytes()", "harness_test.py", "JourneyHarness.test_log_window_rotation_and_read_refusals"),
        ("activity.py", 'row.get("beat") == beat',
         'row.get("run") == run', "harness_test.py", "JourneyHarness.test_diagnostics_and_other_beats_do_not_credit_actions"),
        ("herdr.py", 'if response.get("id") != "journey":',
         'if response.get("id") is None:', "herdr_test.py", "HerdrTransport.test_refusal_and_truncated_response_are_errors"),
        ("beatstate.py", 'passed = expected == actual',
         'passed = True', "harness_test.py", "JourneyHarness.test_a_mismatch_cannot_pass_and_blocks_the_next_beat"),
        ("mcp_stdio.py", 'type(response.get("id")) is type(wanted) and response.get("id") == wanted',
         'response.get("id") == wanted', "mcp_stdio_test.py", "MCPTransport.test_reply_and_failure_lifetimes"),
        ("results.py", 'wall <= limit',
         'wall < limit', "results_test.py", "RunVerdict.test_mapping_and_budget"),
        ("results.py", 'if unknown_ids:',
         'if False:', "results_test.py", "RunVerdict.test_mapping_and_budget"),
        ("beatstate.py", 'if not recorded or recorded != actual:',
         'if not recorded:', "harness_test.py", "JourneyHarness.test_a_mismatch_cannot_pass_and_blocks_the_next_beat"),
        ("beatstate.py", 'if verdict == "pass" and matched:',
         'if False and matched:', "harness_test.py", "JourneyHarness.test_known_gaps_require_a_current_owner_ruling"),
        ("observation.py", 'holder["binding"].get("socket") == socket',
         'holder["binding"].get("socket") == "foreign.sock"', "observation_test.py", "Observation.test_shield_holder_is_bound_to_this_socket_and_pane"),
        ("terminal_state.py", 'projects = sorted(re.findall(r"^[│ ]*╭─ (atlas|beacon|cobalt) ─", text, re.MULTILINE))',
         'projects = []', "observation_test.py", "Observation.test_picker_requires_rendered_project_groups"),
        ("machine.py", 'if running_binary != expected_binary:',
         'if False:', "machine_test.py", "InstalledMachine.test_systemd_projection_checks_binary_and_manager_receipt"),
        ("machine.py", 'and all(row["source"] in (source, *sources) for row in failed)',
         '', "machine_test.py", "InstalledMachine.test_known_product_gap_cannot_hide_another_failed_assertion"),
        ("machine.py", 'or (" MISSING" in line and " MISSING optional " not in line)',
         '', "machine_test.py", "InstalledMachine.test_doctor_does_not_treat_default_information_as_a_warning"),
        ("runtime.py", 'temporary.chmod(mode)',
         'temporary.chmod(0o644)', "harness_test.py", "JourneyHarness.test_a_mismatch_cannot_pass_and_blocks_the_next_beat"),
        ("results.py", '"machine-lifecycle-final": 29000',
         '"machine-lifecycle-final": 108000', "results_test.py", "RunVerdict.test_final_lifecycle_rows_use_the_final_dispatch_budget"),
        ("beatstate.py", 'lane = os.environ.get("JOURNEY_STATE_LANE", os.environ["JOURNEY_LANE"])',
         'lane = os.environ["JOURNEY_LANE"]', "harness_test.py", "JourneyHarness.test_a_mismatch_cannot_pass_and_blocks_the_next_beat"),
        ("terminal.py", '"right": "Right"',
         '"right": "right"', "terminal_test.py", "TerminalInput.test_picker_keys_use_backend_key_names"),
        ("results.py", 'budget = result["paper_ms"]',
         'budget = original[lane]', "results_test.py", "RunVerdict.test_mapping_and_budget"),
    ]
    for old, new in (
        ('proof != state.get("dispatches", {}).get(str(path))', 'False'),
        ('any(proof.get(key) != value for key, value in wanted.items())', 'False'),
        ('len(argv) < 2 or argv[0] != "pfm" or argv[1] == "internal" or argv[1].startswith("-")', 'False'),
        ('if recorded != actual:', 'if False:'),
        ('if not re.fullmatch(r"(?:0|[1-9][0-9]{0,2})\\n", status) or int(status) > 255 or (completed_status is not None and status != completed_status):', 'if False:'),
        ('path.parent.relative_to(directory)', 'pass'),
        ('if artifact.is_symlink() or artifact.resolve() != artifact:', 'if False:'),
        ('owner_receipt(state, row["path"], row["actual"], row["status"], row["output_sha256"])', 'pass'),
        ('if completed_output is not None and digest != completed_output:', 'if False:'),
        (' + state.get("owner_input", [])', ''),
        ('"invocation": str(uuid.uuid4())', '"invocation": "stale-entry"'),
    ):
        mutations.append(("beatstate.py", old, new, "harness_test.py",
                          "JourneyHarness.test_owner_receipts_require_the_current_completed_dispatch"))
    mutations.append(("terminal.py", 'receipt_dispatch(target, arguments, receipt, line)\n',
                      'receipt_dispatch(target, arguments, receipt, line, sent=True)\n', "harness_test.py",
                      "JourneyHarness.test_owner_receipts_require_the_current_completed_dispatch"))
    output = Path(os.environ.get("PFM_TEST_TIMING_DIR", "/tmp")) / "journey-mutations"
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="journey-mutation-") as scratch:
        Path(scratch).chmod(0o755)  # The native permission proof runs as an unprivileged child.
        tree = Path(scratch) / "journey"
        shutil.copytree(source, tree, ignore=shutil.ignore_patterns("__pycache__"))
        for number, (module, old, new, test_file, test) in enumerate(mutations, 1):
            path = tree / module
            original = path.read_text()
            if original.count(old) != 1:
                raise RuntimeError(f"MUTATION-SOURCE-DRIFT: {module}: {old}")
            path.write_text(original.replace(old, new))
            try:
                try:
                    result = command([sys.executable, str(tree / "fixtures" / test_file), test], timeout=20,
                                     env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"})
                except RuntimeError as error:
                    result = str(error)
                log = output / (str(number) + "-" + test + ".log")
                log.write_text(result)
                if "AssertionError" not in result or "FAILED (failures=" not in result:
                    raise RuntimeError(f"MUTATION-NOT-CAUGHT: {module}/{test}; {log}\n{result}")
                print(f"WATCHED-FAIL {module}/{test} PASS log={log}")
            finally:
                path.write_text(original)


class HarnessMutations(unittest.TestCase):
    def test_guards_refuse_the_planted_defects(self):
        main()


if __name__ == "__main__":
    unittest.main()
