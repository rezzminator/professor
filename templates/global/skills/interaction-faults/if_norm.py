#!/usr/bin/env python3
"""Plan factor-pair normalization and merge validated synonym maps."""
import argparse
from collections import Counter, defaultdict
from pathlib import Path
import re
import sys
from if_json import read_json, write_json


def snake_case(value):
    return isinstance(value, str) and re.fullmatch(r'[a-z][a-z0-9]*(?:_[a-z0-9]+)*', value) is not None


def plan(out, batch):
    if batch <= 0:
        raise ValueError('--batch must be positive')
    counts = Counter()
    for path in sorted((out / 'labels').glob('*.json')):
        rows = read_json(path)
        if not isinstance(rows, list):
            raise ValueError(f'{path}: expected an array')
        for row in rows:
            if not isinstance(row, dict) or row.get('interaction') is not True:
                continue
            factors = row.get('factors', {})
            if isinstance(factors, dict):
                for name, value in factors.items():
                    if snake_case(name) and snake_case(value):
                        counts[name, value] += 1
    names = defaultdict(list)
    for (name, value), count in sorted(counts.items()):
        names[name].append(f'{name}={value} {count}\n')
    batches, current = [], []
    for lines in names.values():
        if current and len(current) + len(lines) > batch:
            batches.append(current)
            current = []
        while len(lines) > batch:
            batches.append(lines[:batch])
            lines = lines[batch:]
        current.extend(lines)
    if current:
        batches.append(current)
    pairs = out / 'pairs'
    pairs.mkdir(parents=True, exist_ok=True)
    (out / 'norm').mkdir(exist_ok=True)
    for path in pairs.glob('*.txt'):
        if path.stem.isdecimal():
            path.unlink()
    print(f'PAIRS {len(counts)} batches={len(batches)}')
    for index, lines in enumerate(batches, 1):
        path = pairs / f'{index}.txt'
        path.write_text(''.join(lines))
        print(f'PLAN NORM {index} {path}')


def apply(out):
    paths = list((out / 'norm').glob('*.json'))
    maps = {'names': {}, 'values': {}}
    rejected = conflicts = 0
    if paths:
        pairs = set()
        for path in (out / 'pairs').glob('*.txt'):
            for line in path.read_text().splitlines():
                pair, count = line.rsplit(' ', 1)
                int(count)
                pairs.add(pair)
        allowed = {'names': {pair.split('=', 1)[0] for pair in pairs}, 'values': pairs}
        for path in sorted(paths, key=lambda p: (0, int(p.stem)) if p.stem.isdecimal() else (1, p.name)):
            doc = read_json(path)
            for kind in maps:
                for key, canonical in doc[kind].items():
                    if key not in allowed[kind] or not snake_case(canonical):
                        rejected += 1
                    elif key in maps[kind]:
                        conflicts += int(maps[kind][key] != canonical)
                    else:
                        maps[kind][key] = canonical
    write_json(out / 'norm.json', maps)
    print(f"NORM names={len(maps['names'])} values={len(maps['values'])} rejected={rejected} conflicts={conflicts}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', required=True, type=Path)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument('--plan', action='store_true')
    mode.add_argument('--apply', action='store_true')
    parser.add_argument('--batch', type=int, default=300)
    parser.add_argument('--tests', help='test-path regex (classification occurs upstream)')
    args = parser.parse_args()
    try:
        if args.plan:
            plan(args.out, args.batch)
        else:
            apply(args.out)
    except (OSError, ValueError, KeyError, TypeError, AttributeError) as exc:
        sys.exit(f'NORM FAILED: {exc}')


if __name__ == '__main__':
    main()
