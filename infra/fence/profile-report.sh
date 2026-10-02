#!/usr/bin/env bash
# profile-report.sh — turns a gate run directory into what a human reads first:
# per failing Go package the path of its DIAGNOSIS.txt, a PROFILE block with the
# attribution of every slow or red step, profile.tsv for the ledger and
# profile/INDEX.txt naming every artifact of the run.
#
#   profile-report.sh failures <go-test.json> <profile-dir>
#   profile-report.sh summary <run-dir>
#
# failures: for each package with a `fail` action in the `go test -json` stream,
# one line per non-helper process directory <label>.<pid> under <profile-dir>
# (<label>: the import path under github.com/rezzminator/professor/pfm/, `/`
# replaced by `_`):
#     PROFILE <import path>: <dir>/<reason>/DIAGNOSIS.txt      (a dir with several
#         bundles lists them on its one line, ` · ` apart)
#     PROFILE <import path>: no bundle — <dir>/summary.json (event <e>, exit <c>)
#     PROFILE <import path>: NOT RECORDED — no <label>.* under <profile-dir>
# A green stream prints nothing. Exit 0; 64 bad usage; 2 the stream is unreadable
# (absent, or holding no test event at all — never an empty list read as green).
# A bundle the summary lists whose DIAGNOSIS.txt is gone, and a summary that
# cannot be read, are said in the line, never skipped.
#
# summary: reads <run-dir>/gate.tsv, steps/*.prof.tsv, resources.tsv,
# resources.err, profile/*/summary.json and profile/fixture-*/*/summary.json (each
# Go fixture step profiles into its own profile/fixture-<kind>/ root; INDEX.txt lists
# that root's process directories in its place); asks pfm/scripts/test-contention.sh
# (windows) once for every step window plus the whole file; writes
# <run-dir>/profile.tsv (one row per gate.tsv step, then GATE) and
# <run-dir>/profile/INDEX.txt; prints
#     PROFILE gate …                      the whole run
#     PROFILE step …                      every non-PASS step, then the 5
#                                         slowest PASS steps
#     PROFILE index <run-dir>/profile/INDEX.txt
# A step line carries its pointers: ` · xtrace` (ERR records), ` · hang` (the
# bound ended it), ` · profiles` (a Go step — pfm.unit, pfm.e2e, fixture.go-* —
# that is not PASS). A pointer to an artifact that is not there says
# MISSING. Exit 0; 64 bad usage; 2 no run directory or an unreadable gate.tsv;
# 71 an output cannot be written.
#
# Unmeasured cells are NA, never 0; a judge that cannot answer is
# `attribution not measured (judge failed: <line>)`. checks.sh calls `failures`
# after each Go step's report and `summary` once the gate's rows are in; this
# script never judges contention itself.
set -uo pipefail
export LC_ALL=C PYTHONUTF8=1

here=$(cd "$(dirname "$0")" && pwd) || { echo "profile-report: cannot resolve the script directory" >&2; exit 2; }
command -v python3 > /dev/null 2>&1 || { echo "profile-report: python3 is missing" >&2; exit 2; }
exec python3 - "$here/../../pfm" "$@" << 'PY'
import bisect
import datetime
import json
import os
import re
import statistics
import subprocess
import sys
import tempfile

PREFIX = 'github.com/rezzminator/professor/pfm/'
NUMBER = re.compile(r'[+-]?[0-9]+(\.[0-9]+)?')
REASON = re.compile(r'[A-Za-z0-9_-]+')
FIXTURE_ROOT = re.compile(r'fixture-[a-z0-9-]+')  # a Go fixture step's profile root, never a process
GO_STEPS = ('pfm.unit', 'pfm.e2e')
GO_STEP_PREFIX = 'fixture.go-'
SLOWEST_PASS = 5
JUDGE_SECONDS = 120
USAGE = (
    'usage: profile-report.sh failures <go-test.json> <profile-dir>\n'
    '       profile-report.sh summary <run-dir>'
)


def die(code, message):
    print(f'profile-report: {message}', file=sys.stderr)
    sys.exit(code)


