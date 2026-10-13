"""Stop hook of the GPT role twins: a sub-tracer stores its .hops, a gpt-flights-foreman returns a well-formed first line.

Claude Code runs it from a twin's frontmatter `Stop` hook, converted to `SubagentStop` for that agent only, as
`gpt-agent-stop.py {role}`. Each role reads the agent's own transcript and, while its rule is unmet, refuses the
stop with the exact repair; a role's refusals per agent are capped. Every decision is one JSON line in
`{log dir}/{role}.jsonl`. Any failure is logged and the stop is allowed: the hook never traps an agent.

- sub-tracer: a node that ran `tracetree.py start` has {RUN}/{ID}.started; until {RUN}/{ID}.hops exists, the
  tracer's wait blocks on it through two full timeouts, so the stop is refused (cap 3).
- foreman: a stop with no background task of its own still running is its return, and the manual's first line is
  exactly `DONE {name}` or `PARTIAL|FAILED|BLOCKED {name}: {text}`, {name} the flight or unit its brief names;
  any other first line is refused (cap 2). A stop while a child runs is a wait, never a return.
"""
import hashlib
import json
import os
import re
import runpy
import stat
import sys
import tempfile
import time

LOG_DIR = os.environ.get('GPT_AGENT_STOP_LOG_DIR') or os.path.join(os.environ.get('PFM_HOME') or os.path.expanduser('~'), '.local/state/pfm/gpt-agent-stop')
TRACETREE = 'python3 ~/.claude/skills/tracetree/tracetree.py'
CAPS = {'sub-tracer': 3, 'foreman': 2}
RETURN_LINE = re.compile(r'^(?:DONE (?P<done>[\w.-]*\w)|(?:PARTIAL|FAILED|BLOCKED) (?P<other>[\w.-]*\w): \S.*)$')
LAUNCH = re.compile(r'Async agent launched successfully[\s\S]*?agentId: (\w+)|Command running in background with ID: (\w+)')
NOTICE = re.compile(r'<task-id>(\w+)</task-id>[\s\S]*?<status>(completed|failed|killed|stopped)</status>')


def log(role, entry):
    entry['ts'] = time.strftime('%Y-%m-%dT%H:%M:%S')
    path = os.path.join(LOG_DIR, role + '.jsonl')
    try:
        os.makedirs(LOG_DIR, exist_ok=True)
        state_helpers()['append_decision_log'](path, entry)
        return True
    except Exception as e:
        print('gpt-agent-stop: cannot write the decision log %s; stop allowed: %r' % (path, e), file=sys.stderr)
        return False


_STATE_HELPERS = None


def state_helpers():
    global _STATE_HELPERS
    if _STATE_HELPERS is None:
        _STATE_HELPERS = runpy.run_path(os.path.join(os.path.dirname(os.path.realpath(__file__)), 'gpt-return-watch.py'))
    return _STATE_HELPERS


def texts(value):
    """Every string inside a transcript value: message content, tool results, attachments."""
    if isinstance(value, str):
        yield value
    elif isinstance(value, list):
        for item in value:
            yield from texts(item)
    elif isinstance(value, dict):
        for item in value.values():
            yield from texts(item)


def rows(transcript):
    with open(transcript) as f:
        return [json.loads(line) for line in f if line.strip()]


def first_user_text(records):
    """The agent's first user message: a sub-tracer's card, a foreman's brief; '' without one."""
    for row in records:
        if row.get('type') == 'user':
            content = row.get('message', {}).get('content', '')
            return content if isinstance(content, str) else '\n'.join(
                b.get('text', '') for b in content if isinstance(b, dict) and b.get('type') == 'text')
    return ''


def counter_path(role, agent_id):
    key = hashlib.sha256(json.dumps([role, agent_id]).encode()).hexdigest()
    return os.path.join(LOG_DIR, 'counts', key)


