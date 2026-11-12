#!/usr/bin/env python3
"""Check literal forecast scores and rank failure contracts without reading faults.json."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

RANK = Path(__file__).with_name('if_rank.py')
sys.path.insert(0, str(RANK.parent))


def main():
    with tempfile.TemporaryDirectory(prefix='rank-') as directory:
        out = Path(directory)
        (out / 'mine.json').write_text(json.dumps({'fixes': [{'sha': 'known'}]}))
        cells = [
            dict(id='real', values={'engine': 'codex'}, broke=['known'],
                 door='pkg/real.py:1', held='real', held_by=[], worst='wrong-state',
                 heat=2, interaction_share=0.5, prescore=1.4),
            dict(id='none', values={'engine': 'claude'}, broke=['known'],
                 door='pkg/none.py:2', held='none', held_by=[], worst='data-loss',
                 heat=4, interaction_share=0.75, prescore=5.0),
            dict(id='fake', values={'engine': 'codex'}, broke=['known'],
                 door='pkg/fake.py:3', held='fake', held_by=[], worst='wrong-target',
                 heat=2, interaction_share=0.5, prescore=1.8),
        ]
        # 4 * 1.25 * 1 * 1 = 5; 2 * 1 * .5 * .9 = .9;
        # 2 * 1 * .2 * .7 = .28.
        expected = [
            '1 none 5.0 none data-loss pkg/none.py:2 engine=claude',
            '2 fake 0.9 fake wrong-target pkg/fake.py:3 engine=codex',
            '3 real 0.28 real wrong-state pkg/real.py:1 engine=codex',
        ]

        def run(k):
            (out / 'cells.json').write_text(json.dumps({'cells': cells}))
            return subprocess.run([sys.executable, str(RANK), '--out', str(out), '--k', str(k)],
                                  capture_output=True, text=True)

        saved = cells[:]
        cells[:] = [dict(cells[1], id='untraced', held='untraced', prescore=1000), cells[2]]
        result = run(2)
        wanted = [f'FAULTS {out}/faults.json cells=2 forecast=1 untraced=1',
                  '1 fake 0.9 fake wrong-target pkg/fake.py:3 engine=codex']
        faults = json.loads((out / 'faults.json').read_text()) if (out / 'faults.json').exists() else {}
        actual = (result.returncode, result.stdout.splitlines(), result.stderr,
                  faults.get('forecast'), [(c['id'], c['score'], c['rank']) for c in faults.get('cells', [])])
        expected_untraced = (0, wanted, '', ['fake'], [('fake', 0.9, 1), ('untraced', 0.0, None)])
        assert actual == expected_untraced, (out / 'faults.json', expected_untraced, actual)
        cells[:] = saved
        print('PASS high-prescore untraced excluded with zero score; traced fake forecast and exact untraced=1')
        for k in (0, 2, 3, 4):
            result = run(k)
            wanted = [f'FAULTS {out}/faults.json cells=3 forecast={min(k, 3)} untraced=0', *expected[:k]]
            assert (result.returncode, result.stdout.splitlines(), result.stderr) == (0, wanted, ''), (
                out / 'cells.json', wanted, result)
        print('PASS exact order and literal scores; forecast length for K=0,2,3,4')
        for field, value, message in (
            ('broke', ['absent-sha'], "cell real cites shas not in mine.json: ['absent-sha']"),
            ('held', 'unknown', 'cell real held=unknown worst=wrong-state'),
            ('worst', 'unknown', 'cell real held=real worst=unknown'),
        ):
            original = cells[0][field]
            cells[0][field] = value
            result = run(3)
            cells[0][field] = original
            wanted = f'RANK FAILED: {message}\n'
            assert result.returncode != 0 and result.stderr == wanted, (out / 'cells.json', wanted, result)
            print(f'PASS invalid {field}: nonzero exit and exact RANK FAILED stderr')
        from if_rank import rank
        ranked = rank({'cells': json.loads(json.dumps(cells))}, {'fixes': [{'sha': 'known'}]}, 2)
        assert ranked['forecast'] == ['none', 'fake'], ranked
        assert [(cell['id'], cell['rank']) for cell in ranked['cells']] == [
            ('none', 1), ('fake', 2), ('real', None)], ranked
        diversity = [dict(cells[1], id='B01', door='p/x.py:1'),
                     dict(cells[1], id='B02', door='p/x.py:2'),
                     dict(cells[1], id='B03', door='p/x.py:3'),
                     dict(cells[2], id='N01', door='p/y.py:1'),
                     dict(cells[2], id='N02', door='p/z.py:1')]
        ranked = rank({'cells': diversity}, {'fixes': [{'sha': 'known'}]}, 5)
        assert ranked['forecast'] == ['B01', 'B02', 'N01', 'N02'], ranked
        assert [(c['id'], c['score'], c['rank']) for c in ranked['cells']] == [
            ('B01', 5.0, 1), ('B02', 5.0, 2), ('B03', 5.0, None),
            ('N01', .9, 3), ('N02', .9, 4)], ranked
        saved = cells[:]
        cells[:] = diversity
        result = run(5)
        assert result.stdout.splitlines() == [f'FAULTS {out}/faults.json cells=5 forecast=4 untraced=0',
            '1 B01 5.0 none data-loss p/x.py:1 engine=claude',
            '2 B02 5.0 none data-loss p/x.py:2 engine=claude',
            '3 N01 0.9 fake wrong-target p/y.py:1 engine=codex',
            '4 N02 0.9 fake wrong-target p/z.py:1 engine=codex'], result
        cells[:] = saved
        print('PASS two-per-file diversity and exact forecast table')
        for prescore in (0, 0.0001):
            cells[:] = [dict(saved[0], id='zero', door='pkg/none.py:1', prescore=prescore), saved[1]]
            result = run(3)
            wanted = [f'FAULTS {out}/faults.json cells=2 forecast=1 untraced=0',
                      '1 none 5.0 none data-loss pkg/none.py:2 engine=claude']
            assert (result.returncode, result.stdout.splitlines(), result.stderr) == (0, wanted, ''), (
                out / 'cells.json', wanted, result)
        cells[:] = saved
        print('PASS zero and rounded-zero scores excluded beside positive score at same door')
        result = run(-1)
        assert result.returncode != 0 and result.stderr == 'RANK FAILED: --k must be non-negative\n', result
        cells.clear()
        result = run(10)
        assert (result.returncode, result.stdout, result.stderr) == (
            0, f'FAULTS {out}/faults.json cells=0 forecast=0 untraced=0\n', ''), result
        print('PASS negative K rejected and empty forecast accepted')
        # Code-only forecasts have no history manifest; gone doors stay unranked.
        (out / 'mine.json').unlink()
        cells[:] = [dict(saved[1], id='code', broke=['M1-1'], source='code',
                         door_head='pkg/current.py:8', anchor='head'),
                    dict(saved[2], id='gone', anchor='gone', source='code', broke=['M1-2'])]
        result = run(3)
        wanted = [f'FAULTS {out}/faults.json cells=2 forecast=1 untraced=0',
                  '1 code 5.0 none data-loss pkg/current.py:8 engine=claude',
                  'GONE gone pkg/fake.py:3']
        assert (result.returncode, result.stdout.splitlines(), result.stderr) == (0, wanted, ''), (out, wanted, result)
        faults = json.loads((out / 'faults.json').read_text())
        assert [(c['id'], c['score'], c['rank']) for c in faults['cells']] == [
            ('code', 5.0, 1), ('gone', 0, None)], faults
        print('PASS code-only rank without mine; current doors displayed; gone excluded')
    print('PASS all rank self-tests')


class RankTests(unittest.TestCase):
    def test_main(self):
        main()


if __name__ == '__main__':
    unittest.main()
