#!/usr/bin/env python3
"""Plan bounded code mapping batches from tracked source and configuration."""
import argparse
from collections import Counter, defaultdict
from pathlib import Path, PurePosixPath
import re
import sys
from if_json import write_json
from if_repo import git, is_test

EXCLUDED_DIRS = {'vendor', 'node_modules', 'dist', 'build', 'target', '.git', 'generated'}
DOC_SUFFIXES = {'.md', '.rst', '.txt', '.adoc'}
LOCK_NAMES = {'package-lock.json', 'npm-shrinkwrap.json', 'yarn.lock', 'pnpm-lock.yaml',
              'bun.lockb', 'bun.lock', 'Cargo.lock', 'Gemfile.lock', 'Pipfile.lock',
              'poetry.lock', 'uv.lock', 'composer.lock', 'go.sum', 'packages.lock.json'}


def alternatives(pattern):
    # stdlib exposes no public ERE splitter; track nesting solely to split top-level alternatives.
    result, start, depth, bracket, escaped = [], 0, 0, 0, False
    for index, char in enumerate(pattern):
        if escaped:
            escaped = False
        elif char == '\\':
            escaped = True
        elif bracket:
            if char == '[' and pattern[index + 1:index + 2] in (':', '.', '='):
                bracket += 1
            elif char == ']' and pattern[index - 1:index] not in ('[', '^'):
                bracket -= 1
        elif char == '[':
            bracket = 1
        elif char == '(':
            depth += 1
        elif char == ')':
            depth -= 1
        elif char == '|' and depth == 0:
            result.append(pattern[start:index])
            start = index + 1
    return list(dict.fromkeys(result + [pattern[start:]]))


def grep(repo, pattern, paths, counts=False):
    output = git(repo, 'grep', '-I', '-i', '-z', '-E', '-o' if counts else '-l',
                 '-e', pattern, '--', *paths, no_match=True)
    if not counts:
        return set(filter(None, output.split('\0')))
    matches = Counter()
    while output:
        path, separator, remainder = output.partition('\0')
        if not separator:
            raise ValueError('invalid git grep output')
        _, separator, output = remainder.partition('\n')
        matches[path] += 1
    return matches


def plan(repo, pattern, scope, out, tests=None, files_per_mapper=8, lines_per_mapper=2500, max_mappers=40):
    if min(files_per_mapper, lines_per_mapper, max_mappers) <= 0:
        raise ValueError('mapper limits must be positive')
    if tests:
        re.compile(tests)
    paths = []
    for name in filter(None, git(repo, 'ls-files', '-z', '--', *scope).split('\0')):
        path = PurePosixPath(name)
        if (is_test(name, tests) or path.suffix.lower() in DOC_SUFFIXES or
                set(path.parts[:-1]) & EXCLUDED_DIRS or path.name in LOCK_NAMES or
                path.suffix == '.lock' or '.min.' in path.name):
            continue
        paths.append(name)
    matching = grep(repo, pattern, paths) if paths else set()
    if not matching:
        raise ValueError(f'NO SURFACE: no file under {scope} mentions {pattern}')
    files, skipped = [], []
    for path in sorted(matching):
        with (Path(repo) / path).open(encoding='utf-8', errors='surrogateescape') as source:
            lines = sum(1 for _ in source)
        if lines > 4000:
            skipped.append(dict(path=path, reason='over 4000 lines', lines=lines))
        else:
            files.append(dict(path=path, lines=lines))
    if files:
        eligible = [row['path'] for row in files]
        counts = grep(repo, pattern, eligible, counts=True)
        scores = Counter(path for alternative in alternatives(pattern) for path in grep(repo, alternative, eligible))
        for row in files:
            row.update(systems=scores[row['path']], matches=counts[row['path']])
    files.sort(key=lambda row: (-row['systems'], -row['matches'], row['path']))
    directories = defaultdict(list)
    for row in files:
        directories[str(PurePosixPath(row['path']).parent)].append(row)
    batches = []
    for rows in directories.values():
        batch, lines = [], 0
        for row in rows:
            if batch and (len(batch) >= files_per_mapper or lines + row['lines'] > lines_per_mapper):
                batches.append(batch)
                batch, lines = [], 0
            batch.append(row)
            lines += row['lines']
        if batch:
            batches.append(batch)
    batches.sort(key=lambda batch: (-batch[0]['systems'],
                                    -batch[0]['matches'], batch[0]['path']))
    unmapped = [row['path'] for batch in batches[max_mappers:] for row in batch]
    result = dict(files=files, batches=[[row['path'] for row in batch] for batch in batches[:max_mappers]],
                  unmapped=unmapped, skipped=skipped)
    out = Path(out)
    out.mkdir(parents=True, exist_ok=True)
    write_json(out / 'map.json', result)
    print(f'MAPPED {out}/map.json files={len(files)} batches={len(result["batches"])}')
    for index, batch in enumerate(result['batches'], 1):
        print(f'PLAN MAP {index} {",".join(batch)}')
    if unmapped:
        print(f'UNMAPPED {len(unmapped)} files')
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--repo', required=True)
    parser.add_argument('--grep', required=True)
    parser.add_argument('--scope', required=True, nargs='+')
    parser.add_argument('--out', required=True, type=Path)
    parser.add_argument('--tests')
    parser.add_argument('--files-per-mapper', type=int, default=8)
    parser.add_argument('--lines-per-mapper', type=int, default=2500)
    parser.add_argument('--max-mappers', type=int, default=40)
    args = parser.parse_args()
    try:
        plan(args.repo, args.grep, args.scope, args.out, args.tests, args.files_per_mapper,
             args.lines_per_mapper, args.max_mappers)
    except (OSError, ValueError, TypeError, re.error) as exc:
        message = str(exc)
        sys.exit(message if message.startswith('NO SURFACE:') else f'MAP FAILED: {message}')


if __name__ == '__main__':
    main()
