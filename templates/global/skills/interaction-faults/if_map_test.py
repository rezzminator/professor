"""Map planning over ordinary files, with the git read door mocked."""
import contextlib
import io
import json
import sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parent))
import re
import tempfile
import unittest
from unittest.mock import patch
import if_map


class MapTests(unittest.TestCase):
    def test_plan(self):
        contents = {'src/main.go': 'docker postgres\ndocker\n', 'src/app.py': 'docker\n',
            'web/app.ts': 'postgres\n', 'rust/lib.rs': 'docker\n#[cfg(test)]\n',
            'java/App.java': 'docker\n', 'compose.yaml': 'docker postgres\n',
            'tests/test_app.py': 'docker\n', 'README.md': 'docker\n',
            'vendor/a.go': 'docker\n', 'package-lock.json': 'docker\n',
            'web/a.min.js': 'docker\n', 'src/binary': 'docker\0',
            'src/huge.py': 'docker\n' * 4001}
        with tempfile.TemporaryDirectory(prefix='map-test-') as directory:
            root = Path(directory)
            for path, body in contents.items():
                dest = root / path
                dest.parent.mkdir(parents=True, exist_ok=True)
                dest.write_text(body)
            def git(repo, *args, no_match=False):
                if args[0] == 'ls-files':
                    return '\0'.join(contents) + '\0'
                self.assertIn('-I', args)
                pattern = args[args.index('-e') + 1]
                paths = args[args.index('--') + 1:]
                matched = {p: len(re.findall(pattern, contents[p], re.I)) for p in paths if '\0' not in contents[p]}
                if '-o' in args:
                    return ''.join((p + '\0match\n') * n for p, n in matched.items() if n)
                return ''.join(p + '\0' for p, n in matched.items() if n)
            out = root / 'out'
            with patch('if_map.git', side_effect=git), contextlib.redirect_stdout(io.StringIO()) as capture:
                result = if_map.plan(str(root), 'docker|postgres', ['.'], out, files_per_mapper=2,
                                     lines_per_mapper=2, max_mappers=2)
                self.assertEqual({f['path'] for f in result['files']},
                    {'src/main.go', 'src/app.py', 'web/app.ts', 'rust/lib.rs', 'java/App.java', 'compose.yaml'})
                scores = {f['path']: (f['systems'], f['matches']) for f in result['files']}
                self.assertEqual(scores['src/main.go'], (2, 3))
                self.assertEqual(result['batches'], [['src/main.go'], ['compose.yaml']])
                self.assertEqual(sorted(result['unmapped']), ['java/App.java', 'rust/lib.rs', 'src/app.py', 'web/app.ts'])
                self.assertEqual(result['skipped'], [{'path': 'src/huge.py', 'reason': 'over 4000 lines', 'lines': 4001}])
                complete = if_map.plan(str(root), 'docker|postgres', ['.'], root / 'all',
                                       files_per_mapper=2, lines_per_mapper=2)
                self.assertEqual(complete['batches'], [['src/main.go'], ['compose.yaml'],
                    ['java/App.java'], ['rust/lib.rs'], ['src/app.py'], ['web/app.ts']])
                with self.assertRaisesRegex(ValueError, 'mapper limits must be positive'):
                    if_map.plan(str(root), 'docker', ['.'], out, max_mappers=0)
                self.assertEqual(json.loads((out / 'map.json').read_text()), result)
                self.assertIn('UNMAPPED 4 files\n', capture.getvalue())
                with self.assertRaisesRegex(ValueError, '^NO SURFACE:'):
                    if_map.plan(str(root), 'nomatch', ['.'], out)
                contents.clear()
                contents.update({'a/high.py': 'docker postgres\n', 'a/low.py': 'docker\n' * 10,
                                 'b/high.py': 'docker postgres docker\n'})
                for path, body in contents.items():
                    dest = root / path
                    dest.parent.mkdir(parents=True, exist_ok=True)
                    dest.write_text(body)
                priority = if_map.plan(str(root), 'docker|postgres', ['.'], root / 'priority', max_mappers=1)
                self.assertEqual(priority['batches'], [['b/high.py']])
                self.assertEqual(priority['unmapped'], ['a/high.py', 'a/low.py'])
            self.assertEqual(if_map.alternatives(r'(docker|podman)|post[g|r]es|a\|b'),
                             ['(docker|podman)', 'post[g|r]es', r'a\|b'])
            self.assertEqual(if_map.alternatives('[[:alpha:]|]|x'), ['[[:alpha:]|]', 'x'])

if __name__ == '__main__':
    unittest.main()