def warn(message):
    print(f'profile-report: {message}', file=sys.stderr)


def usage_error(message):
    print(f'profile-report: {message}\n{USAGE}', file=sys.stderr)
    sys.exit(64)


def tidy(path):
    """A path as the caller wrote it, minus a trailing slash."""
    return path.rstrip('/') or path


def cell(text):
    """A numeric cell kept as written; anything else (NA …, UNAVAILABLE …) is NA."""
    return text if text is not None and NUMBER.fullmatch(text) else 'NA'


def fixed(value, places):
    return f'{value:.{places}f}' if isinstance(value, (int, float)) else 'NA'


def short(text, limit):
    return text if len(text) <= limit else text[: limit - 1] + '…'


# --- one Go process directory --------------------------------------------------
def process_info(directory):
    """The facts of a <label>.<pid> directory. summary is None with `problem`
    (NO SUMMARY or SUMMARY UNREADABLE) and `reason` when summary.json cannot be used."""
    info = {'dir': directory, 'summary': {}, 'problem': None, 'reason': '', 'helper': '', 'bundles': []}
    path = f'{directory}/summary.json'
    try:
        with open(path, encoding='utf-8') as handle:
            data = json.load(handle)
        if not isinstance(data, dict):
            raise ValueError('not a JSON object')
        info['summary'] = data
    except FileNotFoundError:
        info['problem'] = 'NO SUMMARY'
    except (OSError, UnicodeError, ValueError) as exc:
        info['problem'], info['reason'] = 'SUMMARY UNREADABLE', f'{path}: {exc}'
    helper = info['summary'].get('helper_of')
    info['helper'] = '' if helper is None else str(helper)
    listed = [b for b in (info['summary'].get('bundles') or []) if isinstance(b, str) and REASON.fullmatch(b)]
    try:
        on_disk = sorted(e for e in os.listdir(directory) if os.path.isfile(f'{directory}/{e}/DIAGNOSIS.txt'))
    except OSError as exc:
        on_disk = []
        warn(f'{directory} cannot be listed: {exc}')
    for reason in listed + [r for r in on_disk if r not in listed]:
        file = f'{directory}/{reason}/DIAGNOSIS.txt'
        info['bundles'].append((file, os.path.isfile(file), reason in listed))
    return info


def event_text(info):
    facts = info['summary']
    return f"event {facts.get('event', 'NA')}, exit {facts.get('exit_code', 'NA')}"


def process_names(profile_dir, pattern=None):
    """Sorted entries of profile_dir that are directories (and match pattern); the
    caller decides what an unreadable profile_dir means."""
    names = [n for n in os.listdir(profile_dir) if os.path.isdir(f'{profile_dir}/{n}') and (pattern is None or pattern.fullmatch(n))]
    return sorted(names, key=lambda n: (n.rpartition('.')[0], int(n.rpartition('.')[2]) if n.rpartition('.')[2].isdigit() else -1, n))


# --- failures ------------------------------------------------------------------
def read_failed_packages(stream):
    try:
        with open(stream, encoding='utf-8', errors='replace') as handle:
            text = handle.read()
    except OSError as exc:
        die(2, f'stream {stream}: {exc}')
    failed, events, unparsable = set(), 0, []
    for number, line in enumerate(text.splitlines(), 1):
        if not line.strip():
            continue
        try:
            event = json.loads(line)
        except ValueError:
            event = None
        if not isinstance(event, dict):
            unparsable.append(number)
            continue
        events += 1
        if event.get('Action') == 'fail' and event.get('Package'):
            failed.add(event['Package'])
    if events == 0:
        die(2, f'stream {stream}: no test event ({"empty" if not text.strip() else "no line is a JSON event"})')
    if unparsable:
        warn(f'stream {stream}: {len(unparsable)} line(s) are not JSON events (first: line {unparsable[0]})')
    return sorted(failed)


def label_of(package):
    if not package.startswith(PREFIX):
        return None
    return package[len(PREFIX):].replace('/', '_')


