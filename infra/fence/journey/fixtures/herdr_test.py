#!/usr/bin/env python3
"""Transport cases against a real private Unix socket; no engine is replaced."""
import importlib.util
import json
import os
from pathlib import Path
import socket
import tempfile
import threading
import unittest

SOURCE = Path(os.environ.get("JOURNEY_HERDR_MODULE", Path(__file__).resolve().parent.parent / "herdr.py"))
SPEC = importlib.util.spec_from_file_location("journey_herdr", SOURCE)
HERDR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(HERDR)


class HerdrTransport(unittest.TestCase):
    def exchange(self, response, expected_request):
        with tempfile.TemporaryDirectory(prefix="journey-herdr-") as directory:
            path = str(Path(directory) / "herdr.sock")
            failures = []
            with socket.socket(socket.AF_UNIX) as server:
                server.bind(path)
                server.listen(1)
                server.settimeout(3)

                def answer():
                    try:
                        with server.accept()[0] as connection:
                            connection.settimeout(3)
                            frame = bytearray()
                            while not frame.endswith(b"\n"):
                                part = connection.recv(65536)
                                if not part:
                                    raise AssertionError("request ended before newline")
                                frame.extend(part)
                            actual = json.loads(frame)
                            self.assertEqual(expected_request, actual, path)
                            connection.sendall(response)
                    except Exception as error:
                        failures.append(error)

                worker = threading.Thread(target=answer)
                worker.start()
                try:
                    return HERDR.request(path, "pane.get", {"pane_id": "sentinel-pane"})
                finally:
                    worker.join(4)
                    if worker.is_alive():
                        raise AssertionError(f"transport worker did not end: {path}")
                    if failures:
                        raise failures[0]

    def test_request_reads_the_complete_envelope(self):
        expected = {"id": "journey", "method": "pane.get", "params": {"pane_id": "sentinel-pane"}}
        response = {"id": "journey", "result": {"pane": {"pane_id": "sentinel-pane", "text": "READY"}}}
        actual = self.exchange((json.dumps(response) + "\n").encode(), expected)
        self.assertEqual(response, actual)

    def test_refusal_and_truncated_response_are_errors(self):
        expected = {"id": "journey", "method": "pane.get", "params": {"pane_id": "sentinel-pane"}}
        for response, message, code in [
            (b'{"id":"journey","error":{"code":"pane_not_found"}}\n', "pane.get: refused:", "pane_not_found"),
            (b'{"id":"journey","ok":false,"error":null}\n', "pane.get: refused:", None),
            (b'{"id":"journey","result":{}', "pane.get: response ended before newline:", None),
            (b'{"id":"journey","ok":true}\n', "pane.get: unrecognized response envelope", None),
            (b'{"id":"foreign","result":{}}\n', "pane.get: mismatched response id:", None),
        ]:
            with self.subTest(response=response):
                with self.assertRaisesRegex(ValueError, message) as failure:
                    self.exchange(response, expected)
                self.assertEqual(code, getattr(failure.exception, "code", None))
                if message == "pane.get: refused:":
                    self.assertIsInstance(failure.exception, HERDR.Refused)
                    self.assertEqual("pane.get: refused: " + str(json.loads(response)), str(failure.exception))


if __name__ == "__main__":
    unittest.main()
