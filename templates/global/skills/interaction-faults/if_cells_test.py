#!/usr/bin/env python3
"""Check normalized cells, bounded plans and mining boundaries."""
import contextlib
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).parent
sys.path.insert(0, str(ROOT))


def test_fix_tests():
    import if_cells
    from if_cells import attach_fix_tests
    planned = [{'door': 'x.go:1', 'broke': ['first', 'second']}, {'door': 'x.go:1', 'broke': ['first']}]
    outputs = {'first': 'x.go\nx_test.go\n', 'second': 'x.go\nx_test.go\n'}
    def show(command, **kwargs):
        assert command[:6] == ['git', '-C', '/fixture', 'show', '--name-only', '--format='], command
        return subprocess.CompletedProcess(command, 0, outputs[command[-1]], '')
    with patch('if_repo.subprocess.run', side_effect=show) as calls, \
         patch.object(if_cells, 'in_file_test_paths', return_value=set()):
        attach_fix_tests(planned, '/fixture')
    assert planned == [{'door': 'x.go:1', 'broke': ['first', 'second'], 'fix_tests': ['x_test.go']},
                       {'door': 'x.go:1', 'broke': ['first'], 'fix_tests': ['x_test.go']}], planned
    assert [call.args[0][-1] for call in calls.call_args_list] == ['first', 'second'], calls.call_args_list
    paths = ['a_test.go', 'test_a.py', 'a_test.py', 'a.test.ts', 'a.test.js',
             'a.spec.ts', 'a.spec.js', 'tests/a.txt', 'p/e2e/a.txt',
             'testdata/tests/a.txt', 'tests/testdata/a.txt', 'p/testing/a.txt', 'x.go']
    with patch.object(if_cells, 'git', return_value='\n'.join(reversed(paths))), \
             patch.object(if_cells, 'in_file_test_paths', return_value=set()):
        attach_fix_tests(planned, '/fixture')
    assert planned[0]['fix_tests'] == sorted(paths[:7]), planned
    print('PASS fix tests from two commits, sorted patterns, deduplication and SHA cache')
    far = [f'far/{i}_test.go' for i in range(10)]
    nearest = ['pkg/door/sub/z_test.go', 'pkg/a_test.go', 'pkg/b_test.go',
               'pkg/c_test.go', 'pkg/d_test.go', 'pkg/e_test.go', 'pkg/f_test.go',
               'pkg/doorway/a_test.go']
    failures = []
    for paths, expected in [
            (far + ['pkg/door/z_test.go', 'pkg/door/a_test.go'],
             ['pkg/door/a_test.go', 'pkg/door/z_test.go']),
            (far + nearest,
             ['pkg/door/sub/z_test.go', 'pkg/a_test.go', 'pkg/b_test.go',
              'pkg/c_test.go', 'pkg/d_test.go', 'pkg/doorway/a_test.go'])]:
        cell = {'door': 'pkg/door/x.go:12', 'broke': ['first']}
        with patch.object(if_cells, 'git', return_value='\n'.join(reversed(paths))), \
             patch.object(if_cells, 'in_file_test_paths', return_value=set()):
            attach_fix_tests([cell], '/fixture')
        if cell['fix_tests'] != expected:
            failures.append(('expected', expected, 'actual', cell['fix_tests']))
    assert failures == [], failures
    print('PASS same-directory tests beat 10 far files; fallback keeps 6 nearest with path ties')


