#!/usr/bin/env python3
"""Check normalization plans, merges and failures with literal expectations."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).parent


def main():
    with tempfile.TemporaryDirectory(prefix='norm-') as directory:
        out = Path(directory)
        (out / 'labels').mkdir()
        rows = [{'interaction': True, 'factors': {'backend': 'herdr_native', 'mux': 'native_herdr', 'os': 'macos'}},
                {'interaction': True, 'factors': {'os': 'linux', 'mux': 'native_herdr'}},
                {'interaction': False, 'factors': {'ignored': 'value'}}]
        (out / 'labels/1.json').write_text(json.dumps(rows))
        def run(*args):
            return subprocess.run([sys.executable, str(ROOT / 'if_norm.py'), '--out', str(out), *args], capture_output=True, text=True)
        result = run('--plan', '--batch', '3')
        wanted = f'PAIRS 4 batches=2\nPLAN NORM 1 {out}/pairs/1.txt\nPLAN NORM 2 {out}/pairs/2.txt\n'
        assert (result.returncode, result.stdout, result.stderr) == (0, wanted, ''), (out, wanted, result)
        actual = {p.name: p.read_text() for p in (out / 'pairs').glob('*.txt')}
        assert actual == {'1.txt': 'backend=herdr_native 1\nmux=native_herdr 2\n', '2.txt': 'os=linux 1\nos=macos 1\n'}, (out, actual)
        print('PASS exact pair counts, name-preserving batch boundary and PLAN lines')
        result = run('--apply')
        assert (result.returncode, result.stdout, result.stderr) == (0, 'NORM names=0 values=0 rejected=0 conflicts=0\n', ''), result
        assert json.loads((out / 'norm.json').read_text()) == {'names': {}, 'values': {}}, out
        print('PASS missing normalizers produce empty maps and zeros')
        (out / 'norm').mkdir(exist_ok=True)
        (out / 'norm/2.json').write_text(json.dumps({'names': {'mux': 'backend', 'missing': 'axis'}, 'values': {'mux=native_herdr': 'herdr_native', 'os=macos': 'Bad Value'}}))
        (out / 'norm/10.json').write_text(json.dumps({'names': {'mux': 'multiplexer'}, 'values': {'mux=native_herdr': 'native', 'os=missing': 'linux'}}))
        result = run('--apply')
        assert (result.returncode, result.stdout, result.stderr) == (0, 'NORM names=1 values=1 rejected=3 conflicts=2\n', ''), result
        assert json.loads((out / 'norm.json').read_text()) == {'names': {'mux': 'backend'}, 'values': {'mux=native_herdr': 'herdr_native'}}, out
        print('PASS exact maps, rejected keys/canonicals and numeric batch conflict precedence')
        result = run('--plan', '--batch', '1')
        assert result.stdout == f'PAIRS 4 batches=4\nPLAN NORM 1 {out}/pairs/1.txt\nPLAN NORM 2 {out}/pairs/2.txt\nPLAN NORM 3 {out}/pairs/3.txt\nPLAN NORM 4 {out}/pairs/4.txt\n', result
        assert {p.name: p.read_text() for p in (out / 'pairs').glob('*.txt')} == {'1.txt': 'backend=herdr_native 1\n', '2.txt': 'mux=native_herdr 2\n', '3.txt': 'os=linux 1\n', '4.txt': 'os=macos 1\n'}, out
        result = run('--plan', '--batch', '300')
        assert result.stdout == f'PAIRS 4 batches=1\nPLAN NORM 1 {out}/pairs/1.txt\n', result
        assert {p.name: p.read_text() for p in (out / 'pairs').glob('*.txt')} == {'1.txt': 'backend=herdr_native 1\nmux=native_herdr 2\nos=linux 1\nos=macos 1\n'}, out
        print('PASS oversized names split and replanning removes obsolete batches')
        (out / 'labels/1.json').write_text('{}')
        result = run('--plan')
        assert (result.returncode, result.stdout, result.stderr) == (1, '', f'NORM FAILED: {out}/labels/1.json: expected an array\n'), result
        print('PASS unreadable label contract')
    print('PASS all norm self-tests')


class NormTests(unittest.TestCase):
    def test_main(self):
        main()


if __name__ == '__main__':
    unittest.main()
