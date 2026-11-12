#!/usr/bin/env python3
"""Check merge coverage, rejected rows and exact saved evidence."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

MERGE = Path(__file__).with_name('if_merge.py')


def test_searched():
    with tempfile.TemporaryDirectory(prefix='searched-') as directory:
        out = Path(directory)
        (out / 'trace').mkdir()
        failures = []
        for extra, held, rejection in [({'searched': ['x_test.go']}, 'none', None),
                                       ({}, 'none', 'none without searched'),
                                       ({'searched': []}, 'none', 'none without searched'),
                                       ({'searched': 'x_test.go'}, 'fake', 'searched must be an array of strings'),
                                       ({'searched': [7]}, 'real', 'searched must be an array of strings')]:
            cell = dict(id='C01', held='untraced', held_by=[])
            row = dict(id='C01', held=held, held_by=[], meet='x.go:1', **extra)
            (out / 'cells.json').write_text(json.dumps({'cells': [cell]}))
            (out / 'trace/1.json').write_text(json.dumps([row]))
            result = subprocess.run([sys.executable, str(MERGE), '--out', str(out)], capture_output=True, text=True)
            expected_lines = (['MERGED traced=0 untraced=1 rejected=1',
                               f'REJECTED 1.json row=1 id=C01: {rejection}', 'UNTRACED C01']
                              if rejection else ['MERGED traced=1 untraced=0 rejected=0'])
            expected = {'cells': [cell if rejection else row]}
            actual = json.loads((out / 'cells.json').read_text())
            if (result.returncode, result.stdout.splitlines(), result.stderr, actual) != (0, expected_lines, '', expected):
                failures.append((out, row, 'expected', expected_lines, expected, 'actual', result, actual))
        assert failures == [], failures
    print('PASS searched copied; none without searched and malformed searched rejected')


def main():
    with tempfile.TemporaryDirectory(prefix='merge-') as directory:
        out = Path(directory)
        (out / 'trace').mkdir()
        cells = [dict(id=f'D0{i}', door=f'p/{i}.py:1', held='none', held_by=[]) for i in range(1, 4)]
        (out / 'cells.json').write_text(json.dumps({'cells': cells}))
        rows = [dict(id='D01', held='real', held_by=['test_live.py::meeting'], meet='p/1.py:20', searched=['test_live.py']),
                dict(id='D02', held='fake', held_by=['test_unit.py::meeting'], meet='p/2.py:30'),
                dict(id='D99', held='none', held_by=[], meet='p/99.py:1'),
                dict(id='D03', held='maybe', held_by=[], meet='p/3.py:1'),
                dict(id='D01', held='none', held_by=[], meet='p/3.py:1')]
        (out / 'trace/1.json').write_text(json.dumps(rows))
        result = subprocess.run([sys.executable, str(MERGE), '--out', str(out)], capture_output=True, text=True)
        wanted = ['MERGED traced=2 untraced=1 rejected=3',
                  'REJECTED 1.json row=3 id=D99: unknown id',
                  'REJECTED 1.json row=4 id=D03: invalid held: maybe', 'REJECTED 1.json row=5 id=D01: duplicate id', 'UNTRACED D03']
        assert (result.returncode, result.stdout.splitlines(), result.stderr) == (0, wanted, ''), (out, wanted, result)
        expected = {'cells': [{**cells[0], **rows[0]}, {**cells[1], **rows[1]}, {**cells[2], 'held': 'untraced', 'held_by': []}]}
        actual = json.loads((out / 'cells.json').read_text())
        failures = [] if actual == expected else [('merge', out / 'cells.json', expected, actual)]
        (out / 'trace/1.json').write_text('[]')
        result = subprocess.run([sys.executable, str(MERGE), '--out', str(out)], capture_output=True, text=True)
        assert result.stdout.splitlines() == ['MERGED traced=0 untraced=3 rejected=0',
                                               'UNTRACED D01', 'UNTRACED D02', 'UNTRACED D03'], result
        actual = json.loads((out / 'cells.json').read_text())
        expected = {'cells': [dict(cell, held='untraced', held_by=[]) for cell in cells]}
        if actual != expected:
            failures.append(('rerun', out / 'cells.json', expected, actual))
        assert failures == [], failures
        print('PASS exact merge; unknown id, invalid held and duplicate rejected')
        print('PASS rerun resets uncovered evidence to untraced')
    print('PASS all merge self-tests')


class MergeTests(unittest.TestCase):
    def test_searched(self):
        test_searched()

    def test_main(self):
        main()


if __name__ == '__main__':
    unittest.main()
