#!/usr/bin/env bash
# pfm test contention judge — says, for any time window of a gate run, whether
# the slowness was the machine (CONTENTION), the code (CODE), or could not be
# told (not measured). It reads the gate-wide resources.tsv that
# infra/fence/sampler.sh records and the thresholds in pfm/.testcontention.yml;
# it runs nothing and writes nothing.
#
#   test-contention.sh window --resources R.tsv [--from E] [--to E]
#                      [--run-delay S --cpu S] [--yml F]
#       prints one line `<WORD><TAB><evidence>`. No --from/--to = the whole file.
#       --run-delay (a process's runnable-but-waiting seconds) and --cpu (its CPU
#       seconds) come together or not at all.
#   test-contention.sh windows --resources R.tsv --windows W.tsv [--yml F]
#       W.tsv rows `label<TAB>from<TAB>to[<TAB>run_delay_s<TAB>cpu_s]`, the last
#       two both numbers or both `NA`; prints `label<TAB><WORD><TAB><evidence>`
#       per row, same order. Every row is checked before the first is printed.
#
# Signals over the rows whose epoch_s lies in [from, to] (each needs at least
# min_samples rows with a value, else `NA (<n> samples < <min>)`):
#   tick-deficit  (Δvm_busy_s − Δcg_cpu_s) ÷ (Δepoch_s × cpus): the cgroup counts
#                 vCPU time the host withheld, /proc/stat ticks do not, so a
#                 negative value is the host starving the VM. Crosses at ≤
#                 tick_deficit_max. (Steal time is blind in this VM: unused.)
#   cpu-psi       Δcg_psi_cpu_some_s ÷ Δepoch_s: runnable work waiting in the
#                 VM. Crosses at ≥ cpu_psi_share_min.
#   spin          window median spin_us ÷ the least spin_us seen in this file and
#                 the 20 newest run.*/resources.tsv beside it in the same timing
#                 base (this file alone when none). Crosses at ≥ spin_ratio_min.
#   run-delay     run_delay_s ÷ cpu_s. Crosses at ≥ run_delay_ratio_min.
# Verdict: CONTENTION when any measured signal crosses; CODE when one is
# measured and none crosses; `not measured` when none is. The evidence names
# every signal with its value and its line. A missing or empty resources file is
# `not measured`, its reason in the evidence, exit 0.
#
# What THIS script reports when it is itself broken: bad usage, an unreadable or
# incomplete thresholds file, a malformed resources.tsv or windows file is exit 2
# with one `test-contention:` line on stderr naming the file (and line) — never a
# verdict. An unreadable sibling is skipped with a stderr line, never silently.
set -uo pipefail
export LC_ALL=C PYTHONUTF8=1

PFM="${PFM:-$(cd "$(dirname "$0")/.." && pwd)}"
command -v python3 > /dev/null 2>&1 || { echo "test-contention: python3 is missing" >&2; exit 2; }
exec python3 - "$PFM/.testcontention.yml" "$@" << 'PY'
import os
import re
import statistics
import sys
from pathlib import Path

HEADER = (
    'epoch_s uptime_s cg_cpu_s vm_busy_s cpus cg_io_read_mb cg_io_write_mb cg_mem_mb '
    'cg_mem_peak_mb cg_swap_mb cg_psi_cpu_some_s cg_psi_io_some_s cg_psi_io_full_s '
    'cg_psi_mem_some_s cg_psi_mem_full_s vm_psi_cpu_some_s vm_psi_io_some_s '
    'vm_psi_io_full_s vm_psi_mem_some_s vm_psi_mem_full_s spin_us load1'
).split()
KEYS = ('tick_deficit_max', 'cpu_psi_share_min', 'spin_ratio_min', 'run_delay_ratio_min', 'min_samples')
SIBLINGS = 20
NUMBER = re.compile(r'[+-]?[0-9]+(\.[0-9]+)?')
USAGE = (
    'usage: test-contention.sh window --resources R.tsv [--from E] [--to E] [--run-delay S --cpu S] [--yml F]\n'
    '       test-contention.sh windows --resources R.tsv --windows W.tsv [--yml F]'
)


def fail(message):
    print(f'test-contention: {message}', file=sys.stderr)
    sys.exit(2)


def number(text, what):
    if not NUMBER.fullmatch(text):
        fail(f'{what} is not a number: {text!r}')
    return float(text)