def failures(stream, profile_dir):
    profile_dir = tidy(profile_dir)
    for package in read_failed_packages(stream):
        label = label_of(package)
        if label is None:
            print(f'  PROFILE {package}: NOT RECORDED — no label: the import path is not under {PREFIX}')
            continue
        try:
            names = process_names(profile_dir, re.compile(re.escape(label) + r'\.[0-9]+'))
        except FileNotFoundError:
            names = []
        except OSError as exc:
            print(f'  PROFILE {package}: NOT RECORDED — {profile_dir} cannot be listed: {exc}')
            continue
        shown = 0
        for name in names:
            info = process_info(f'{profile_dir}/{name}')
            if info['reason']:
                warn(info['reason'])
            if info['helper']:
                continue
            shown += 1
            if info['bundles']:
                paths = [file if exists else f'{file} (listed in summary.json, file missing)' for file, exists, _ in info['bundles']]
                print(f'  PROFILE {package}: ' + ' · '.join(paths))
            elif info['problem']:
                print(f"  PROFILE {package}: no bundle — {info['dir']}/summary.json {info['problem']}")
            else:
                print(f"  PROFILE {package}: no bundle — {info['dir']}/summary.json ({event_text(info)})")
        if not names:
            print(f'  PROFILE {package}: NOT RECORDED — no {label}.* under {profile_dir}')
        elif shown == 0:
            print(f'  PROFILE {package}: NOT RECORDED — every {label}.* under {profile_dir} is a helper process')


# --- summary: inputs -----------------------------------------------------------
def read_gate(path):
    try:
        with open(path, encoding='utf-8') as handle:
            lines = handle.read().split('\n')
    except (OSError, UnicodeError) as exc:
        die(2, f'gate.tsv {path}: {exc}')
    if lines and lines[-1] == '':
        lines.pop()
    if not lines or lines[0].split('\t') != ['step', 'verdict', 'seconds']:
        die(2, f'gate.tsv {path}: header is not step, verdict, seconds: {(lines[0] if lines else "")[:120]!r}')
    steps, special = [], {}
    for number, line in enumerate(lines[1:], 2):
        if not line:
            continue
        cells = line.split('\t')
        if len(cells) != 3:
            die(2, f'gate.tsv {path}:{number}: {len(cells)} columns, expected 3: {line!r}')
        if cells[0] in ('STEPS', 'BUDGET'):
            special[cells[0]] = (cells[1], cells[2])
        else:
            steps.append((cells[0], cells[1], cells[2]))
    return steps, special


def read_prof(path):
    """(dict of key -> text, None) or (None, reason)."""
    try:
        with open(path, encoding='utf-8') as handle:
            text = handle.read()
    except (OSError, UnicodeError) as exc:
        return None, str(exc)
    pairs = {}
    for number, line in enumerate(text.split('\n'), 1):
        if not line:
            continue
        key, tab, value = line.partition('\t')
        if not tab or not key:
            return None, f'line {number} is not key<TAB>value'
        pairs.setdefault(key, value)
    if not pairs:
        return None, 'no key<TAB>value row'
    return pairs, None


def read_resources(path):
    """(rows, sample count, None) or (None, 0, reason) when the file is absent or empty.
    A row is {column: float|None}; a row whose epoch_s is not a number is dropped."""
    try:
        with open(path, encoding='utf-8') as handle:
            lines = handle.read().split('\n')
    except FileNotFoundError:
        return None, 0, 'NOT RECORDED'
    except (OSError, UnicodeError) as exc:
        return None, 0, f'UNREADABLE ({exc})'
    if lines and lines[-1] == '':
        lines.pop()
    if not lines:
        return None, 0, 'EMPTY'
    header = lines[0].split('\t')
    rows, samples = [], 0
    for line in lines[1:]:
        if not line:
            continue
        samples += 1
        values = {}
        for column, text in zip(header, line.split('\t')):
            values[column] = float(text) if NUMBER.fullmatch(text) else None
        if values.get('epoch_s') is not None:
            rows.append(values)
    return rows, samples, None


def points(rows, columns):
    """[(epoch_s, sum of columns)] over the rows where every column has a value."""
    found = []
    for row in rows:
        values = [row.get(column) for column in columns]
        if all(v is not None for v in values):
            found.append((row['epoch_s'], sum(values)))
    return found


