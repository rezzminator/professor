#!/usr/bin/env python3
"""Aggregate labeled fixes by door file and print bounded tracer plans."""
import argparse
from collections import Counter, defaultdict
from datetime import date
from difflib import SequenceMatcher
import math
from pathlib import Path, PurePosixPath
from posixpath import commonpath
import sys
from if_rank import PER_DOOR, W, value_text
from if_json import read_json, write_json
from if_norm import snake_case
from if_repo import git, is_runnable_test, in_file_test_paths


def most_common(counts):
    return min(counts, key=lambda value: (-counts[value], value))


def cells(out):
    mine = read_json(out / 'mine.json') if (out / 'mine.json').exists() else {'fixes': []}
    norm = read_json(out / 'norm.json') if (out / 'norm.json').exists() else {'names': {}, 'values': {}}
    head = date.fromisoformat(mine['head_date']) if mine['fixes'] else None
    heat = Counter()
    shas = {fix['sha'] for fix in mine['fixes']}
    for fix in mine['fixes']:
        weight = math.exp(-(head - date.fromisoformat(fix['date'])).days / 7)
        for path in set(fix['files'] + fix.get('tests', [])):
            heat[path] += weight
    labels, by_door, seen = [], defaultdict(list), set()
    invalid_pairs = 0
    for path in sorted((out / 'labels').glob('*.json')):
        rows = read_json(path)
        if not isinstance(rows, list):
            raise ValueError(f'{path}: expected an array')
        for row in rows:
            if not isinstance(row, dict):
                labels.append({'invalid': 'row must be an object', 'row': row})
                continue
            key = 'id' if row.get('source') == 'code' else 'sha'
            if not row.get(key):
                row['invalid'] = f'missing {key}'
            elif type(row.get('interaction')) is not bool:
                row['invalid'] = 'interaction must be a boolean'
            else:
                row.pop('invalid', None)
            labels.append(row)
            if 'invalid' in row:
                continue
            sha = row[key]
            if (key == 'sha' and sha not in shas) or (key, sha) in seen:
                raise ValueError(f'{path}: unknown or duplicate {key}: {sha}')
            seen.add((key, sha))
            if row['worst'] not in W:
                raise ValueError(f'{path}: invalid worst for {sha}')
            door = row['door'].split(':', 1)[0]
            if not door:
                raise ValueError(f'{path}: empty door for {sha}')
            raw = row.get('factors', {})
            row['factors'] = {}
            if not isinstance(raw, dict):
                invalid_pairs += 1
            else:
                for name, value in raw.items():
                    canonical = norm['names'].get(name, name)
                    normalized = norm['values'].get(f'{name}={value}', value)
                    if (snake_case(canonical) and snake_case(normalized)
                            and value not in ('other', 'any', 'either', 'all', 'none')
                            and normalized not in ('other', 'any', 'either', 'all', 'none')):
                        if canonical in row['factors'] and row['factors'][canonical] != normalized:
                            invalid_pairs += 1  # two names merged into one factor with two values: keep the first
                        else:
                            row['factors'][canonical] = normalized
                    else:
                        invalid_pairs += 1
            by_door[door].append(row)
            if key == 'id':
                heat[door] += 1
    broken = []
    for door, rows in by_door.items():
        interactions = [row for row in rows if row['interaction']]
        groups = defaultdict(list)
        for row in interactions:
            groups[frozenset(row['factors'].items())].append(row)
        for values, group in groups.items():
            values = dict(values)
            broken.append(dict(door=most_common(Counter(row['door'] for row in group)),
                               values=values, broke=sorted(row.get('sha') or row['id'] for row in group),
                               worst=max((row['worst'] for row in group), key=W.get),
                               heat=heat[door], interaction_share=len(interactions)/len(rows),
                               held='none', held_by=[]))
            code_ids = sorted(row['id'] for row in group if row.get('source') == 'code')
            if len(code_ids) == len(group):
                broken[-1].update(source='code', door_head=broken[-1]['door'], anchor='head')
            elif code_ids:
                broken[-1]['code_ids'] = code_ids
    order_cells(broken)
    return {'labels': labels, 'cells': broken, 'invalid_pairs': invalid_pairs}