def parse_args(default_yml, argv):
    if not argv or argv[0] not in ('window', 'windows'):
        fail(f'a subcommand (window|windows) is required\n{USAGE}')
    mode = argv[0]
    allowed = {'--resources', '--yml'} | ({'--from', '--to', '--run-delay', '--cpu'} if mode == 'window' else {'--windows'})
    given = {}
    rest = argv[1:]
    while rest:
        flag = rest.pop(0)
        if flag not in allowed:
            fail(f'{mode}: unknown argument {flag!r}\n{USAGE}')
        if flag in given:
            fail(f'{mode}: {flag} given twice')
        if not rest:
            fail(f'{mode}: {flag} needs a value')
        given[flag] = rest.pop(0)
    required = ('--resources',) if mode == 'window' else ('--resources', '--windows')
    for flag in required:
        if flag not in given:
            fail(f'{mode}: {flag} is required\n{USAGE}')
    if ('--run-delay' in given) != ('--cpu' in given):
        fail('window: --run-delay and --cpu come together or not at all')
    given.setdefault('--yml', default_yml)
    return mode, given


def load_thresholds(path):
    try:
        text = Path(path).read_text(encoding='utf-8')
    except (OSError, UnicodeError) as exc:
        fail(f'thresholds {path}: {exc}')
    found = {}
    for lineno, raw in enumerate(text.splitlines(), 1):
        line = raw.split('#', 1)[0].strip()
        if not line:
            continue
        match = re.fullmatch(r'([a-z_]+):\s*(\S+)', line)
        if not match:
            fail(f'thresholds {path}:{lineno}: not a "key: value" line: {raw!r}')
        key, value = match.groups()
        if key in found:
            fail(f'thresholds {path}:{lineno}: key {key} given twice')
        found[key] = (value, lineno)
    limits = {}
    for key in KEYS:
        if key not in found:
            fail(f'thresholds {path}: missing key {key}')
        value, lineno = found[key]
        if not NUMBER.fullmatch(value):
            fail(f'thresholds {path}:{lineno}: key {key} is not a number: {value!r}')
        limits[key] = float(value)
    if limits['tick_deficit_max'] > 0:
        fail(f"thresholds {path}: key tick_deficit_max must not be positive (a deficit is negative): {limits['tick_deficit_max']:g}")
    for key in KEYS[1:4]:
        if limits[key] <= 0:
            fail(f'thresholds {path}: key {key} must be positive: {limits[key]:g}')
    if limits['min_samples'] < 1 or limits['min_samples'] != int(limits['min_samples']):
        fail(f"thresholds {path}: key min_samples must be a whole number of at least 1: {limits['min_samples']:g}")
    limits['min_samples'] = int(limits['min_samples'])
    return limits


def read_rows(path):
    """Rows of a resources.tsv as {column: float|None}; exit 2 on a malformed file."""
    try:
        lines = Path(path).read_text(encoding='utf-8').split('\n')
    except (OSError, UnicodeError) as exc:
        fail(f'resources {path}: {exc}')
    if lines and lines[-1] == '':
        lines.pop()
    if not lines or lines[0].split('\t') != HEADER:
        fail(f'resources {path}: header differs from the resources.tsv contract: {(lines[0] if lines else "")[:120]!r}')
    rows = []
    for lineno, line in enumerate(lines[1:], 2):
        cells = line.split('\t')
        if len(cells) != len(HEADER):
            fail(f'resources {path}:{lineno}: {len(cells)} columns, expected {len(HEADER)}')
        row = {}
        for column, cell in zip(HEADER, cells):
            if cell == 'NA':
                row[column] = None
            elif NUMBER.fullmatch(cell):
                row[column] = float(cell)
            else:
                fail(f'resources {path}:{lineno}: column {column} is neither a number nor NA: {cell!r}')
        if row['epoch_s'] is None:
            fail(f'resources {path}:{lineno}: epoch_s is NA')
        rows.append(row)
    return rows


