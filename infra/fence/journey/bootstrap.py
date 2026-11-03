#!/usr/bin/env python3
"""Root inventory, live process restart, and carried identities for lane preludes."""
import json
import os
import re
from pathlib import Path
import sqlite3
import shlex
import sys
import time

import observation
import activity
from observation import bundle
from observation_lifecycle import expectations
from observation_process import engine_births
from runtime import command, read_json, write_json, write_text
from isolation import zsh_path
from terminal import keys, text, type_command
import herdr
from managed_unit import run as run_managed_unit
from bootstrap_evidence import failed_composer
from bootstrap_snapshot import complete_observation, snapshot


def wait(label, predicate, bound=15):
    deadline = time.monotonic() + bound
    while True:
        value = predicate()
        if value:
            return value
        if time.monotonic() >= deadline:
            raise ValueError(f"ROOT-TIMEOUT: {label}")
        time.sleep(0.1)


def inventory():
    root = Path(os.environ["JOURNEY_ROOT"])
    home = Path(os.environ["HOME"])
    declared = read_json(root / "inventory.json")
    missing = []
    for project in declared["projects"]:
        if not (home / "work" / project / ".git").exists():
            missing.append(project + ":git")
    for nested in ("atlas/.worktrees/flight-export", "atlas-worktrees/flight-review", "atlas/plugins/lantern"):
        if not (home / "work" / nested / ".git").is_file():
            missing.append(nested + ":git-link")
    if missing:
        raise ValueError("ROOT-INVENTORY-INCOMPLETE: " + ", ".join(missing) +
                         "; authorized Git fixture creation is required")
    return declared


def engine_ready(chat):
    try:
        status = json.loads(observation.report(['pfm','chat','status',chat['session_id'],'--json'], allowed_codes=(0,3)))
    except RuntimeError as error:
        if 'agent_not_found' in str(error):
            return False
        raise
    if status['state'] not in {'idle','working','blocked'}:
        return False
    display = observation.terminal(chat['terminal'])
    births = engine_births({'terminal':display,'process_births':observation.terminal_births(display)}, chat.get('engine','claude'))
    if not births:
        return False
    prior = {(birth['pid'],birth['start']) for birth in chat['expected_prior_births']}
    if any((birth['pid'],birth['start']) in prior for birth in births):
        raise ValueError('CHECKPOINT-OLD-BIRTH-SURVIVED: ' + chat['session_id'])
    return status


def resume_live(chat, owner):
    if engine_ready(chat):
        return
    target = chat['terminal']
    if target['backend'] == 'herdr':
        wait('restored shell ' + chat['name'], lambda: 'New chat' in observation.screen(target) or observation.screen(target).rstrip().endswith('%'), 30)
        if 'New chat' in observation.screen(target):
            keys(target, ['escape'])
        wait('restored prompt ' + chat['name'], lambda: observation.screen(target).rstrip().endswith('%'))
        type_command(target, ['pfm','chat','open','--restored',chat['session_id']])
    else:
        from chat import seats, seat_terminal
        wait('restore owner shell', lambda: observation.screen(owner).rstrip().endswith('%'))
        type_command(owner, ['pfm','chat','open',chat['session_id']])
        def live_seat():
            rows = [row for row in seats() if row['session_id'] == chat['session_id'] and row['state'] == 'live'
                    and (not chat.get('seat_id') or row['seat_id'] == chat['seat_id'])]
            if len(rows) > 1:
                raise ValueError('CHECKPOINT-SEAT-AMBIGUOUS: ' + chat['session_id'])
            return rows[0] if rows else False
        chat['terminal'] = seat_terminal(wait('restored tmux seat ' + chat['name'], live_seat, 30))
        def owner_command():
            return observation.report(['tmux','-S',owner['socket'],'display-message','-p','-t',owner['pane'],'#{pane_current_command}']).strip()
        wait('restore owner dispatch', lambda: owner_command() in {'tmux','zsh'}, 15)
        if owner_command() == 'tmux':
            keys(owner, ['ctrl+b','d'])
        wait('detached restore owner shell', lambda: observation.screen(owner).rstrip().endswith('%'), 15)
    wait('restored engine ' + chat['name'], lambda: engine_ready(chat), 30)