def blocks_so_far(role, agent_id):
    """A per-agent counter keeps hook work independent of retained audit history."""
    path = counter_path(role, agent_id)
    try:
        with open(path) as f:
            count = int(f.read(32))
    except FileNotFoundError:
        return 0
    if not 0 <= count <= CAPS[role]:
        raise ValueError('invalid refusal count in %s: %r' % (path, count))
    return count


def store_block_count(role, agent_id, count):
    path = counter_path(role, agent_id)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    state_helpers()['prune_counts'](os.path.dirname(path))
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode='w', dir=os.path.dirname(path), delete=False) as f:
            temporary = f.name
            f.write(str(count) + '\n')
        os.replace(temporary, path)
    finally:
        if temporary and os.path.exists(temporary):
            os.unlink(temporary)


def tracer_reason(run, node):
    return ('Your card\'s file %(run)s/%(id)s.hops is not stored, and `tracetree.py start` registered you as '
            'running: your tracer waits on every started node until its wait times out, so stopping now holds '
            'the whole trace and returns nothing. Before you stop, store it with one Bash call, '
            '`%(tt)s write %(run)s %(id)s <<\'HOPS\'` ... `HOPS`, in your manual\'s Write format: THREAD, ANSWER, '
            'the hops you already read, and one END line. A thread you could not finish still ends: '
            '`END unread: {why}` at the last line you read; a target that does not exist is an ANSWER line '
            'saying so, cited to the hop that shows it. A refused write prints why: fix it and write again. '
            'Then end with your manual\'s one return line.') % {'run': run, 'id': node, 'tt': TRACETREE}


def sub_tracer(data, records, entry):
    card = first_user_text(records)
    run = re.search(r'^RUN: (\S+)\s*$', card, re.M)
    node = re.search(r'^ID: (\S+)\s*$', card, re.M)
    run, node = (run.group(1) if run else None), (node.group(1) if node else None)
    entry.update({'run': run, 'id': node})
    if not run or not node:
        entry['why'] = 'no RUN: or ID: line in the first user message'
        return 'allow-nocard', None
    started = os.path.exists(os.path.join(run, node + '.started'))
    path = os.path.join(run, node + '.hops')
    stored = False
    try:
        if stat.S_ISREG(os.stat(path).st_mode):
            with open(path) as f:
                stored = bool(f.read(1))
    except FileNotFoundError:
        pass
    entry.update({'started': started, 'stored': stored})
    if not started:
        return 'allow-unstarted', None
    if stored:
        return 'allow-stored', None
    return 'block', tracer_reason(run, node)


def last_text(data, records):
    message = data.get('last_assistant_message')
    if isinstance(message, str) and message.strip():
        return message
    for row in reversed(records):
        if row.get('type') == 'assistant':
            content = row.get('message', {}).get('content', [])
            parts = [b.get('text', '') for b in content if isinstance(b, dict) and b.get('type') == 'text'] \
                if isinstance(content, list) else [content]
            if any(p.strip() for p in parts):
                return '\n'.join(parts)
    return ''


def pending_tasks(records):
    """Only typed results of this agent's background launches establish tasks."""
    calls, launched, ended = {}, [], set()
    for row in records:
        content = row.get('message', {}).get('content', [])
        if isinstance(content, list):
            for block in content:
                if not isinstance(block, dict):
                    continue
                if row.get('type') == 'assistant' and block.get('type') == 'tool_use':
                    name = block.get('name')
                    if name == 'Agent' or (name == 'Bash' and block.get('input', {}).get('run_in_background')):
                        calls[block.get('id')] = name
                elif row.get('type') == 'user' and block.get('type') == 'tool_result':
                    name = calls.get(block.get('tool_use_id'))
                    text = '\n'.join(texts(block.get('content', '')))
                    if name and not block.get('is_error'):
                        match = LAUNCH.match(text)
                        if match and ((name == 'Agent' and match.group(1)) or
                                      (name == 'Bash' and match.group(2))):
                            launched.append(match.group(1) or match.group(2))
        notice = row.get('attachment', {}) if row.get('type') == 'attachment' else None
        if row.get('type') == 'user' and isinstance(content, str) and content.startswith('<task-notification>'):
            notice = content
        if notice is not None:
            for text in texts(notice):
                ended |= {task for task, _ in NOTICE.findall(text)}
    return [task for task in launched if task not in ended]