def sibling_spin_min(path):
    """The least spin_us of a sibling resources.tsv, or None when it has none; a bad sibling is skipped, loudly."""
    try:
        lines = Path(path).read_text(encoding='utf-8').split('\n')
        if not lines or lines[0].split('\t') != HEADER:
            raise ValueError('header differs from the resources.tsv contract')
        index = HEADER.index('spin_us')
        values = []
        for lineno, line in enumerate(lines[1:], 2):
            if not line:
                continue
            cells = line.split('\t')
            if len(cells) != len(HEADER):
                raise ValueError(f'line {lineno}: {len(cells)} columns, expected {len(HEADER)}')
            if cells[index] != 'NA':
                if not NUMBER.fullmatch(cells[index]):
                    raise ValueError(f'line {lineno}: spin_us is neither a number nor NA: {cells[index]!r}')
                values.append(float(cells[index]))
        return min(values) if values else None
    except (OSError, UnicodeError, ValueError) as exc:
        print(f'test-contention: sibling {path} skipped: {exc}', file=sys.stderr)
        return None


def spin_reference(resources, rows):
    """(reference µs, source text, sorted None when there is no spin value anywhere)."""
    own = [row['spin_us'] for row in rows if row['spin_us'] is not None]
    candidates = [('this file', min(own))] if own else []
    here = Path(resources).resolve()
    base = here.parent.parent
    found = []
    try:
        for sibling in base.glob('run.*/resources.tsv'):
            if sibling.resolve() != here:
                found.append((sibling.stat().st_mtime, str(sibling), sibling))
    except OSError as exc:
        print(f'test-contention: siblings of {resources} not listed: {exc}', file=sys.stderr)
    found.sort(reverse=True)
    for _, _, sibling in found[:SIBLINGS]:
        least = sibling_spin_min(sibling)
        if least is not None:
            candidates.append((sibling.parent.name, least))
    if not candidates:
        return None, None
    name, value = min(candidates, key=lambda item: item[1])
    source = name if len(candidates) == 1 else f'{name} (min of {len(candidates)} files)'
    return value, source


def percent(value, signed=True):
    scaled = round(value * 100, 1)
    scaled = 0.0 if scaled == 0 else scaled
    return f'{scaled:+.1f}%' if signed else f'{scaled:.1f}%'


def percent_line(value):
    return f'{value * 100:g}%'


def window_samples(inside, columns, minimum):
    usable = [row for row in inside if all(row[column] is not None for column in columns)]
    if len(usable) >= minimum:
        return usable, None
    reason = f'{len(usable)} samples < {minimum}'
    if len(inside) >= minimum:
        reason += f', {" and ".join(columns)} NA in {len(inside) - len(usable)} of {len(inside)} rows'
    return None, reason


def signal_tick_deficit(inside, limits):
    usable, reason = window_samples(inside, ('vm_busy_s', 'cg_cpu_s', 'cpus'), limits['min_samples'])
    if usable is None:
        return 'tick-deficit NA (%s)' % reason, None
    first, last = usable[0], usable[-1]
    span = last['epoch_s'] - first['epoch_s']
    d_vm = last['vm_busy_s'] - first['vm_busy_s']
    d_cg = last['cg_cpu_s'] - first['cg_cpu_s']
    if span <= 0 or last['cpus'] <= 0:
        return 'tick-deficit NA (window spans no time or cpus is 0)', None
    if d_vm < 0 or d_cg < 0:
        return 'tick-deficit NA (a cpu counter went backwards)', None
    value = (d_vm - d_cg) / (span * last['cpus'])
    crossed = value <= limits['tick_deficit_max']
    line = ('≤' if crossed else '>') + ' ' + percent_line(limits['tick_deficit_max'])
    return f'tick-deficit {percent(value)} ({line})', crossed


def signal_cpu_psi(inside, limits):
    usable, reason = window_samples(inside, ('cg_psi_cpu_some_s',), limits['min_samples'])
    if usable is None:
        return 'cpu-psi NA (%s)' % reason, None
    first, last = usable[0], usable[-1]
    span = last['epoch_s'] - first['epoch_s']
    stall = last['cg_psi_cpu_some_s'] - first['cg_psi_cpu_some_s']
    if span <= 0:
        return 'cpu-psi NA (window spans no time)', None
    if stall < 0:
        return 'cpu-psi NA (the pressure counter went backwards)', None
    value = stall / span
    crossed = value >= limits['cpu_psi_share_min']
    line = ('≥' if crossed else '<') + ' ' + percent_line(limits['cpu_psi_share_min'])
    return f'cpu-psi {percent(value)} ({line})', crossed