def herdr_command(*arguments):
    return ["herdr", "--session", os.environ["JOURNEY_SESSION"], *arguments]



def tmux_server(socket_name="default", session="owner"):
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", socket_name) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_-]*", session):
        raise ValueError("ROOT-TMUX-NAME: plain socket basename and session required")
    directory = Path(os.environ["PFM_TMUX_DIR"])
    if not directory.is_absolute() or directory.is_symlink():
        raise ValueError("ROOT-TMUX-DIRECTORY: absolute non-symlink required")
    directory.resolve().relative_to(Path(os.environ["JOURNEY_ROOT"]).resolve(strict=True))
    socket = directory / socket_name
    if socket.is_symlink() or (socket.exists() and not socket.is_socket()):
        raise ValueError(f"ROOT-SOCKET-OCCUPIED: {socket}")
    directory.mkdir(parents=True, exist_ok=True)
    command(["bash", "-c", 'tmux -S "$1" has-session -t "$2" 2>/dev/null || '
             'tmux -S "$1" new-session -d -x 160 -y 50 -s "$2" -c "$HOME" "$3" -l',
             "journey-owner", str(socket), session, zsh_path()])
    return {"backend": "tmux", "socket": str(socket), "pane": session + ":0.0"}


def start_servers():
    root = Path(os.environ["JOURNEY_ROOT"])
    socket = Path(os.environ["HERDR_SOCKET_PATH"])
    ready = False
    if socket.exists():
        try:
            herdr.request(str(socket), "workspace.list", {})
            ready = True
        except (ConnectionRefusedError, FileNotFoundError):
            if not socket.is_socket():
                raise ValueError(f"ROOT-SOCKET-OCCUPIED: {socket}")
            socket.unlink()
    if not ready:
        # nohup detaches only this run's server; the containing fence owns its lifetime.
        command(["bash", "-c", 'nohup "$@" >"$JOURNEY_ROOT/herdr.log" 2>&1 </dev/null &',
                 "journey-herdr", *herdr_command("server")])
    wait("Herdr socket", socket.is_socket)
    return tmux_server()


def workspace(label, cwd):
    result = json.loads(command(herdr_command("workspace", "create", "--label", label, "--cwd", cwd, "--no-focus")))["result"]
    return {"id": result["workspace"]["workspace_id"], "tab": result["tab"]["tab_id"],
            "backend": "herdr", "pane": result["root_pane"]["pane_id"], "label": label}


def owner_client(owner):
    pane = command(["tmux", "-S", owner["socket"], "new-window", "-d", "-P", "-F", "#{pane_id}",
                    "-t", "owner", "-n", "herdr-client", *herdr_command()]).strip()
    return {"backend": "tmux", "socket": owner["socket"], "pane": pane}


def rearm_picker(target, observer=False):
    from chat import shell_ready
    wait('picker or parent shell', lambda: '◆ pfm ' in observation.screen(target) or shell_ready(target), 30)
    if '◆ pfm ' in observation.screen(target):
        keys(target, ['escape'])
    wait('picker parent shell', lambda: shell_ready(target))
    text(target, 'env -u PFM_BEAT_FILE -u PFM_BEAT pfm' if observer else 'pfm')
    keys(target, ['enter'])
    wait('fresh empty picker', lambda: 'New chat' in observation.screen(target), 30)
    return target


def observer_picker(target):
    return rearm_picker(target, observer=True)


def refresh_observer():
    """Use the newly installed binary without restarting any carried engine."""
    return observer_picker(read_json(os.environ["JOURNEY_CARRY"])["observer_picker"])