def test_bad_sha():
    import if_cells
    with tempfile.TemporaryDirectory(prefix='bad-sha-') as directory:
        out = Path(directory)
        (out / 'labels').mkdir()
        (out / 'mine.json').write_text(json.dumps({'head_date': '2026-11-02', 'fixes': [
            {'sha': 'bad-sha', 'date': '2026-11-02', 'files': ['x.go']}]}))
        (out / 'labels/1.json').write_text(json.dumps([dict(sha='bad-sha', interaction=True,
            factors={'os': 'linux', 'backend': 'native'}, door='x.go:1', worst='wrong-state')]))
        with patch('sys.argv', ['if_cells.py', '--out', str(out), '--repo', '/fixture']), \
             patch('if_repo.subprocess.run', return_value=subprocess.CompletedProcess([], 128, '', 'bad revision')), \
             patch.object(if_cells, 'reanchor_cells'):
            try:
                if_cells.main()
            except SystemExit as exc:
                assert str(exc) == 'CELLS FAILED: bad-sha: git show --name-only --format=: bad revision', str(exc)
            else:
                raise AssertionError('expected CELLS FAILED for bad-sha')
    print('PASS bad SHA reports CELLS FAILED and names SHA')


def main():
    with tempfile.TemporaryDirectory(prefix='cells-') as directory:
        out = Path(directory)
        (out / 'labels').mkdir()
        fixes = [dict(sha=str(i), date='2026-11-02', files=['a/x.py']) for i in range(4)]
        (out / 'mine.json').write_text(json.dumps({'head_date': '2026-11-02', 'fixes': fixes}))
        labels = [
            dict(sha='0', interaction=True, factors={'mux': 'native_herdr', 'os': 'macos'}, door='a/x.py:20', worst='data-loss'),
            dict(sha='1', interaction=True, factors={'backend': 'herdr_native', 'os': 'macos'}, door='a/x.py:10', worst='wrong-state'),
            dict(sha='2', interaction=True, factors={'backend': 'herdr_native', 'os': 'linux', 'state': 'any'}, door='a/x.py:30', worst='wrong-state'),
            dict(sha='3', interaction=False, factors={'state': 'open'}, door='a/x.py:20', worst='cosmetic')]
        (out / 'labels/1.json').write_text(json.dumps(labels))
        (out / 'norm.json').write_text(json.dumps({'names': {'mux': 'backend'}, 'values': {'mux=native_herdr': 'herdr_native'}}))
        from if_cells import cells
        doc = cells(out)
        expected = [
            dict(id='C01', door='a/x.py:10', values={'backend': 'herdr_native', 'os': 'macos'}, broke=['0', '1'], worst='data-loss', heat=4.0, interaction_share=.75, held='none', held_by=[], prescore=1.0),
            dict(id='C02', door='a/x.py:30', values={'backend': 'herdr_native', 'os': 'linux'}, broke=['2'], worst='wrong-state', heat=4.0, interaction_share=.75, held='none', held_by=[], prescore=0.7)]
        cleaned = json.loads(json.dumps(labels))
        cleaned[0]['factors'] = {'backend': 'herdr_native', 'os': 'macos'}
        cleaned[2]['factors'] = {'backend': 'herdr_native', 'os': 'linux'}
        assert doc == {'labels': cleaned, 'cells': expected, 'invalid_pairs': 1}, (out, doc)
        print('PASS exact normalized cells, distinct conditions, scores and invalid pair count')
        def run(*args):
            import if_cells
            stdout, stderr = io.StringIO(), io.StringIO()
            command = ['if_cells.py', '--out', str(out), '--repo', '/fixture', *args]
            code = 0
            with patch('sys.argv', command), patch.object(if_cells, 'git', return_value='x.go\nx_test.go\n'), \
                 patch.object(if_cells, 'reanchor_cells'), \
                 patch.object(if_cells, 'in_file_test_paths', return_value=set()), \
                 contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                try:
                    if_cells.main()
                except SystemExit as exc:
                    code = exc.code if isinstance(exc.code, int) else 1
                    if isinstance(exc.code, str):
                        stderr.write(exc.code + '\n')
            return subprocess.CompletedProcess(command, code, stdout.getvalue(), stderr.getvalue())
        assert (out / 'trace').exists() is False, (out / 'trace', 'expected absent before run')
        for k, plans in [(0, []), (1, ['PLAN TRACE 1 C01|a/x.py:10|backend=herdr_native,os=macos|tests=x_test.go', 'PLAN TRACE 2 C02|a/x.py:30|backend=herdr_native,os=linux|tests=x_test.go'])]:
            result = run('--k', str(k), '--per-tracer', '1')
            wanted = [f'CELLS {out}/cells.json cells=2 traced_plan={2*k} invalid_rows=0 invalid_pairs=1 single=0', *plans]
            assert (result.returncode, result.stdout.splitlines(), result.stderr) == (0, wanted, ''), (out, wanted, result)
        assert (out / 'trace').is_dir() is True, (out / 'trace', 'expected directory after run')
        print('PASS exact bounded TRACE plans and trace directory creation')
        for value in ['other', 'any', 'either', 'all', 'none']:
            labels[2]['factors']['state'] = value
            (out / 'labels/1.json').write_text(json.dumps(labels))
            actual = cells(out)
            assert (actual['cells'], actual['invalid_pairs']) == (expected, 1), (value, actual)
        (out / 'norm.json').unlink()
        actual = cells(out)
        assert [(c['values'], c['broke']) for c in actual['cells']] == [
            ({'mux': 'native_herdr', 'os': 'macos'}, ['0']),
            ({'backend': 'herdr_native', 'os': 'linux'}, ['2']),
            ({'backend': 'herdr_native', 'os': 'macos'}, ['1'])], actual
        print('PASS all catch-all values and absent normalization')
        (out / 'norm.json').write_text(json.dumps({
            'names': {'mux': 'backend'}, 'values': {'state=open': 'any'}}))
        labels = [
            dict(sha='0', interaction=True, factors={'backend': 'native', 'os': 'macos'}, door='a/x.py:10', worst='data-loss'),
            dict(sha='1', interaction=True, factors={'mux': 'native', 'backend': 'native', 'state': 'open'}, door='a/x.py:20', worst='data-loss'),
            dict(sha='2', interaction=True, factors={'state': 'any'}, door='a/x.py:30', worst='data-loss'),
            dict(sha='3', interaction=False, factors={}, door='a/x.py:40', worst='cosmetic')]
        (out / 'labels/1.json').write_text(json.dumps(labels))
        result = run('--k', '1')
        actual = json.loads((out / 'cells.json').read_text())
        ranked = subprocess.run([sys.executable, str(ROOT / 'if_rank.py'), '--out', str(out), '--k', '3'],
                                capture_output=True, text=True)
        faults = json.loads((out / 'faults.json').read_text())
        checks = [
            ('cells', [(c['door'], c['values'], c['prescore'], c.get('single')) for c in actual['cells']], [
                ('a/x.py:10', {'backend': 'native', 'os': 'macos'}, 1.0, None),
                ('a/x.py:30', {}, 0, True),
                ('a/x.py:20', {'backend': 'native'}, 0, True)]),
            ('trace', (result.returncode, result.stdout.splitlines(), result.stderr), (0, [
                f'CELLS {out}/cells.json cells=3 traced_plan=1 invalid_rows=0 invalid_pairs=2 single=2',
                'PLAN TRACE 1 C01|a/x.py:10|backend=native,os=macos|tests=x_test.go'], '')),
            ('forecast', faults['forecast'], ['C01']),
            ('ranks', [(c['id'], c['score'], c['rank']) for c in faults['cells']], [
                ('C01', 1.0, 1), ('C02', 0.0, None), ('C03', 0.0, None)]),
            ('rank output', (ranked.returncode, ranked.stdout.splitlines(), ranked.stderr), (0, [
                f'FAULTS {out}/faults.json cells=3 forecast=1 untraced=0',
                '1 C01 1.0 none data-loss a/x.py:10 backend=native,os=macos'], ''))]
        failures = [(name, 'expected', expected, 'actual', observed, str(out))
                    for name, observed, expected in checks if observed != expected]
        assert failures == [], failures
        print('PASS zero/one conditions retained but excluded from TRACE and forecast; two included at same door')
        for args, error in [(('--k', '-1'), '--k must be non-negative'), (('--per-tracer', '0'), '--per-tracer must be positive')]:
            result = run(*args)
            assert (result.returncode, result.stderr) == (1, f'CELLS FAILED: {error}\n'), result
        (out / 'labels/1.json').write_text(json.dumps([7, {}, {'sha': '0', 'interaction': 'yes'}]))
        actual = cells(out)
        assert actual == {'labels': [{'invalid': 'row must be an object', 'row': 7}, {'invalid': 'missing sha'}, {'sha': '0', 'interaction': 'yes', 'invalid': 'interaction must be a boolean'}], 'cells': [], 'invalid_pairs': 0}, actual
        print('PASS invalid CLI and malformed rows')
        import if_mine
        import if_repo
        git_calls = []
        def fake_run(command, **kwargs):
            git_calls.append(command)
            args = command[3:]
            if args[0] == 'grep' and args[1] == '-IlzE':
                return subprocess.CompletedProcess(command, 1, '', '')
            if args[0] == 'grep':
                return subprocess.CompletedProcess(command, 0, 'a/file with space.py\0', '')
            if args[:2] == ['log', '-1']:
                output = '2026-11-02\n'
            elif args[0] == 'log':
                output = ''.join(f'@{i:010d}\t2026-11-02\tfix engine {i}\n\na/file with space.py\n' for i in range(25))
            else:
                output = 'diff\n'
            return subprocess.CompletedProcess(command, 0, output, '')
        capture = io.StringIO()
        with patch('sys.argv', ['if_mine.py', '--grep', 'engine', '--scope', 'a', '--since', '2026-01-01', '--out', str(out)]), \
             patch.object(if_repo.subprocess, 'run', side_effect=fake_run), contextlib.redirect_stdout(capture):
            if_mine.main()
        lines = capture.getvalue().splitlines()
        assert lines == ['FIXES 25', f'MINED {out}/mine.json fixes=25 paths=1 batches=3 head=2026-11-02',
                         'PLAN LABEL 1 ' + ','.join(f'{i:010d}' for i in range(12)),
                         'PLAN LABEL 2 ' + ','.join(f'{i:010d}' for i in range(12, 24)),
                         'PLAN LABEL 3 0000000024'], lines
        assert (out / 'subjects.txt').read_text() == ''.join(
            f'{i:010d} 2026-11-02 fix engine {i}\n' for i in range(25)), out / 'subjects.txt'
        assert json.loads((out / 'mine.json').read_text())['fixes'][0]['files'] == ['a/file with space.py'], out
        for code, error in [(1, 'NO SURFACE: no file under [\'a\'] mentions engine'),
                            (2, 'git grep -zilE engine: bad regex')]:
            with patch.object(if_repo.subprocess, 'run', return_value=subprocess.CompletedProcess([], code, '', 'bad regex')):
                try:
                    if_mine.mine('.', 'engine', ['a'], '2026-01-01', out, 12)
                except ValueError as exc:
                    assert str(exc) == error, (error, str(exc))
                else:
                    raise AssertionError(f'expected {error}')
        print('PASS exact LABEL plans, subjects, spaced paths and no-surface/error distinction')
    print('PASS all cells self-tests')



