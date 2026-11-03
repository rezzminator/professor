#!/usr/bin/env python3
"""Watch exact chat identity guards fail only in private fenced copies."""
import os, shutil, sys, tempfile, unittest
from pathlib import Path
import journey_path
from runtime import command

HERE = journey_path.JOURNEY


class ChatMutations(unittest.TestCase):
    def test_private_mutations_are_caught(self):
        if os.environ.get("PFM_DEV_FENCE") != "1":
            self.fail("mutations require the fence")
        mutations = [
            ('and row["herdr_socket"] == os.environ["HERDR_SOCKET_PATH"]', '', "test_capture_is_scoped_and_preserves_every_origin"),
            ('if previous["target"] != target:', 'if False:', "test_capture_is_scoped_and_preserves_every_origin"),
            ('if len(matches) != 1:\n        raise ValueError(f"CHAT-CAPTURE-AMBIGUOUS',
             'if False:\n        raise ValueError(f"CHAT-CAPTURE-AMBIGUOUS', "test_ambiguous_capture_and_refresh_lineage"),
            ('"open", current["session_id"]', '"open", "invented"', "test_ambiguous_capture_and_refresh_lineage"),
            ('return current.get("expect", {}).get("lifecycle", "live") == "live"', 'return True', "test_ambiguous_capture_and_refresh_lineage"),
            ('"socket": metadata["launch_socket"]', '"socket": target["socket"]', "test_new_tmux_socket_is_bound_to_the_public_launch_input"),
            ('if observed_scenario(seat_terminal(row), engine, workspace)["scenario"] == metadata["scenario"]', 'if True', "test_concurrent_cobalt_launches_bind_their_own_native_process"),
            ('"project": metadata.get("project", "atlas")', '"project": "atlas"', "test_concurrent_cobalt_launches_bind_their_own_native_process"),
            ('chat.get("role", chat.get("origin")) == origin', 'chat.get("origin") == origin', "test_distinct_cli_roles_and_reload_annotations"),
            ('keys(target, ["right"])', 'keys(target, ["left"])', "test_wizard_and_missing_typed_prelude_keep_the_real_origin"),
            ('if not any(edge["val"] in identities for edge in linked):', 'if False:', "test_mcp_child_uses_the_actual_parent_edge_and_child_engine"),
            ('if session_id == parent["session_id"]:', 'if False:', "test_mcp_child_uses_the_actual_parent_edge_and_child_engine"),
            ('if len(paths) != 1:', 'if False:', "test_mcp_child_uses_the_actual_parent_edge_and_child_engine"),
            ('if edge != [{"val": parent["session_id"]}]:', 'if False:', "test_mcp_child_uses_the_actual_parent_edge_and_child_engine"),
            ('if actual != workspace.removeprefix("label:"):', 'if False:', "test_reconciled_workspace_label_binds_the_actual_destination"),
            ('updated["prior_seat_ids"] = list(dict.fromkeys([*current.get("prior_seat_ids", []), current["seat_id"]]))', 'updated["prior_seat_ids"] = []', "test_distinct_cli_roles_and_reload_annotations"),
            ('"origin": replacement.get("origin", origin)', '"origin": origin', "test_continuation_replaces_role_without_branch_parent"),
            ('if metadata.get("scenario_session_id") and read_json(metadata["scenario"]).get("session_id") != metadata["scenario_session_id"]:', 'if False:', "test_concurrent_cobalt_launches_bind_their_own_native_process"),
            ('if set(values) != {"reload_records"} or type(values["reload_records"]) is not bool:', 'if False:', "test_distinct_cli_roles_and_reload_annotations"),
            ('keys(target, ["ctrl+u"])', 'keys(target, ["ctrl+a"])', "test_picker_credits_only_the_rendered_selected_session"),
        ]
        with tempfile.TemporaryDirectory(prefix="journey-chat-mutation-") as scratch:
            tree = Path(scratch) / "journey"
            shutil.copytree(HERE, tree, ignore=shutil.ignore_patterns("__pycache__"))
            sources = {name: (tree / name).read_text() for name in ("chat.py", "chat_carry.py") }
            for old, new, test in mutations:
                with self.subTest(test=test, statement=old):
                    filenames = [name for name, value in sources.items() if old in value]
                    self.assertEqual(1, len(filenames), old)
                    path = tree / filenames[0]
                    original = sources[filenames[0]]
                    self.assertEqual(1, original.count(old))
                    path.write_text(original.replace(old, new))
                    try:
                        command([sys.executable, "-B", str(tree / "fixtures/chat_test.py"), "CarriedChat." + test], timeout=15)
                    except RuntimeError as error:
                        result = str(error)
                        self.assertIn("AssertionError", result)
                        self.assertIn("FAILED (failures=", result)
                        print("WATCHED-FAIL chat.py/" + test)
                    else:
                        self.fail("mutation passed: " + old)
                    finally:
                        path.write_text(original)


if __name__ == "__main__":
    unittest.main()
