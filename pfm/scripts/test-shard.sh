#!/usr/bin/env bash
# Split slow Go packages across processes and present one go test JSON stream.
set -euo pipefail
export PFM_SHARD_MODULE="$(cd "$(dirname "$0")/.." && pwd)"
exec python3 - "$@" <<'PY'
import argparse
import json
import os
import re
import statistics
import subprocess
import sys
import tempfile
from collections import defaultdict
from pathlib import Path

MODULE = Path(os.environ['PFM_SHARD_MODULE'])
PREFIX = 'github.com/rezzminator/professor/pfm/'
# Clean plain go test ./... took 75.7 s: cmd/pfm took 55.7 s; no other package exceeded 22 s.
# History-run medians: three shards 43.8 s, five 43.3 s;
# four measured 39.2 s then 43.7 s under varying host load.
SHARDS = {
    PREFIX + 'cmd/pfm': 4,
}
TERMINALS = {'pass', 'fail', 'skip'}


def error(message, code=2):
    print('test-shard: ' + message, file=sys.stderr)
    raise SystemExit(code)


def output_ready(path):
    parent = Path(path).parent
    try:
        with tempfile.NamedTemporaryFile(dir=parent):
            pass
    except OSError as exc:
        error(f'--out {path} is not writable: {exc}')


def history_tests(path):
    if not path:
        return None
    try:
        with open(path, encoding='utf-8') as stream:
            events = [json.loads(line) for line in stream if line.strip()]
    except (OSError, ValueError):
        return None
    result = defaultdict(dict)
    for event in events:
        name = event.get('Test', '')
        if event.get('Action') in TERMINALS and name and '/' not in name and isinstance(event.get('Elapsed'), (float, int)):
            result[event.get('Package', '')][name] = float(event['Elapsed'])
    return result if any(result.values()) else None


def package_list():
    proc = subprocess.run(['go', 'list', './...'], cwd=MODULE, text=True, capture_output=True)
    if proc.returncode:
        error(f'go list package ./... failed: {proc.stderr.strip() or proc.stdout.strip()}')
    packages = [line.strip() for line in proc.stdout.splitlines() if line.strip()]
    if not packages:
        error('go list package ./... returned no packages')
    return packages


def shard_counts(packages):
    return {package: SHARDS.get(package, 1) for package in packages}


def package_dir(package):
    return MODULE / package.removeprefix(PREFIX)


def list_tests(package, binary, env=None):
    proc = subprocess.run([binary, '-test.list', '.*'], cwd=package_dir(package), env=env, text=True, capture_output=True)
    if proc.returncode:
        error(f'listing package {package} failed: {proc.stderr.strip() or proc.stdout.strip()}')
    names = []
    for line in proc.stdout.splitlines():
        name = line.strip()
        if re.fullmatch(r'(?:Test|Fuzz|Example)[A-Za-z0-9_]*', name):
            names.append(name)
    return names


def split_flags(flags):
    values = {'-p', '-parallel', '-timeout', '-cpu', '-tags', '-count'}
    booleans = {'-short', '-failfast', '-race'}
    accepted = values | booleans
    build = []
    binary = []
    index = 0
    while index < len(flags):
        flag = flags[index]
        name, equals, value = flag.partition('=')
        if name not in accepted:
            error(f'unsupported go test flag {flag}')
        if name in values and not equals:
            if index + 1 >= len(flags) or flags[index + 1].startswith('-'):
                error(f'go test flag {name} needs a value')
            index += 1
            value = flags[index]
        elif name in booleans and not equals and index + 1 < len(flags) and flags[index + 1] in ('true', 'false'):
            index += 1
            value = flags[index]
        elif name in booleans and not equals:
            value = None
        if name == '-race':
            build.append(name + (('=' + value) if value is not None else ''))
        elif name == '-tags':
            build.extend([name, value])
        if name in ('-parallel', '-short', '-failfast', '-cpu'):
            binary.append('-test.' + name[1:] + (('=' + value) if value is not None else ''))
        index += 1
    return build, binary


def assignments(names, count, weights):
    shards = [[] for _ in range(count)]
    costs = [0.0] * count
    median = statistics.median(weights.values()) if weights else 1.0
    if weights:
        order = sorted(enumerate(names), key=lambda item: (-weights.get(item[1], median), item[0]))
    else:
        order = list(enumerate(names))
    for index, name in order:
        weight = weights.get(name, median)
        shard = min(range(count), key=lambda n: (costs[n], n)) if weights else index % count
        shards[shard].append(name)
        costs[shard] += weight
    return shards, costs


def read_stream(path, expected_package=None):
    events = []
    builds = []
    try:
        with open(path, 'rb') as stream:
            for raw in stream:
                if not raw.strip():
                    continue
                event = json.loads(raw)
                package = event.get('Package')
                if not package and event.get('ImportPath') and str(event.get('Action', '')).startswith('build-'):
                    # A compile failure is reported by ImportPath, never Package.
                    builds.append((raw, event))
                    continue
                if not package or (expected_package and package != expected_package):
                    raise ValueError(f'unexpected package {package!r}')
                events.append((raw, event))
    except (OSError, UnicodeError, ValueError) as exc:
        error(f'unreadable shard stream {path}: {exc}', 1)
    return events, builds