def test_anchors_and_code():
    import if_cells
    with tempfile.TemporaryDirectory(prefix='anchors-') as directory:
        repo = Path(directory)
        old = 'open_connection(value)'
        cases = [
            ('exact', 'before\n' + old + '\nafter\n', 'x.py:2'),
            ('moved', 'insert\nbefore\n' + old + '\nafter\n', 'x.py:3'),
            ('moved', old + '\nother\n' + old + '\n', 'x.py:1'),
            ('fuzzy', 'before\nopen_connection(values)\nafter\n', 'x.py:2'),
            ('drifted', 'before\nreturn completely_different\nafter\n', 'x.py:2'),
            ('gone', None, 'x.py:2'),
            ('file', 'anything\n', 'x.py')]
        for expected, contents, door in cases:
            target = repo / 'x.py'
            if contents is None:
                if target.exists():
                    target.unlink()
            else:
                target.write_text(contents)
            labels = [dict(sha='new', door='x.py' if expected == 'file' else 'x.py:2')]
            cell = dict(door=labels[0]['door'], broke=['new'], values={'a': 'one', 'b': 'two'},
                        heat=1, interaction_share=1, worst='wrong-state')
            def git(repo, *args, **kwargs):
                if args[0] == 'log':
                    return ''
                assert args == ('show', 'new:x.py'), args
                return 'before\n' + old + '\nafter\n'
            with patch.object(if_cells, 'git', side_effect=git):
                if_cells.reanchor_cells({'labels': labels, 'cells': [cell]}, repo,
                                        {'fixes': [{'sha': 'new', 'date': '2026-11-02'}]})
            if_cells.order_cells([cell])
            assert (cell['anchor'], cell['door_head'], cell['prescore']) == (
                expected, door, 0 if expected == 'gone' else 1.0), cell
            cell['fix_tests'] = []
            plans = list(if_cells.trace_plan([cell], 6))
            expected_plans = [] if expected == 'gone' else [f'C01|{door}|a=one,b=two|tests=-']
            assert plans == expected_plans, (expected_plans, plans, cell)
        (repo / 'x.py').unlink()
        (repo / 'final.py').write_text('before\n' + old + '\nafter\n')
        cell = dict(door='x.py:2', broke=['old', 'new'], values={}, heat=1,
                    interaction_share=1, worst='wrong-state')
        labels = [dict(sha=sha, door='x.py:2') for sha in ['old', 'new']]
        def renamed(repo, *args, **kwargs):
            if args[0] == 'log':
                assert args[-1] == 'new..HEAD', args
                return 'R100\tmiddle.py\tfinal.py\n\nR100\tx.py\tmiddle.py\n'
            assert args == ('show', 'new:x.py'), args
            return 'before\n' + old + '\nafter\n'
        with patch.object(if_cells, 'git', side_effect=renamed):
            if_cells.reanchor_cells({'labels': labels, 'cells': [cell]}, repo,
                                    {'fixes': [{'sha': 'new', 'date': '2026-11-02'},
                                               {'sha': 'old', 'date': '2026-11-02'}]})
        assert (cell['anchor'], cell['door_head']) == ('exact', 'final.py:2'), cell
        (repo / 'labels').mkdir()
        code = dict(source='code', id='M1-1', interaction=True, door='pkg/x.rs:2',
                    factors={'os': 'linux', 'state': 'open'}, worst='wrong-state')
        (repo / 'labels/1.json').write_text(json.dumps([code]))
        doc = if_cells.cells(repo)
        actual = doc['cells'][0]
        assert (actual['source'], actual['broke'], actual['heat'], actual['anchor'], actual['door_head']) == (
            'code', ['M1-1'], 1, 'head', 'pkg/x.rs:2'), actual
        with patch.object(if_cells, 'git', return_value='pkg/x.rs\npkg/FooTest.java\nfar/test_x.py\n'), \
             patch.object(if_cells, 'in_file_test_paths', return_value={'pkg/x.rs'}):
            if_cells.attach_fix_tests(doc['cells'], str(repo))
        assert actual['fix_tests'] == ['pkg/FooTest.java', 'pkg/x.rs'], actual
        capture = io.StringIO()
        with patch('sys.argv', ['if_cells.py', '--out', str(repo), '--repo', str(repo),
                                '--tests', 'custom_check']), \
             patch.object(if_cells, 'git', return_value='pkg/custom_check.rs\n'), \
             patch.object(if_cells, 'in_file_test_paths', return_value=set()), \
             contextlib.redirect_stdout(capture):
            if_cells.main()
        assert capture.getvalue().splitlines() == [
            f'CELLS {repo}/cells.json cells=1 traced_plan=1 invalid_rows=0 invalid_pairs=0 single=0',
            'PLAN TRACE 1 C01|pkg/x.rs:2|os=linux,state=open|tests=pkg/custom_check.rs'], capture.getvalue()
        (repo / 'mine.json').write_text(json.dumps({'head_date': '2026-11-02', 'fixes': [
            dict(sha='new', date='2026-11-02', files=['pkg/x.rs'])]}))
        history = dict(code, sha='new')
        del history['source'], history['id']
        (repo / 'labels/1.json').write_text(json.dumps([history, code]))
        mixed = if_cells.cells(repo)['cells'][0]
        assert (mixed['broke'], mixed['heat'], mixed['code_ids']) == (['M1-1', 'new'], 2, ['M1-1']), mixed
        (repo / 'labels/1.json').write_text(json.dumps([code, code]))
        try:
            if_cells.cells(repo)
        except ValueError as exc:
            assert str(exc) == f'{repo}/labels/1.json: unknown or duplicate id: M1-1', str(exc)
        else:
            raise AssertionError('duplicate code id accepted')
    print('PASS exact/moved/fuzzy/drifted/gone/file, chained renames, newest matching sha, code and mixed cells')