def delta_between(rows, columns, low, high):
    """The counter's growth between two epochs, interpolated between the bracketing
    samples; an end outside the sampled range by more than the file's median
    cadence is unknown. None (NA) when unknown or the counter went backwards."""
    pts = points(rows, columns)
    if len(pts) < 2:
        return None
    gaps = [b[0] - a[0] for a, b in zip(pts, pts[1:])]
    cadence = statistics.median(gaps)

    def at(moment):
        if moment < pts[0][0]:
            return pts[0][1] if pts[0][0] - moment <= cadence else None
        if moment > pts[-1][0]:
            return pts[-1][1] if moment - pts[-1][0] <= cadence else None
        index = bisect.bisect_left([p[0] for p in pts], moment)
        if pts[index][0] == moment:
            return pts[index][1]
        (t0, v0), (t1, v1) = pts[index - 1], pts[index]
        return v0 + (v1 - v0) * (moment - t0) / (t1 - t0)

    start, end = at(low), at(high)
    if start is None or end is None or end < start:
        return None
    return end - start


def delta_whole(rows, columns):
    pts = points(rows, columns)
    if len(pts) < 2 or pts[-1][1] < pts[0][1]:
        return None
    return pts[-1][1] - pts[0][1]


def peak(rows, column):
    values = [row[column] for row in rows if row.get(column) is not None]
    return max(values) if values else None


# --- summary: the judge ----------------------------------------------------------
def ask_judge(judge, pfm_dir, resources, windows):
    """One windows call: [(WORD, evidence)] in order, or every window not measured
    with the reason the judge failed."""
    if not windows:
        return []

    def failed(reason):
        return [('not measured', f'judge failed: {reason}')] * len(windows)

    request = ''.join(f'{label}\t{low}\t{high}\n' for label, low, high in windows)
    try:
        # the judge reads its script from stdin, so the windows go in a file
        with tempfile.TemporaryDirectory(prefix='profile-report.') as scratch:
            request_file = f'{scratch}/windows.tsv'
            with open(request_file, 'w', encoding='utf-8') as handle:
                handle.write(request)
            command = ['bash', judge, 'windows', '--resources', resources, '--windows', request_file]
            done = subprocess.run(command, capture_output=True, text=True, timeout=JUDGE_SECONDS,
                                  env={**os.environ, 'PFM': pfm_dir}, check=False)
    except (OSError, subprocess.SubprocessError) as exc:
        return failed(f'{judge}: {exc}')
    if done.returncode != 0:
        lines = [line for line in done.stderr.splitlines() if line.strip()]
        return failed(lines[0] if lines else f'exit {done.returncode} with no message')
    if done.stderr.strip():
        sys.stderr.write(done.stderr)
    out = [line for line in done.stdout.split('\n') if line]
    if len(out) != len(windows):
        return failed(f'{len(out)} answers for {len(windows)} windows')
    answers = []
    for line in out:
        parts = line.split('\t', 2)
        if len(parts) != 3:
            return failed(f'unparsable answer {line[:120]!r}')
        answers.append((parts[1], parts[2]))
    return answers


# --- summary: pointers ---------------------------------------------------------------
def xtrace_pointer(run, name, prof):
    path = f'{run}/steps/{name}.xtrace'
    raw = prof.get('err_records', '0') if prof else '0'
    count = int(raw) if re.fullmatch(r'[0-9]+', raw) else None
    if count == 0:
        return ''
    label = f'{count} ERR' if count is not None else f'ERR count {cell(raw)}'
    try:
        with open(path, encoding='utf-8', errors='replace') as handle:
            first = next((line.rstrip('\n') for line in handle if line.startswith('ERR ')), None)
    except OSError as exc:
        return f' · xtrace {path} ({label}; first: UNREADABLE {exc})'
    if first is None:
        return f' · xtrace {path} ({label}; first: no ERR record in the file)'
    return f' · xtrace {path} ({label}; first: {short(first, 200)})'