def merged_streams(streams, out, expected=None):
    # streams: (path, package-or-None), in the desired output order.
    groups = defaultdict(list)
    order = []
    ordered_entries = []
    build_entries = {}
    for path, package_hint in streams:
        entries, builds = read_stream(path, package_hint)
        ordered_entries.append(entries)
        # Keep older go test -json build events once when merging captured streams.
        build_entries.update((raw, event) for raw, event in builds if raw not in build_entries)
        packages = {entry.get('Package') for _, entry in entries}
        if package_hint:
            packages.add(package_hint)
        if not packages:
            error(f'shard stream {path} has no package events', 1)
        for package in packages:
            if package not in groups:
                order.append(package)
            groups[package].append([(raw, event) for raw, event in entries if event.get('Package') == package])

    sharded = {package for package, pieces in groups.items() if len(pieces) > 1}
    if expected:
        sharded.update(expected)
    failed = False
    with open(out, 'wb') as output:
        for raw, event in build_entries.items():
            output.write(raw)
            failed |= event.get('Action') == 'build-fail'
        # Keep the unsharded process's original interleaving and bytes.
        for entries in ordered_entries:
            for raw, event in entries:
                if event['Package'] not in sharded:
                    output.write(raw)
        for package in order:
            pieces = groups[package]
            if package not in sharded:
                terminals = [event for _, event in pieces[0] if not event.get('Test') and event.get('Action') in TERMINALS]
                if len(terminals) != 1 or terminals[0]['Action'] == 'fail' or any(event.get('Action') == 'fail' for _, event in pieces[0]):
                    failed = True
                continue

            terminals = []
            latest_time = ''
            ran = set()
            shard_failed = False
            for piece in pieces:
                shard_terminals = []
                for raw, event in piece:
                    latest_time = max(latest_time, event.get('Time', ''))
                    if event.get('Test') and event.get('Action') == 'run':
                        ran.add(event['Test'].split('/')[0])
                    if event.get('Action') == 'fail':
                        shard_failed = True
                    if not event.get('Test') and event.get('Action') in TERMINALS:
                        shard_terminals.append(event)
                    else:
                        output.write(raw)
                if len(shard_terminals) != 1:
                    shard_failed = True
                terminals.extend(shard_terminals)
            missing = [name for name in (expected or {}).get(package, ()) if name.startswith(('Test', 'Fuzz')) and name not in ran]
            for name in missing:
                output.write(json.dumps({'Time': latest_time, 'Action': 'output', 'Package': package, 'Output': f'SHARD-LOST {name}\n'}, separators=(',', ':')).encode() + b'\n')
            action = 'fail' if shard_failed or missing else 'pass'
            failed |= action == 'fail'
            terminal_time = max((event.get('Time', '') for event in terminals), default=latest_time)
            elapsed = max((event.get('Elapsed', 0) for event in terminals), default=0)
            output.write(json.dumps({'Time': terminal_time, 'Action': action, 'Package': package, 'Elapsed': elapsed}, separators=(',', ':')).encode() + b'\n')
    return 1 if failed else 0


def command_args():
    parser = argparse.ArgumentParser(prog='test-shard.sh')
    sub = parser.add_subparsers(dest='mode', required=True)
    for mode in ('run', 'plan', 'merge'):
        cmd = sub.add_parser(mode)
        cmd.add_argument('--out', required=True)
        if mode in ('run', 'plan'):
            cmd.add_argument('--history')
        else:
            cmd.add_argument('streams', nargs='+')
    args, flags = parser.parse_known_args()
    if args.mode == 'run':
        if not flags or flags[0] != '--':
            error('run needs -- before go test flags')
        args.flags = flags[1:]
        args.build_flags, args.binary_flags = split_flags(args.flags)
    elif flags:
        error(f'unrecognized arguments: {flags}')
    return args


def main():
    args = command_args()
    output_ready(args.out)
    if args.mode == 'merge':
        return merged_streams([(path, None) for path in args.streams], args.out)

    packages = package_list()
    history = history_tests(args.history)
    counts = shard_counts(packages)
    selected = [package for package in packages if counts[package] > 1]
    raw_dir = Path(args.out).parent / 'unit-shards'
    if args.mode == 'run':
        raw_dir.mkdir(exist_ok=True)
        for suffix in ('*.json', '*.err'):
            for old in raw_dir.glob(suffix):
                old.unlink()

    with tempfile.TemporaryDirectory(prefix='test-shard-') as binary_dir:
        return run_packages(args, packages, selected, counts, history, raw_dir, Path(binary_dir))


