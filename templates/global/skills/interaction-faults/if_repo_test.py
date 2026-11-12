"""Repository classification and read failures."""
import subprocess
import contextlib
import io
import tempfile
import sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parent))
import unittest
from unittest.mock import patch
import if_repo


class RepoTests(unittest.TestCase):
    def test_paths(self):
        for path, expected in [('a_test.go', True), ('test_a.py', True), ('a.test.ts', True),
                ('a.spec.js', True), ('a_spec.rb', True), ('OrderTest.java', True),
                ('OrderTests.cs', True), ('OrderIT.java', True), ('conftest.py', True),
                ('src/tests/lib.rs', True), ('Fixtures/input.json', True), ('testdata/a', True),
                ('src/lib.rs', False), ('src/Testament.java', False), ('contest/a.py', False)]:
            with self.subTest(path=path):
                self.assertEqual(if_repo.is_test(path), expected)
        self.assertEqual(if_repo.is_test('checks/a.py', r'^checks/'), True)
        for path, expected in [('tests/test_a.py', True), ('docker-compose.test.yml', False),
                ('docs/spec/a.md', False), ('Fixtures/input.json', False), ('src/a.spec.ts', True)]:
            with self.subTest(runnable=path):
                self.assertEqual(if_repo.is_runnable_test(path), expected)
        self.assertEqual(if_repo.is_runnable_test('checks/a.yml', r'^checks/'), True)
        self.assertEqual(if_repo.is_test('test_a.py', r'^checks/'), True)

    def test_in_file_and_errors(self):
        with patch('if_repo.git', return_value='src/lib.rs\0') as query:
            self.assertEqual(if_repo.in_file_test_paths('.', ['src/lib.rs', 'src/main.rs']), {'src/lib.rs'})
            self.assertEqual(query.call_count, 1)
        for code, expected in [(0, 'ok'), (1, '')]:
            with patch('if_repo.subprocess.run', return_value=subprocess.CompletedProcess([], code, 'ok', '')):
                self.assertEqual(if_repo.git('.', 'grep', no_match=True), expected)
        with patch('if_repo.subprocess.run', return_value=subprocess.CompletedProcess([], 128, '', 'bad repository')):
            with self.assertRaisesRegex(ValueError, 'bad repository'):
                if_repo.git('.', 'grep', no_match=True)

    def test_mine_status(self):
        import if_mine
        with tempfile.TemporaryDirectory(prefix='mine-test-') as directory:
            capture = io.StringIO()
            with patch('if_mine.git', side_effect=['src/app.rs\0', '2026-11-02\n', '']), \
                 contextlib.redirect_stdout(capture):
                with self.assertRaisesRegex(ValueError, '^NO FIXES: 0 fix commits'):
                    if_mine.mine('.', 'engine', ['src'], '2026-01-01', Path(directory), 12)
            self.assertEqual(capture.getvalue(), 'FIXES 0\n')

    def test_pack_batches(self):
        import if_mine
        sizes = {'a': 800, 'b': 800, 'c': 800, 'd': 1, 'e': 10, 'f': 10, 'g': 10}
        self.assertEqual(if_mine.pack_batches(list('abcdefg'), sizes, 3),
                         [['a', 'b', 'c'], ['d', 'e', 'f'], ['g']])
        sizes['c'] = 801
        self.assertEqual(if_mine.pack_batches(list('abc'), sizes, 12), [['a', 'b'], ['c']])

if __name__ == '__main__':
    unittest.main()