def hang_pointer(run, name, prof, verdict):
    if not ((prof and prof.get('ended') == 'timeout') or verdict == 'TIMEOUT'):
        return ''
    path = f'{run}/steps/{name}.hang/tree.txt'
    return f' · hang {path}' + ('' if os.path.isfile(path) else ' (MISSING)')


def is_go_step(name):
    return name in GO_STEPS or name.startswith(GO_STEP_PREFIX)


# --- summary ---------------------------------------------------------------------
def gate_verdict(steps_verdict, budget_verdict):
    if steps_verdict == 'FAIL' or budget_verdict in ('FAIL', 'ERROR'):
        return 'FAIL'
    if steps_verdict is None:
        return 'NA'
    if budget_verdict == 'WARN':
        return 'WARN'
    return 'PASS'


def seconds_text(text):
    return 'NA' if cell(text) == 'NA' else f'{float(text):.1f}s'


def summary(run, judge, pfm_dir):
    run = tidy(run)
    if not os.path.isdir(run):
        die(2, f'run directory {run} is not a directory')
    steps, special = read_gate(f'{run}/gate.tsv')
    if 'STEPS' not in special:
        warn(f'gate.tsv {run}/gate.tsv has no STEPS row: the GATE verdict and wall are NA')
    steps_verdict, steps_wall = special.get('STEPS', (None, 'NA'))
    budget_verdict = special.get('BUDGET', (None, None))[0]

    profs = {}
    steps_dir = f'{run}/steps'
    try:
        prof_files = sorted(f for f in os.listdir(steps_dir) if f.endswith('.prof.tsv'))
    except FileNotFoundError:
        prof_files = []
    except OSError as exc:
        warn(f'{steps_dir} cannot be listed: {exc}')
        prof_files = []
    for file in prof_files:
        name = file[: -len('.prof.tsv')]
        data, reason = read_prof(f'{steps_dir}/{file}')
        if reason:
            warn(f'{steps_dir}/{file}: {reason}')
        profs[name] = (data, reason)

    resources_path = f'{run}/resources.tsv'
    rows, samples, resources_problem = read_resources(resources_path)
    rows = rows or []

    # windows for the judge: every step with a numeric window, then the whole file
    windows, window_of = [], {}
    no_window = {}
    for name, verdict, _ in steps:
        data = profs.get(name, (None, None))[0]
        if data is None:
            no_window[name] = 'step did not run' if verdict == 'NOT-RUN' else (
                f'no window: {steps_dir}/{name}.prof.tsv unreadable' if name in profs else f'no window: {steps_dir}/{name}.prof.tsv absent')
            continue
        low, high = (data.get(k, '').replace(',', '.') for k in ('start_epoch', 'finish_epoch'))
        if not (NUMBER.fullmatch(low) and NUMBER.fullmatch(high) and float(high) >= float(low)):
            no_window[name] = 'no window: start_epoch and finish_epoch are not a window in ' + f'{steps_dir}/{name}.prof.tsv'
            continue
        window_of[name] = (low, high)
        windows.append((name, low, high))
    windows.append(('GATE', '0', '99999999999'))  # every epoch: the whole file
    answers = ask_judge(judge, pfm_dir, resources_path, windows)
    verdict_of = {label: answers[i] for i, (label, _, _) in enumerate(windows[:-1])}
    gate_answer = answers[-1]

    # profile.tsv rows
    table, by_name = [], {}
    for name, verdict, wall in steps:
        data = profs.get(name, (None, None))[0]
        window = window_of.get(name)
        io = delta_between(rows, ('cg_io_read_mb', 'cg_io_write_mb'), float(window[0]), float(window[1])) if window else None
        word, evidence = verdict_of[name] if name in verdict_of else ('not measured', no_window.get(name, 'no window'))
        row = {
            'step': name, 'verdict': verdict, 'wall_s': cell(wall),
            'cpu_s': cell(data.get('cpu_s')) if data else 'NA',
            'io_mb': fixed(io, 3),
            'psi_cpu_s': cell(data.get('psi_cpu_some_s')) if data else 'NA',
            'psi_io_s': cell(data.get('psi_io_some_s')) if data else 'NA',
            'psi_mem_s': cell(data.get('psi_memory_some_s')) if data else 'NA',
            'attribution': word, 'evidence': evidence,
        }
        table.append(row)
        by_name[name] = row
    gate_row = {
        'step': 'GATE', 'verdict': gate_verdict(steps_verdict, budget_verdict), 'wall_s': cell(steps_wall),
        'cpu_s': fixed(delta_whole(rows, ('cg_cpu_s',)), 3),
        'io_mb': fixed(delta_whole(rows, ('cg_io_read_mb', 'cg_io_write_mb')), 3),
        'psi_cpu_s': fixed(delta_whole(rows, ('vm_psi_cpu_some_s',)), 3),
        'psi_io_s': fixed(delta_whole(rows, ('vm_psi_io_some_s',)), 3),
        'psi_mem_s': fixed(delta_whole(rows, ('vm_psi_mem_some_s',)), 3),
        'attribution': gate_answer[0], 'evidence': gate_answer[1],
    }
    table.append(gate_row)

    index_path = f'{run}/profile/INDEX.txt'
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')
    index = [f'# profile index {run} · {stamp}', '']
    index += go_process_section(run)
    index += step_section(run, steps, profs)
    index += resource_section(run, resources_path, samples, resources_problem)

    columns = ['step', 'verdict', 'wall_s', 'cpu_s', 'io_mb', 'psi_cpu_s', 'psi_io_s', 'psi_mem_s', 'attribution', 'evidence']
    write(f'{run}/profile.tsv', '\t'.join(columns) + '\n' + ''.join('\t'.join(r[c] for c in columns) + '\n' for r in table))
    write(index_path, '\n'.join(index) + '\n')

    print_block(run, steps, profs, by_name, steps_verdict, steps_wall, rows, gate_row, index_path)