def signal_spin(inside, limits, reference):
    usable, reason = window_samples(inside, ('spin_us',), limits['min_samples'])
    if usable is None:
        return 'spin NA (%s)' % reason, None
    if reference[0] is None or reference[0] <= 0:
        return 'spin NA (reference spin_us is %s)' % ('absent' if reference[0] is None else '0'), None
    value = statistics.median(row['spin_us'] for row in usable) / reference[0]
    crossed = value >= limits['spin_ratio_min']
    line = ('≥' if crossed else '<') + f" ×{limits['spin_ratio_min']:g}"
    return f'spin ×{value:.1f} ({line}; ref {reference[0]:g}µs from {reference[1]})', crossed


def signal_run_delay(delay, cpu, limits):
    if delay is None:
        return 'run-delay NA (not given)', None
    if cpu <= 0:
        return 'run-delay NA (cpu is 0 s)', None
    value = delay / cpu
    crossed = value >= limits['run_delay_ratio_min']
    line = ('≥' if crossed else '<') + f" {limits['run_delay_ratio_min']:g}"
    return f'run-delay {value:.2f} ({line})', crossed


def judge(rows, limits, reference, lo, hi, delay, cpu):
    inside = [row for row in rows if lo <= row['epoch_s'] <= hi]
    results = [
        signal_tick_deficit(inside, limits),
        signal_cpu_psi(inside, limits),
        signal_spin(inside, limits, reference),
        signal_run_delay(delay, cpu, limits),
    ]
    measured = [crossed for _, crossed in results if crossed is not None]
    if any(measured):
        word = 'CONTENTION'
    elif measured:
        word = 'CODE'
    else:
        word = 'not measured'
    return word, ' · '.join(text for text, _ in results)


def read_windows(path):
    try:
        lines = Path(path).read_text(encoding='utf-8').split('\n')
    except (OSError, UnicodeError) as exc:
        fail(f'windows {path}: {exc}')
    windows = []
    for lineno, line in enumerate(lines, 1):
        if line == '':
            continue
        cells = line.split('\t')
        where = f'windows {path}:{lineno}'
        if len(cells) not in (3, 5):
            fail(f'{where}: {len(cells)} columns, expected label, from, to and optionally run_delay_s, cpu_s: {line!r}')
        if not cells[0]:
            fail(f'{where}: the label is empty')
        lo, hi = number(cells[1], f'{where}: from'), number(cells[2], f'{where}: to')
        if hi < lo:
            fail(f'{where}: to {cells[2]} is before from {cells[1]}')
        delay = cpu = None
        if len(cells) == 5 and (cells[3] not in ('', 'NA') or cells[4] not in ('', 'NA')):
            if cells[3] in ('', 'NA') or cells[4] in ('', 'NA'):
                fail(f'{where}: run_delay_s and cpu_s come together or not at all: {line!r}')
            delay, cpu = number(cells[3], f'{where}: run_delay_s'), number(cells[4], f'{where}: cpu_s')
            if delay < 0 or cpu < 0:
                fail(f'{where}: run_delay_s and cpu_s must not be negative: {line!r}')
        windows.append((cells[0], lo, hi, delay, cpu))
    return windows


def main():
    mode, given = parse_args(sys.argv[1], sys.argv[2:])
    limits = load_thresholds(given['--yml'])
    inf = float('inf')
    if mode == 'window':
        lo = number(given['--from'], '--from') if '--from' in given else -inf
        hi = number(given['--to'], '--to') if '--to' in given else inf
        if hi < lo:
            fail(f'window: --to {given["--to"]} is before --from {given["--from"]}')
        delay = cpu = None
        if '--run-delay' in given:
            delay, cpu = number(given['--run-delay'], '--run-delay'), number(given['--cpu'], '--cpu')
            if delay < 0 or cpu < 0:
                fail('window: --run-delay and --cpu must not be negative')
        windows = [(None, lo, hi, delay, cpu)]
    else:
        windows = read_windows(given['--windows'])
    resources = given['--resources']
    if not os.path.exists(resources):
        verdicts = [('not measured', f'{resources} absent')] * len(windows)
    elif os.path.getsize(resources) == 0:
        verdicts = [('not measured', f'{resources} empty')] * len(windows)
    else:
        rows = read_rows(resources)
        reference = spin_reference(resources, rows)
        verdicts = [judge(rows, limits, reference, lo, hi, delay, cpu) for _, lo, hi, delay, cpu in windows]
    for (label, *_), (word, evidence) in zip(windows, verdicts):
        print(f'{word}\t{evidence}' if label is None else f'{label}\t{word}\t{evidence}')


main()
PY