def run_packages(args, packages, selected, counts, history, raw_dir, binary_dir):
    processes = []
    streams = []
    exits = []
    expected = {}
    run_env = os.environ.copy()
    run_env.pop('PFM_TEST_PFM_BINARY', None)
    prebuild_error = None
    if args.mode == 'run':
        prebuilt = binary_dir / 'pfm'
        build_env = os.environ.copy()
        build_env.update({'CGO_ENABLED': '0', 'GOFLAGS': '', 'GOTOOLCHAIN': 'local',
                          'GOTELEMETRY': 'off', 'HOME': os.environ.get('HOME', '')})
        build = subprocess.run(['go', 'build', '-trimpath', '-buildvcs=false', '-o', str(prebuilt), './cmd/pfm'],
                               cwd=MODULE, env=build_env, text=True, stdout=subprocess.PIPE,
                               stderr=subprocess.STDOUT)
        if build.returncode:
            prebuild_error = build.stdout or f'go build exited {build.returncode}'
            print(f'test-shard: prebuilt pfm build failed: {prebuild_error.rstrip()}', file=sys.stderr)
        else:
            run_env['PFM_TEST_PFM_BINARY'] = str(prebuilt)

    def start(command, cwd, package=None):
        index = len(streams) + 1
        path = raw_dir / f'{index}.json'
        stdout = open(path, 'wb')
        stderr = open(path.with_suffix('.err'), 'wb')
        try:
            proc = subprocess.Popen(command, cwd=cwd, env=run_env, stdout=stdout, stderr=stderr)
        except OSError as exc:
            stdout.close()
            stderr.close()
            for running, _, _, _ in processes:
                running.kill()
                running.wait()
            error(f'could not run package {package or command[-1]}: {exc}')
        stdout.close()
        stderr.close()
        processes.append((proc, path, package, command[0]))
        streams.append((path, package))
        return proc

    def finish(procs):
        for proc, path, _, executable in procs:
            proc.wait()
            stderr = path.with_suffix('.err').read_bytes()
            if stderr:
                sys.stderr.buffer.write(stderr)
            if proc.returncode:
                exits.append((path, proc.returncode, executable))

    unsharded = [package for package in packages if package not in selected]
    if args.mode == 'run' and unsharded:
        start(['go', 'test', *args.flags, '-count=1', '-timeout', '25m', '-json', *unsharded], MODULE)

    for package in selected:
        binary = binary_dir / (package.rsplit('/', 1)[-1] + '.test')
        build_flags = args.build_flags if args.mode == 'run' else []
        compile_run = subprocess.run(['go', 'test', '-c', '-o', str(binary), *build_flags, package], cwd=MODULE,
                                     env=run_env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        if compile_run.returncode:
            if compile_run.stdout:
                sys.stderr.write(compile_run.stdout)
            if args.mode == 'plan':
                error(f'compiling package {package} failed', 1)
            path = raw_dir / f'{len(streams) + 1}.json'
            with open(path, 'w', encoding='utf-8') as output:
                for line in compile_run.stdout.splitlines(keepends=True):
                    output.write(json.dumps({'Action': 'output', 'Package': package, 'Output': line}) + '\n')
                output.write(json.dumps({'Action': 'fail', 'Package': package, 'Elapsed': 0}) + '\n')
            streams.append((path, package))
            break
        try:
            names = list_tests(package, str(binary), run_env)
        except SystemExit:
            for proc, _, _, _ in processes:
                proc.kill()
                proc.wait()
            raise
        shards, costs = assignments(names, counts[package], (history or {}).get(package, {}))
        if args.mode == 'plan':
            for index, shard in enumerate(shards, 1):
                print(f'{package}\t{index}\t{",".join(shard)}\t{costs[index - 1]:.3f}')
            continue
        expected[package] = [name for shard in shards for name in shard]
        group = []
        for shard in shards:
            selector = '^(' + '|'.join(re.escape(name) for name in shard) + ')$' if shard else '^$'
            command = ['go', 'tool', 'test2json', '-t', '-p', package, str(binary), '-test.v=test2json',
                       '-test.count=1', '-test.timeout', '25m', '-test.run', selector, *args.binary_flags]
            start(command, package_dir(package), package)
            group.append(processes[-1])
        finish(group)

    if args.mode == 'plan':
        return 0
    if processes and processes[0][2] is None:
        finish(processes[:1])
    code = merged_streams(streams, args.out, expected)
    if prebuild_error:
        with open(args.out, 'ab') as output:
            for line in prebuild_error.splitlines(keepends=True):
                output.write(json.dumps({'Action': 'build-output', 'ImportPath': './cmd/pfm',
                                         'Output': line if line.endswith('\n') else line + '\n'}).encode() + b'\n')
            output.write(json.dumps({'Action': 'build-fail', 'ImportPath': './cmd/pfm'}).encode() + b'\n')
    for path, returncode, executable in exits:
        if code == 0 or returncode != 1:
            print(f'test-shard: {executable} for {path} exited {returncode}', file=sys.stderr)
    return 1 if exits or prebuild_error else code


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except OSError as exc:
        error(str(exc))
PY
