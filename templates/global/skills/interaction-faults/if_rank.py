#!/usr/bin/env python3
"""Rank door cells by heat, interaction share, coverage and severity."""
import argparse
from collections import Counter
import math
from pathlib import Path
import sys
from if_json import read_json, write_json

W = {'data-loss': 1.0, 'wrong-target': 0.9, 'wrong-state': 0.7, 'cosmetic': 0.3}
PER_DOOR = 2  # forecast cells per door file
HELD = {'none': 1.0, 'fake': 0.5, 'real': 0.2, 'untraced': 0.0}


def value_text(values):
    return ','.join(f'{key}={values[key]}' for key in sorted(values))


def rank(doc, mine, k):
    if k < 0:
        raise ValueError('--k must be non-negative')
    shas = {fix['sha'][:10] for fix in (mine or {}).get('fixes', [])}
    for cell in doc['cells']:
        code_ids = set(cell['broke'] if cell.get('source') == 'code' else cell.get('code_ids', []))
        bad = [sha for sha in cell['broke'] if sha not in code_ids and sha[:10] not in shas]
        if bad:
            raise ValueError(f"cell {cell['id']} cites shas not in mine.json: {bad}")
        if cell.get('held') not in HELD or cell.get('worst') not in W:
            raise ValueError(f"cell {cell['id']} held={cell.get('held')} worst={cell.get('worst')}")
        heat, share = cell['heat'], cell['interaction_share']
        if not math.isfinite(heat) or heat < 0 or not 0 <= share <= 1:
            raise ValueError(f"cell {cell['id']} invalid heat or interaction_share")
        cell['score'] = 0 if cell.get('anchor') == 'gone' else round(cell['prescore'] * HELD[cell['held']], 4)
    doc['cells'].sort(key=lambda cell: (cell.get('anchor') == 'gone', -cell['score'], cell['id']))
    doc['forecast'], doors = [], Counter()
    for cell in doc['cells']:
        cell['rank'] = None
        door = cell.get('door_head', cell['door']).split(':', 1)[0]
        if cell['score'] == 0 or len(doc['forecast']) == k or doors[door] == PER_DOOR:
            continue
        doc['forecast'].append(cell['id'])
        cell['rank'] = len(doc['forecast'])
        doors[door] += 1
    return doc


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', required=True, type=Path)
    parser.add_argument('--k', type=int, default=10)
    parser.add_argument('--tests', help='test-path regex (classification occurs upstream)')
    args = parser.parse_args()
    try:
        mine = read_json(args.out / 'mine.json') if (args.out / 'mine.json').exists() else None
        doc = rank(read_json(args.out / 'cells.json'), mine, args.k)
        write_json(args.out / 'faults.json', doc)
        untraced = sum(cell['held'] == 'untraced' for cell in doc['cells'])
        print(f"FAULTS {args.out}/faults.json cells={len(doc['cells'])} "
              f"forecast={len(doc['forecast'])} untraced={untraced}")
        for cell in doc['cells']:
            if cell['rank'] is not None:
                print(cell['rank'], cell['id'], cell['score'], cell['held'], cell['worst'],
                      cell.get('door_head', cell['door']), value_text(cell['values']))
        for cell in doc['cells']:
            if cell.get('anchor') == 'gone':
                print('GONE', cell['id'], cell['door'])
    except (OSError, ValueError, KeyError, TypeError) as exc:
        sys.exit(f'RANK FAILED: {exc}')


if __name__ == '__main__':
    main()