def write(path, text):
    try:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, 'w', encoding='utf-8') as handle:
            handle.write(text)
    except OSError as exc:
        die(71, f'cannot write {path}: {exc}')


def process_line(directory):
    info = process_info(directory)
    if info['problem']:
        if info['reason']:
            warn(info['reason'])
        return f"{info['dir']} {info['problem']}"
    facts = info['summary']
    line = (f"{info['dir']} · event {facts.get('event', 'NA')} · exit {facts.get('exit_code', 'NA')}"
            f" · wall {fixed(facts.get('wall_s'), 1)}s · run delay {fixed(facts.get('run_delay_s'), 3)}s")
    if info['helper']:
        line += f" · helper of {info['helper']}"
    for file, exists, _ in info['bundles']:
        line += f' → {file}' + ('' if exists else ' (MISSING)')
    return line


def go_process_section(run):
    profile_dir = f'{run}/profile'
    try:
        names = process_names(profile_dir)
        problem = None
    except FileNotFoundError:
        names, problem = [], f'{profile_dir} NOT RECORDED'
    except OSError as exc:
        names, problem = [], f'{profile_dir} UNREADABLE ({exc})'
    count, lines = 0, []
    for name in names:
        path = f'{profile_dir}/{name}'
        if not FIXTURE_ROOT.fullmatch(name):
            lines.append(process_line(path))
            count += 1
            continue
        # A Go fixture step's own profile root: its process directories take its place here.
        try:
            inner = process_names(path)
        except OSError as exc:
            lines.append(f'{path} UNREADABLE ({exc})')
            continue
        if not inner:
            lines.append(f'{path} none recorded — no <label>.<pid> directory under {path}')
        for process in inner:
            lines.append(process_line(f'{path}/{process}'))
            count += 1
    lines.insert(0, f'## Go test processes ({count})')
    if problem:
        lines.append(problem)
    elif not names:
        lines.append(f'none recorded — no <label>.<pid> directory under {profile_dir}')
    return lines + ['']