def live_chat(name, pane, account, project):
    log_path = os.environ["JOURNEY_ACTIVITY"]
    log_start = activity.cursor(log_path)
    target = {"backend": "herdr", "pane": pane}
    def screen():
        return herdr.request(os.environ["HERDR_SOCKET_PATH"], "pane.read", {"pane_id": pane, "source": "visible"})["result"]["read"]["text"]
    wait("picker or shell " + pane, lambda: "New chat" in screen() or screen().rstrip().endswith("%"))
    if "New chat" in screen():
        keys(target, ["escape"])
    wait("parent zsh " + pane, lambda: screen().rstrip().endswith("%"))
    text(target, "cd -- " + shlex.quote(os.environ["HOME"]))
    keys(target, ["enter"])
    wait("home-start caller " + pane, lambda: herdr.request(os.environ["HERDR_SOCKET_PATH"], "pane.get", {"pane_id": pane})["result"]["pane"]["foreground_cwd"] == os.environ["HOME"])
    text(target, "pfm chat new --backend herdr --engine cc --account " + str(account) +
         " --name " + shlex.quote(name) + " --cwd " + shlex.quote(str(Path(os.environ["HOME"]) / "work" / project)))
    keys(target, ["enter"])
    def seat(identity=None):
        from chat import seats
        rows = [row for row in seats() if row['herdr_socket'] == os.environ['HERDR_SOCKET_PATH'] and row['state'] == 'live'
                and (row['seat_id'] == identity if identity else row['name'] == name)]
        if len(rows) > 1:
            raise ValueError(f"ROOT-SEAT-AMBIGUOUS: {pane}")
        return rows[0] if rows and rows[0]["session_id"] and rows[0]["herdr_pane"] else None

    actual = wait("live chat " + name, seat, 30)
    missing = None
    history = []
    progress = Path(os.environ['JOURNEY_OUT']) / 'bootstrap' / (actual['seat_id'] + '.json')
    def engine_screen():
        nonlocal actual, missing
        current = seat(actual['seat_id'])
        if current is None or current['herdr_pane'] == missing:
            return ''  # Wait for the durable relocation commit; never reread a refused old ID.
        if current['herdr_pane'] != actual['herdr_pane']:
            history.append({'previous':actual['herdr_pane'], 'current':current['herdr_pane']})
        actual = current
        try:
            result = herdr.request(os.environ['HERDR_SOCKET_PATH'], 'pane.read', {'pane_id':actual['herdr_pane'],'source':'visible'})
        except herdr.Refused as error:
            if error.code != 'pane_not_found':
                raise
            missing = actual['herdr_pane']
            write_json(progress, {'seat_id':actual['seat_id'],'session_id':actual['session_id'],'missing':missing,'bindings':history})
            return ''
        write_json(progress, {'seat_id':actual['seat_id'],'session_id':actual['session_id'],'pane':actual['herdr_pane'],'bindings':history})
        return result['result']['read']['text']
    try:
        wait("engine composer " + name, lambda: '❯' in engine_screen(), 20)
    except ValueError:
        failed_composer(progress, actual, missing, history, log_path, log_start)
        raise
    engine_target = {"backend": "herdr", "pane": actual["herdr_pane"]}
    # Inline fixture turns leave the shared positional upgrade script unconsumed.
    text(engine_target, 'journey-prelude mock-engine: {"type":"turn","reply":"journey-seed-ready","busy_ms":200}')
    keys(engine_target, ["enter"])
    def seeded():
        if 'journey-seed-ready' not in engine_screen():
            return False
        try:
            status = json.loads(command(["pfm", "chat", "status", actual["session_id"], "--json"]))
        except RuntimeError as error:
            if 'agent_not_found' in str(error):
                return False
            raise
        return status['state'] == 'idle'
    wait("seeded idle chat " + name, seeded, 20)
    return {"name": name, "seat_id": actual["seat_id"], "session_id": actual["session_id"],
            "terminal": {"backend": "herdr", "pane": actual["herdr_pane"]}, "project": project, "account": account}


