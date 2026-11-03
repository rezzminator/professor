#!/usr/bin/env python3
"""The denied caller refuses registered transports and restores its fault."""
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import native_denied as native
from runtime import write_json


class NativeDeniedTests(unittest.TestCase):
    def test_denied_native_caller_refuses_registered_transport_before_launch(self):
        with tempfile.TemporaryDirectory() as folder:
            owner = Path(folder) / "owner.json"
            write_json(owner, {"backend": "herdr", "pane": "owned"})
            with patch.object(native, "type_command") as dispatch:
                with self.assertRaisesRegex(ValueError, "^NATIVE-CODEX-OUTSIDE:"):
                    native.run(owner, {}, "prefix", ["pfm", "whoami", "--json"], {}, None, None)
                dispatch.assert_not_called()
            write_json(owner, {"backend": "pty", "pane": "actual", "socket": folder + "/socket"})
            settings = Path(folder) / "codex"
            settings.mkdir()
            (settings / "hooks.json").write_text('{"hooks": {}}')
            resources = {"scenario": "scenario", "records": "records", "cwd": folder, "codex_home": str(settings)}
            with patch.object(native.faults, "owned_path"), patch.object(native, "type_command") as dispatch:
                with self.assertRaisesRegex(ValueError, "^NATIVE-CODEX-REGISTERED:"):
                    native.run(owner, resources, "prefix", ["pfm", "whoami", "--json"], {}, None, None)
                dispatch.assert_not_called()


if __name__ == "__main__":
    unittest.main()
