#!/usr/bin/env python3
"""MCP transport lifetime: reply before stdin EOF, bounded teardown, exact id."""
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from runtime import command


class MCPTransport(unittest.TestCase):
    def test_reply_and_failure_lifetimes(self):
        adapter = Path(__file__).resolve().parents[1] / "mcp-stdio.sh"
        cases = (
            ("reply-before-eof", 'print(\'{"id":1,"result":{}}\', flush=True); sys.stdin.read()', "1", "0\n\neof\n"),
            ("wrong-id", 'print(\'{"id":true,"result":{}}\', flush=True)', "1", "1\nexited before answering\neof\n"),
            ("early-eof", "pass", "1", "1\nexited before answering\neof\n"),
            ("deadline", "sys.stdin.read()", "0.1", "2\nno answer in 0.1s\nterm\n"),
            ("invalid-frame", 'print("[]", flush=True); sys.stdin.read()', "1", "3\nMCP-STDIO-REPLY: expected a JSON object\nterm\n"),
        )
        with tempfile.TemporaryDirectory() as scratch:
            directory = Path(scratch)
            server = directory / "server.py"
            shell = directory / "exchange.sh"
            shell.write_text('''source "$1"
export TMPDIR="$2" MCP_STDIO_GRACE_SECS=0.2
mcp_stdio_exchange 1 "$3" '{"id":1}' python3 "$4" >"$2/frames"
printf '%s\\n%s\\n%s\\n' "$?" "$MCP_STDIO_WHY" "$MCP_STDIO_END"
''')
            for name, behavior, bound, expected in cases:
                with self.subTest(name=name):
                    server.write_text("import sys\nsys.stdin.readline()\n" + behavior + "\n")
                    actual = command(["bash", str(shell), str(adapter), scratch, bound, str(server)])
                    self.assertEqual(expected, actual)
            self.assertEqual([], list(directory.glob("journey-mcp.*")))


if __name__ == "__main__":
    unittest.main()