def step_section(run, steps, profs):
    order = {name: i for i, (name, _, _) in enumerate(steps)}
    gate = {name: (verdict, wall) for name, verdict, wall in steps}
    names = sorted(profs, key=lambda n: (order.get(n, len(order)), n))
    lines = [f'## Steps ({len(names)})']
    for name in names:
        data = profs[name][0]
        if data is None:
            lines.append(f'{name} PROF UNREADABLE')
            continue
        verdict, wall = gate.get(name, ('NA', cell(data.get('wall_s'))))
        cpu = cell(data.get('cpu_s'))
        lines.append(f"{name} · {verdict} · ended {data.get('ended', 'NA')} · rc {data.get('rc', 'NA')}"
                     f' · wall {seconds_text(wall)} · cpu {seconds_text(cpu)}'
                     + hang_pointer(run, name, data, verdict) + xtrace_pointer(run, name, data))
    return lines + ['']


def resource_section(run, path, samples, problem):
    lines = ['## Resources']
    lines.append(f'{path} {problem}' if problem else f'{path} · {samples} samples')
    err = f'{run}/resources.err'
    try:
        with open(err, encoding='utf-8') as handle:
            entries = [line for line in handle.read().split('\n') if line]
    except FileNotFoundError:
        entries = []
    except (OSError, UnicodeError) as exc:
        lines.append(f'{err} UNREADABLE ({exc})')
        entries = []
    for entry in entries:
        column, tab, reason = entry.partition('\t')
        lines.append(f'{column} NA — {reason}' if tab else f'{entry} (no reason recorded in {err})')
    return lines


def print_block(run, steps, profs, by_name, steps_verdict, steps_wall, rows, gate_row, index_path):
    def a_line(row):
        return f"attribution {row['attribution']} ({row['evidence']})"

    gate = (f"PROFILE gate {steps_verdict or 'NA'} {seconds_text(steps_wall)}"
            f" · cpu {seconds_text(gate_row['cpu_s'])}"
            f" · io {fixed(delta_whole(rows, ('cg_io_read_mb',)), 1)}/{fixed(delta_whole(rows, ('cg_io_write_mb',)), 1)} MB"
            f" · mem peak {fixed(peak(rows, 'cg_mem_peak_mb'), 1)} MB · swap {fixed(peak(rows, 'cg_swap_mb'), 1)} MB"
            f' · {a_line(gate_row)}')
    print(gate)
    shown = [name for name, verdict, _ in steps if verdict != 'PASS']
    passed = [(name, wall) for name, verdict, wall in steps if verdict == 'PASS']
    passed.sort(key=lambda item: -(float(item[1]) if cell(item[1]) != 'NA' else -1.0))
    shown += [name for name, _ in passed[:SLOWEST_PASS]]
    verdicts = {name: verdict for name, verdict, _ in steps}
    for name in shown:
        row = by_name[name]
        data = profs.get(name, (None, None))[0]
        psi = '/'.join(fixed(None if row[c] == 'NA' else float(row[c]), 2) for c in ('psi_cpu_s', 'psi_io_s', 'psi_mem_s'))
        line = (f"PROFILE step {name} {row['verdict']} {seconds_text(row['wall_s'])}"
                f" · cpu {seconds_text(row['cpu_s'])} · psi cpu/io/mem {psi}s · {a_line(row)}")
        line += xtrace_pointer(run, name, data) if data else ''
        line += hang_pointer(run, name, data, verdicts[name])
        if is_go_step(name) and verdicts[name] != 'PASS':
            line += f' · profiles {index_path}'
        print(line)
    print(f'PROFILE index {index_path}')


def main():
    args = sys.argv[2:]
    if not args:
        usage_error('a subcommand (failures|summary) is required')
    command, rest = args[0], args[1:]
    if command in ('-h', '--help', 'help'):
        print(USAGE)
        return
    if command == 'failures':
        if len(rest) != 2 or any(not part for part in rest):
            usage_error('failures needs <go-test.json> <profile-dir>')
        failures(rest[0], rest[1])
    elif command == 'summary':
        if len(rest) != 1 or not rest[0]:
            usage_error('summary needs <run-dir>')
        pfm_dir = os.path.abspath(sys.argv[1])
        summary(rest[0], os.path.join(pfm_dir, 'scripts', 'test-contention.sh'), pfm_dir)
    else:
        usage_error(f'unknown subcommand: {command}')


main()
PY