def assigned_names(brief):
    child = re.search(r'^CHILD ([\w.-]+) · unit ([\w.-]+)\s*$', brief, re.M)
    if child:
        return list(child.groups())
    labeled = re.search(r'^Flight directory: (.+)$', brief, re.M)
    first = brief.strip().splitlines()[0] if brief.strip() else ''
    path = labeled.group(1).strip() if labeled else first.strip()
    match = re.fullmatch(r'.*/flights/[\w.-]+/([\w.-]+)(?:/(?:briefs/([\w.-]+)-r\d+\.md|requirements\.md))?/?', path)
    return [name for name in match.groups() if name] if match else []


def foreman_reason(line, names):
    name = names[0] if names else '{flight}'
    return ('Your return\'s first line is `%s`, and your manual requires it to be exactly one of `DONE %s`, '
            '`PARTIAL %s: {units not done}`, `FAILED %s: {why}` or `BLOCKED %s: {question}` - nothing before it, '
            'nothing after it on that line, no markup; {name} is your flight, or your unit when your brief made '
            'you a child. Your caller matches that line to read your verdict, so a line in any other shape reads '
            'as no return. Send your final message again with that first line and the rest of your return from '
            'line 2, unchanged otherwise; run no tools first.') % ((line[:120],) + (name,) * 4)


def foreman(data, records, entry):
    message = last_text(data, records)
    if '<compact-now>' in message:
        return 'allow-compact', None
    pending = pending_tasks(records)
    if pending:
        entry['pending'] = pending
        return 'allow-waiting', None
    line = message.strip().splitlines()[0].strip() if message.strip() else ''
    entry['line'] = line[:200]
    brief = first_user_text(records)
    names = assigned_names(brief)
    match = RETURN_LINE.match(line)
    name = match and (match.group('done') or match.group('other'))
    if name in names:
        entry['name'] = name
        return 'allow-return', None
    return 'block', foreman_reason(line, names)


ROLES = {'sub-tracer': sub_tracer, 'foreman': foreman}


def main():
    role = sys.argv[1] if len(sys.argv) > 1 else ''
    entry = {'decision': 'allow-error', 'role': role}
    reason = None
    try:
        if role not in ROLES:
            entry['why'] = 'unknown role %r; expected one of %s' % (role, sorted(ROLES))
            print('gpt-agent-stop: ' + entry['why'], file=sys.stderr)
            return
        data = json.load(sys.stdin)
        entry.update({'event': data.get('hook_event_name'), 'agent_id': data.get('agent_id'),
                      'agent_type': data.get('agent_type'), 'stop_hook_active': data.get('stop_hook_active'),
                      'keys': sorted(data)})
        transcript = data.get('agent_transcript_path')
        if not transcript:
            entry['decision'] = 'allow-nocard'
            entry['why'] = 'no agent_transcript_path on stdin'
            return
        decision, reason = ROLES[role](data, rows(transcript), entry)
        if decision == 'block':
            identity = entry['agent_id'] or ['transcript', os.path.realpath(transcript)]
            count = blocks_so_far(role, identity)
            if count >= CAPS[role]:
                decision = 'allow-cap'
                print('gpt-agent-stop: %s rule still unmet after %d refusals; stop allowed' % (role, CAPS[role]),
                      file=sys.stderr)
            else:
                store_block_count(role, identity, count + 1)
        entry['decision'] = decision
    except Exception as e:
        entry['decision'] = 'allow-error'
        entry['error'] = repr(e)
        print('gpt-agent-stop: hook failed, stop allowed: %r' % (e,), file=sys.stderr)
    finally:
        if log(role or 'unknown', entry) and entry['decision'] == 'block':
            print(json.dumps({'decision': 'block', 'reason': reason}))


if __name__ == '__main__':
    main()