def cell_key(cell):
    return (cell['door'].split(':', 1)[0],
            tuple(sorted(cell['values'].items())))


def order_cells(cells):
    for cell in cells:
        if cell.get('anchor') == 'gone':
            cell['prescore'] = 0
        elif len(cell['values']) < 2:
            cell['prescore'] = 0
            cell['single'] = True
        else:
            cell['prescore'] = cell['heat'] * (0.5 + cell['interaction_share']) * W[cell['worst']]
    # Fix-history heat (decayed fix counts) and code heat (meeting points mapped) share no unit:
    # each source is scaled to its own best cell so neither buries the other.
    top = Counter()
    for cell in cells:
        top[cell.get('source', 'history')] = max(top[cell.get('source', 'history')], cell['prescore'])
    for cell in cells:
        best = top[cell.get('source', 'history')]
        cell['prescore'] = round(cell['prescore'] / best, 4) if best else 0
    cells.sort(key=lambda cell: (-cell['prescore'], cell_key(cell)))
    for index, cell in enumerate(cells, 1):
        cell['id'] = f'C{index:02d}'


def reanchor_cells(doc, repo, mine):
    fixes = {fix['sha']: (fix['date'], -index) for index, fix in enumerate(mine['fixes'])}
    labels = {row['sha']: row for row in doc['labels'] if 'sha' in row and 'invalid' not in row}
    for cell in doc['cells']:
        if cell.get('source') == 'code':
            continue
        matching = [sha for sha in cell['broke'] if sha in labels and labels[sha]['door'] == cell['door']]
        # A mixed cell's modal door may come only from current-tree mapper rows.
        if not matching:
            cell.update(door_head=cell['door'], anchor='head')
            continue
        sha = max(matching, key=fixes.__getitem__)
        path, separator, number = cell['door'].partition(':')
        head_path = path
        if not (Path(repo) / head_path).is_file():
            renames = git(repo, 'log', '-M', '--name-status', '--format=', f'{sha}..HEAD')
            for line in reversed(renames.splitlines()):
                fields = line.split('\t')
                if len(fields) == 3 and fields[0].startswith('R') and fields[1] == head_path:
                    head_path = fields[2]
        cell['door_head'] = head_path + (separator + number if separator else '')
        if not (Path(repo) / head_path).is_file():
            cell['anchor'] = 'gone'
            continue
        if not separator:
            cell['anchor'] = 'file'
            continue
        old_number = int(number)
        if old_number <= 0:
            raise ValueError(f"invalid door line: {cell['door']}")
        old_lines = git(repo, 'show', f'{sha}:{path}').splitlines()
        head_lines = (Path(repo) / head_path).read_text().splitlines()
        cell['anchor'] = 'drifted'
        if old_number > len(old_lines):
            continue
        text = old_lines[old_number - 1].strip()
        exact = [index for index, line in enumerate(head_lines, 1) if line.strip() == text]
        if exact:
            line_number = min(exact, key=lambda index: (abs(index - old_number), index))
            cell['anchor'] = 'exact' if line_number == old_number else 'moved'
        else:
            candidates = [(SequenceMatcher(None, text, line.strip()).ratio(), index)
                          for index, line in enumerate(head_lines, 1)]
            ratio, line_number = max(candidates, key=lambda pair: (pair[0], -abs(pair[1] - old_number), -pair[1]),
                                     default=(0, old_number))
            if ratio < 0.8:
                continue
            cell['anchor'] = 'fuzzy'
        cell['door_head'] = f'{head_path}:{line_number}'


