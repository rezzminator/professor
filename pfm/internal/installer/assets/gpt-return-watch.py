"""PreToolUse hook of the gpt-foreman twin: a child's return watch runs in the flights-foreman manual's exact form.

Claude Code runs it from the twin's frontmatter `PreToolUse` hook (matcher `Bash`) as `gpt-return-watch.py`. The
manual's watch is `timeout N bash -c 'until [ -s "$1" ]; do sleep S; done; cat "$1"' _ {flight}/returns/{file}`.
GPT models swap its `cat` for another reader; a `python3 -c "..."` nested in `bash -c` raises Claude Code's
"could not be checked" dialog, and the flight stalls until a human answers it. A single-file watch on a returns file whose reader is not `cat "$1"` gets that reader
back, its timeout, sleep and path kept, and is allowed; every other call passes untouched. Every decision is one
JSON line in `{log dir}/return-watch.jsonl`. Any failure is logged and the call passes unchanged.
"""
import fcntl
import json
import os
import re
import sys
import time

LOG_DIR = os.environ.get('GPT_RETURN_WATCH_LOG_DIR') or os.path.join(os.environ.get('PFM_HOME') or os.path.expanduser('~'), '.local/state/pfm/gpt-return-watch')
READER = 'cat "$1"'
RETURNS = r'''"[^"\s]*/returns/[^"\s]+"|'[^'\s]*/returns/[^'\s]+'|[^\s'"]*/returns/[^\s'"]+'''
WATCH = re.compile(r'''\A\s*timeout\s+(?P<timeout>\d+)\s+bash\s+-c\s+'until \[ -s "\$1" \]; do sleep (?P<sleep>\d+); '''
                   r'''done; (?P<reader>.*)'\s+_\s+(?P<path>''' + RETURNS + r''')\s*\Z''', re.S)


def log(entry):
    entry['ts'] = time.strftime('%Y-%m-%dT%H:%M:%S')
    path = os.path.join(LOG_DIR, 'return-watch.jsonl')
    try:
        os.makedirs(LOG_DIR, exist_ok=True)
        append_decision_log(path, entry)
    except OSError as e:
        print('gpt-return-watch: cannot write the decision log %s: %r' % (path, e), file=sys.stderr)


# Shared with the stop hook via this shipped sibling, resolved from its managed source.
MAX_LOG_BYTES = 256 * 1024
MAX_COUNTERS = 1024
COUNTER_DAYS = 7


def append_decision_log(path, entry):
    encoded = (json.dumps(entry) + '\n').encode()
    if len(encoded) > MAX_LOG_BYTES:
        encoded = (json.dumps({'decision': entry.get('decision'), 'error': 'audit entry exceeded byte limit'}) + '\n').encode()
    with open(path + '.lock', 'a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if os.path.exists(path) and os.path.getsize(path) + len(encoded) > MAX_LOG_BYTES:
            # Even a preexisting oversized audit leaves at most one bounded backup.
            with open(path, 'rb') as previous:
                previous.seek(max(0, os.path.getsize(path) - MAX_LOG_BYTES))
                tail = previous.read(MAX_LOG_BYTES)
            if os.path.getsize(path) > MAX_LOG_BYTES:
                tail = tail.partition(b'\n')[2]
            with open(path + '.1', 'wb') as backup:
                backup.write(tail)
            with open(path, 'wb'):
                pass
        with open(path, 'ab') as current:
            current.write(encoded)


def prune_counts(directory):
    now = time.time()
    retained = []
    for entry in os.scandir(directory):
        if not re.fullmatch(r'[a-f0-9]{64}', entry.name) or not entry.is_file(follow_symlinks=False):
            continue
        modified = entry.stat(follow_symlinks=False).st_mtime
        if now - modified > COUNTER_DAYS * 86400:
            os.unlink(entry.path)
        else:
            retained.append((modified, entry.path))
    for _, path in sorted(retained)[:max(0, len(retained) - MAX_COUNTERS + 1)]:
        os.unlink(path)


def decide(payload):
    """(decision, the hook's stdout object or None) for one PreToolUse payload."""
    if payload.get('tool_name') != 'Bash':
        return 'pass', None
    tool_input = payload['tool_input']
    if not isinstance(tool_input, dict) or not isinstance(tool_input.get('command'), str):
        raise ValueError('tool_input carries no command string: %r' % (tool_input,))
    watch = WATCH.match(tool_input['command'])
    if not watch:
        return 'pass', None
    # Only a literal single path can inherit this hook's permission decision.
    # Shell operators, expansions and globs must go through normal permission checks.
    path = watch.group('path')
    literal = path[1:-1] if path[:1] in ('"', "'") else path
    if not re.fullmatch(r'[A-Za-z0-9_./@%+=:,-]+', literal) or '..' in literal.split('/'):
        return 'pass', None
    root = literal[:literal.rfind('/returns/') + len('/returns')]
    # Parent aliases are fine, but returns itself and its children cannot escape by symlink.
    expected_root = os.path.join(os.path.realpath(os.path.dirname(root)), 'returns')
    resolved_root = os.path.realpath(root)
    if resolved_root != expected_root or os.path.commonpath((resolved_root, os.path.realpath(literal))) != resolved_root:
        return 'pass', None
    if watch.group('reader') == READER:
        return 'canonical', None
    command = "timeout %s bash -c 'until [ -s \"$1\" ]; do sleep %s; done; %s' _ %s" % (
        watch.group('timeout'), watch.group('sleep'), READER, watch.group('path'))
    return 'rewrite', {'hookSpecificOutput': {
        'hookEventName': 'PreToolUse', 'permissionDecision': 'allow',
        'permissionDecisionReason': "return watch reader set to the flights-foreman manual's cat",
        'updatedInput': dict(tool_input, command=command)}}


def main():
    entry = {'decision': 'error'}
    try:
        payload = json.load(sys.stdin)
        entry['agent_id'] = payload.get('agent_id')
        entry['decision'], out = decide(payload)
        if entry['decision'] != 'pass':
            entry['command'] = payload['tool_input']['command'][:300]
        if out is not None:
            print(json.dumps(out))
    except Exception as e:  # the hook never blocks a call: log it, pass the call unchanged
        entry['error'] = repr(e)[:300]
        print('gpt-return-watch: %r' % (e,), file=sys.stderr)
    log(entry)
    return 0


if __name__ == '__main__':
    sys.exit(main())
