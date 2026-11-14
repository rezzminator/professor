#!/usr/bin/env python3
"""Merge tracer evidence and report every rejected or uncovered cell."""
import argparse
from pathlib import Path
import sys
from if_rank import HELD
from if_json import read_json, write_json


def merge(out):
    doc = read_json(out / 'cells.json')
    indexed = {cell['id']: cell for cell in doc['cells']}
    for cell in indexed.values():
        cell.update(held='untraced', held_by=[])
        cell.pop('meet', None)
        cell.pop('searched', None)
    traced, rejected = set(), []
    for path in sorted((out / 'trace').glob('*.json')):
        rows = read_json(path)
        if not isinstance(rows, list):
            raise ValueError(f'{path}: expected an array')
        for index, row in enumerate(rows, 1):
            identity = row.get('id') if isinstance(row, dict) else None
            reason = None
            if not isinstance(identity, str) or identity not in indexed:
                reason = 'unknown id'
            elif not isinstance(row.get('held'), str) or row['held'] not in HELD:
                reason = f"invalid held: {row.get('held')}"
            elif identity in traced:
                reason = 'duplicate id'
            elif not isinstance(row.get('held_by'), list) or not all(isinstance(cite, str) for cite in row['held_by']):
                reason = 'held_by must be an array of test cites'
            elif 'searched' in row and (not isinstance(row['searched'], list)
                                         or not all(isinstance(path, str) for path in row['searched'])):
                reason = 'searched must be an array of strings'
            elif row['held'] == 'none' and not row.get('searched'):
                reason = 'none without searched'
            elif not isinstance(row.get('meet'), str):
                reason = 'meet must be a string'
            if reason:
                rejected.append(f'REJECTED {path.name} row={index} id={identity}: {reason}')
                continue
            indexed[identity].update({key: row[key] for key in ('held', 'held_by', 'meet')})
            if 'searched' in row:
                indexed[identity]['searched'] = row['searched']
            traced.add(identity)
    untraced = [identity for identity in indexed if identity not in traced]
    write_json(out / 'cells.json', doc)
    print(f'MERGED traced={len(traced)} untraced={len(untraced)} rejected={len(rejected)}')
    for line in rejected:
        print(line)
    for identity in untraced:
        print(f'UNTRACED {identity}')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', required=True, type=Path)
    parser.add_argument('--tests', help='test-path regex (classification occurs upstream)')
    args = parser.parse_args()
    try:
        merge(args.out)
    except (OSError, ValueError, KeyError, TypeError) as exc:
        sys.exit(f'MERGE FAILED: {exc}')


if __name__ == '__main__':
    main()