def attach_fix_tests(cells, repo, tests_override=None):
    by_sha = {}
    for cell in cells:
        code_ids = cell.get('code_ids', [])
        sources = ([None] if cell.get('source') == 'code' else
                   [sha for sha in cell['broke'] if sha not in code_ids] + ([None] if code_ids else []))
        for sha in sources:
            if sha not in by_sha:
                try:
                    paths = git(repo, 'ls-files').splitlines() if sha is None else git(
                        repo, 'show', '--name-only', '--format=', sha).splitlines()
                    in_file = in_file_test_paths(repo, paths)
                except (OSError, ValueError) as exc:
                    raise ValueError(f'{sha}: {exc}') from exc
                by_sha[sha] = {path for path in paths if is_runnable_test(path, tests_override) or path in in_file}
        tests = sorted({path for sha in sources for path in by_sha[sha]})
        door = cell.get('door_head', cell['door']).split(':', 1)[0]
        same_dir = [path for path in tests if PurePosixPath(path).parent == PurePosixPath(door).parent]
        cell['fix_tests'] = same_dir or sorted(
            tests, key=lambda path: (-len(PurePosixPath(commonpath([door, path])).parts), path))[:6]


# The forecast shows at most PER_DOOR cells per door file; the plan traces one spare per file
# so a large file cannot spend the whole trace budget on cells the forecast would drop.


def plan_cells(cells, size):
    planned, doors = [], Counter()
    for cell in sorted((cell for cell in cells if cell['prescore'] > 0), key=lambda cell: (-cell['prescore'], cell['id'])):
        door = cell.get('door_head', cell['door']).split(':', 1)[0]
        if len(planned) < size and doors[door] <= PER_DOOR:
            planned.append(cell)
            doors[door] += 1
    return planned


def trace_plan(cells, limit):
    packages = defaultdict(list)
    for cell in cells:
        if cell.get('anchor') != 'gone':
            packages[str(PurePosixPath(cell.get('door_head', cell['door']).split(':', 1)[0]).parent)].append(cell)
    # Oversized packages need consecutive chunks to respect the per-tracer bound.
    for group in packages.values():
        for start in range(0, len(group), limit):
            yield ' ; '.join(f"{cell['id']}|{cell.get('door_head', cell['door'])}|{value_text(cell['values'])}|tests={','.join(cell['fix_tests']) or '-'}"
                             for cell in group[start:start + limit])


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', required=True, type=Path)
    parser.add_argument('--repo', required=True)
    parser.add_argument('--tests')
    parser.add_argument('--per-tracer', type=int, default=6)
    parser.add_argument('--k', type=int, default=10)
    args = parser.parse_args()
    try:
        if args.per_tracer <= 0:
            raise ValueError('--per-tracer must be positive')
        if args.k < 0:
            raise ValueError('--k must be non-negative')
        doc = cells(args.out)
        mine = read_json(args.out / 'mine.json') if (args.out / 'mine.json').exists() else {'fixes': []}
        reanchor_cells(doc, args.repo, mine)
        order_cells(doc['cells'])
        invalid = sum('invalid' in row for row in doc['labels'])
        planned = plan_cells(doc['cells'], 3 * args.k)
        attach_fix_tests(planned, args.repo, args.tests)
        write_json(args.out / 'cells.json', doc)
        single = sum(cell.get('single', False) for cell in doc['cells'])
        print(f"CELLS {args.out}/cells.json cells={len(doc['cells'])} traced_plan={len(planned)} "
              f"invalid_rows={invalid} invalid_pairs={doc['invalid_pairs']} single={single}")
        (args.out / 'trace').mkdir(exist_ok=True)
        for index, row in enumerate(trace_plan(planned, args.per_tracer), 1):
            print(f'PLAN TRACE {index} {row}')
    except (OSError, ValueError, KeyError, TypeError, AttributeError) as exc:
        sys.exit(f'CELLS FAILED: {exc}')


if __name__ == '__main__':
    main()