def test_sources_share_the_scale():
    import if_cells
    cells = [dict(door='a.rs:1', values={'a': 'x', 'b': 'y'}, heat=20, interaction_share=1, worst='data-loss', source='code'),
             dict(door='b.rs:1', values={'a': 'x', 'b': 'y'}, heat=10, interaction_share=1, worst='data-loss', source='code'),
             dict(door='c.rs:1', values={'a': 'x', 'b': 'y'}, heat=0.2, interaction_share=1, worst='data-loss')]
    if_cells.order_cells(cells)
    actual = [(c['door'], c['prescore']) for c in cells]
    assert actual == [('a.rs:1', 1.0), ('c.rs:1', 1.0), ('b.rs:1', 0.5)], actual
    print('PASS fix-history and code cells share one scale')


def test_plan_spreads_over_doors():
    import if_cells
    cells = [dict(id=f'C{i:02d}', door=f'{door}:{i}', prescore=score)
             for i, (door, score) in enumerate([('big.yml', 1.0), ('big.yml', .9), ('big.yml', .8),
                                                ('big.yml', .7), ('a.rs', .5), ('b.py', .4), ('c.ts', 0)], 1)]
    actual = [cell['id'] for cell in if_cells.plan_cells(cells, 5)]
    assert actual == ['C01', 'C02', 'C03', 'C05', 'C06'], actual
    print('PASS trace plan keeps one spare per door file')


def test_merged_names_conflict():
    import if_cells, tempfile
    with tempfile.TemporaryDirectory() as directory:
        out = Path(directory)
        (out / 'labels').mkdir()
        (out / 'norm.json').write_text(json.dumps({'names': {'clock': 'time', 'when': 'time'}, 'values': {}}))
        (out / 'labels' / 'map-1.json').write_text(json.dumps([dict(
            source='code', id='M1-1', interaction=True, door='a.py:3', worst='wrong-state',
            factors={'clock': 'night', 'when': 'cron', 'db': 'down'})]))
        doc = if_cells.cells(out)
    actual = ([c['values'] for c in doc['cells']], doc['invalid_pairs'])
    assert actual == ([{'time': 'night', 'db': 'down'}], 1), actual
    print('PASS merged factor names with two values keep the first and count the pair')


class CellsTests(unittest.TestCase):
    def test_checks(self):
        for check in (test_merged_names_conflict, test_plan_spreads_over_doors, test_sources_share_the_scale,
                      test_anchors_and_code, test_fix_tests, test_bad_sha, main):
            with self.subTest(check=check.__name__):
                check()


if __name__ == '__main__':
    unittest.main()