def boot():
    declared = inventory()
    root = Path(os.environ["JOURNEY_ROOT"])
    home = Path(os.environ["HOME"])
    workspaces = {}
    config_path = home / ".config/herdr/config.toml"
    configured = config_path.read_text()
    default = 'default_shell = "' + str(home / '.local/bin/herdr-shield') + '"'
    if configured.count(default) != 1:
        raise ValueError("ROOT-SHELL-CONFIG: expected the fixture's shield default")
    # The previous release predates the shield relay protocol; seed it through its native public entry.
    config_path.write_text(configured.replace(default, 'default_shell = ' + json.dumps(zsh_path())))
    try:
        owner = start_servers()
        for label in ("atlas", "beacon", "cobalt"):
            workspaces[label] = workspace(label, str(home / "work" / label))
        main = live_chat("carried-chat", workspaces["atlas"]["pane"], 1, "atlas")
        beacon = live_chat("beacon-chat", workspaces["beacon"]["pane"], 2, "beacon")
        foreign = live_chat("foreign-sentinel", workspaces["cobalt"]["pane"], 3, "cobalt")
    finally:
        config_path.write_text(configured)
        command(herdr_command("server", "reload-config"))
    command(herdr_command("plugin", "link", str(home / ".local/share/herdr-shield/plugin")))
    command(["herdr-shield", "doctor"])
    for label in ("atlas", "beacon", "cobalt"):
        herdr.create_tab(workspaces[label]['id'], str(home))
    for label in declared["workspaces"]:
        cwd = home if label == "~" else home / "work" / label
        if label not in workspaces:
            workspaces[label] = workspace(label, str(cwd))
    for kind, label in declared["workspace_states"].items():
        workspaces[kind] = workspace(label, str(home / "work/atlas"))
    mixed = workspaces["mixed"]
    result = herdr.create_tab(mixed['id'], str(home / 'work/beacon'))
    mixed["foreign_pane"] = result["root_pane"]["pane_id"]
    carry = {"chats": [main, beacon], "foreign": foreign, "owner": owner, "workspaces": workspaces,
             "client": owner_client(owner),
             "picker": {"backend": "herdr", "pane": workspaces["~"]["pane"]}}
    dedicated = herdr.create_tab(workspaces['~']['id'], str(home))
    carry['observer_picker'] = observer_picker({'backend':'herdr','pane':dedicated['root_pane']['pane_id']})
    write_json(os.environ["JOURNEY_CARRY"], carry)
    write_json(root / "target.json", carry["picker"])
    command(herdr_command("workspace", "focus", workspaces["~"]["id"]))
    result = complete_observation(root / "target.json")
    write_json(Path(os.environ["JOURNEY_OUT"]) / "foreign.json", result["foreign"])
    print("ROOT PASS: inventory and foreign sentinel captured")


def resume():
    carry = read_json(os.environ["JOURNEY_CARRY"])
    root = Path(os.environ['JOURNEY_ROOT'])
    receipt_file = root / 'checkpoint-births.json'
    receipts = read_json(receipt_file)
    for chat in [*carry['chats'], carry['foreign']]:
        saved = receipts[chat.get('seat_id') or chat['session_id']]
        if expectations(chat)['lifecycle'] == 'live':
            if not saved['births']:
                raise ValueError('CHECKPOINT-BIRTHS-ABSENT: ' + chat['session_id'])
            chat['expected_prior_births'] = saved['births']
    write_json(os.environ['JOURNEY_CARRY'], carry)
    owner = start_servers()
    for chat in [*carry["chats"], carry["foreign"]]:
        if not observation.reopenable(chat):
            continue
        resume_live(chat, owner)
    carry["owner"], carry["client"] = owner, owner_client(owner)
    if carry.get('phase') != 'uninstalled':
        carry['observer_picker'] = observer_picker(carry.get('observer_picker', carry['picker']))
    write_json(os.environ["JOURNEY_CARRY"], carry)
    command(herdr_command("workspace", "focus", carry["workspaces"]["~"]["id"]))
    # Identities after a restore must be re-observed, never copied from the saved projection.
    result = complete_observation(Path(os.environ["JOURNEY_ROOT"]) / "target.json")
    write_json(Path(os.environ["JOURNEY_OUT"]) / "foreign.json", result["foreign"])
    print("CHECKPOINT PASS: processes restarted through public restored entries; observation complete")


