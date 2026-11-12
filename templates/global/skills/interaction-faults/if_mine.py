#!/usr/bin/env python3
"""Mine fix history, capped diffs, subjects and uncapped labeler plans."""
import argparse
from collections import Counter
from pathlib import Path
import sys
from if_json import write_json
from if_repo import git, is_test, in_file_test_paths


# A labeler reads every diff of its batch; the line budget keeps a batch inside one comfortable read.
DIFF_CAP = 800
BATCH_LINES = 2400


def pack_batches(shas, sizes, most):
    batches = []
    for sha in shas:
        if batches and len(batches[-1]) < most and sum(sizes[s] for s in batches[-1]) + sizes[sha] <= BATCH_LINES:
            batches[-1].append(sha)
        else:
            batches.append([sha])
    return batches


def mine(repo, pattern, scope, since, out, batch, tests=None):
    if batch <= 0:
        raise ValueError('MINE FAILED: --batch must be positive')
    paths = [path for path in git(repo, 'grep', '-zilE', pattern, '--', *scope, no_match=True).split('\0')
             if path and not path.endswith('.md')]
    if not paths:
        raise ValueError(f'NO SURFACE: no file under {scope} mentions {pattern}')
    head_date = git(repo, 'log', '-1', '--format=%cs').strip()
    log = git(repo, 'log', '--no-merges', '-i', '-E', '--grep=fix|bug|regress|broke|revert',
              f'--since={since} 00:00', '--format=@%H\t%as\t%s', '--name-only', '--', *paths)
    fixes, current = [], None
    for line in log.splitlines():
        if line.startswith('@'):
            sha, date, subject = line[1:].split('\t', 2)
            current = dict(sha=sha[:10], date=date, subject=subject, files=[], tests=[])
            fixes.append(current)
        elif line.strip() and current is not None:
            key = 'tests' if is_test(line.strip(), tests) else 'files'
            current[key].append(line.strip())
    in_file = in_file_test_paths(repo, sorted({path for fix in fixes for path in fix['files']}))
    for fix in fixes:
        fix['tests'].extend(path for path in fix['files'] if path in in_file)
    fixes = [fix for fix in fixes if fix['files']]
    print(f'FIXES {len(fixes)}')
    if not fixes:
        raise ValueError(f'NO FIXES: 0 fix commits since {since} on {len(paths)} paths')
    (out / 'diffs').mkdir(parents=True, exist_ok=True)
    sizes = {}
    for fix in fixes:
        stat = git(repo, 'show', '--stat', '--format=%H %as%n%B', fix['sha'], '--', *fix['files'])
        body = git(repo, 'show', '--format=', fix['sha'], '--', *fix['files']).splitlines()
        omitted = f'\n[... {len(body)-DIFF_CAP} more diff lines]' if len(body) > DIFF_CAP else ''
        sizes[fix['sha']] = min(len(body), DIFF_CAP)
        (out / 'diffs' / f"{fix['sha']}.txt").write_text(
            stat + '\n'.join(body[:DIFF_CAP]) + omitted + '\ntests touched: ' + ' '.join(fix['tests']) + '\n')
    hot = Counter(path for fix in fixes for path in fix['files'])
    batches = pack_batches([fix['sha'] for fix in fixes], sizes, batch)
    write_json(out / 'mine.json', dict(head_date=head_date, since=since, paths=len(paths), fixes=fixes,
                                     hot_files=hot.most_common(40), batches=batches))
    (out / 'subjects.txt').write_text(''.join(f"{fix['sha']} {fix['date']} {fix['subject']}\n" for fix in fixes))
    print(f'MINED {out}/mine.json fixes={len(fixes)} paths={len(paths)} batches={len(batches)} head={head_date}')
    for index, shas in enumerate(batches, 1):
        print(f"PLAN LABEL {index} {','.join(shas)}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--repo', default='.')
    parser.add_argument('--grep', required=True, help='system names as an extended regular expression')
    parser.add_argument('--scope', nargs='+', required=True)
    parser.add_argument('--since', required=True)
    parser.add_argument('--out', required=True, type=Path)
    parser.add_argument('--batch', type=int, default=12)
    parser.add_argument('--tests')
    args = parser.parse_args()
    try:
        mine(args.repo, args.grep, args.scope, args.since, args.out, args.batch, args.tests)
    except (OSError, ValueError, KeyError, TypeError) as exc:
        message = str(exc)
        sys.exit(message if message.startswith(('MINE FAILED:', 'NO SURFACE:', 'NO FIXES:')) else f'MINE FAILED: {message}')


if __name__ == '__main__':
    main()