def self_test():
    import tempfile
    from types import SimpleNamespace
    from unittest.mock import patch
    with tempfile.TemporaryDirectory(prefix='journey-bootstrap-') as temporary:
        state, reads, prompts = {'moved':False,'seeded':False,'pending':2}, [], []
        home = str(Path(temporary)/'home')
        env = {'HOME':home,'HERDR_SOCKET_PATH':'/j/fixture/herdr.sock','JOURNEY_OUT':temporary,
               'JOURNEY_ACTIVITY':str(Path(temporary)/'activity.jsonl')}
        write_text(env['JOURNEY_ACTIVITY'], '')
        write_text(Path(env['JOURNEY_ACTIVITY']+'.lock'), '')
        def seats():
            relocated = state['moved'] and state['pending'] == 0
            if state['moved'] and state['pending']:
                state['pending'] -= 1
            return [{'seat_id':'fixture-seat','session_id':'fixture-session','name':'fixture','state':'live',
                     'herdr_socket':'/j/fixture/herdr.sock','herdr_pane':'w9:p1' if relocated else 'w3:p2'}]
        def request(socket, method, params):
            if method == 'pane.get':
                return {'result':{'pane':{'foreground_cwd':home}}}
            if params['pane_id'] == 'w3:p1':
                return {'result':{'read':{'text':'journey% '}}}
            reads.append(params['pane_id'])
            if params['pane_id'] == 'w3:p2':
                assert reads.count('w3:p2') == 1, ('refused stale ID retried',reads)
                state['moved'] = True
                raise herdr.Refused('pane.read', {'error':{'code':'pane_not_found'}})
            assert params['pane_id'] == 'w9:p1', params
            return {'result':{'read':{'text':('journey-seed-ready\n' if state['seeded'] else '')+'❯'}}}
        def typed(target, value):
            if value.startswith('journey-prelude'):
                prompts.append(target)
                state['seeded'] = True
        with patch.dict(os.environ, env), patch.dict(sys.modules, {'chat':SimpleNamespace(seats=seats)}), \
             patch.object(herdr,'request',request), patch(__name__+'.text',typed), patch(__name__+'.keys',lambda *args:None), \
             patch(__name__+'.command',lambda *args,**options:'{"state":"idle"}'):
            try:
                actual = live_chat('fixture','w3:p1',1,'atlas')
            except herdr.Refused as error:
                raise AssertionError('expected durable moved target; actual refusal='+str(error)) from error
        assert actual == {'name':'fixture','seat_id':'fixture-seat','session_id':'fixture-session',
                          'terminal':{'backend':'herdr','pane':'w9:p1'},'project':'atlas','account':1}, actual
        assert prompts == [{'backend':'herdr','pane':'w9:p1'}], prompts
        assert read_json(Path(temporary)/'bootstrap/fixture-seat.json')['bindings'] == [{'previous':'w3:p2','current':'w9:p1'}]
    print('BOOTSTRAP TARGET PASS: moved durable seat followed; refused old pane read once; input reaches actual new pane')


if __name__ == "__main__":
    try:
        if sys.argv[1:] == ['--self-test']:
            self_test()
        elif sys.argv[1] == 'snapshot':
            snapshot(sys.argv[2])
        else:
            {"boot": boot, "resume": resume, "observer": refresh_observer}[sys.argv[1]]()
    except (OSError, ValueError, RuntimeError, KeyError, sqlite3.Error) as error:
        print(f"ROOT-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
