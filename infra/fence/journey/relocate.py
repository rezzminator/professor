#!/usr/bin/env python3
"""Rebase stopped-checkpoint path metadata without rewriting messages or logs."""
import json
import os
from pathlib import Path
import shutil
import sqlite3
import sys
import urllib.parse

from runtime import read_json, write_json


def checkpoint_terminals(saved, carry):
    result = {}
    for chat in [*carry['chats'], carry['foreign']]:
        entry = saved.get(chat.get('seat_id') or chat['session_id'])
        if entry:
            result[chat['session_id']] = {'source':entry['terminal'], 'current':chat['terminal']}
    return result


class Rebase:
    def __init__(self, previous, current):
        self.previous, self.current = previous, current
        self.old, self.new = previous['JOURNEY_ROOT'], current['JOURNEY_ROOT']
        self.prefix = previous['PFM_UNIT_PREFIX'], current['PFM_UNIT_PREFIX']
        self.session = previous['JOURNEY_SESSION'], current['JOURNEY_SESSION']

    def path(self, value):
        if value == self.old or value.startswith(self.old + '/'):
            value = self.new + value[len(self.old):]
        # Claude's provider wire key encodes a cwd by replacing directory separators.
        return value.replace('/projects/' + self.old.replace('/', '-'), '/projects/' + self.new.replace('/', '-'))

    def address(self, value):
        value = self.path(value)
        value = value.replace('/sessions/' + self.session[0] + '/', '/sessions/' + self.session[1] + '/')
        parsed = urllib.parse.urlsplit(value)
        if parsed.scheme in {'http', 'https', 'file'}:
            netloc = parsed.netloc
            if parsed.hostname in {'127.0.0.1', 'localhost'} and parsed.port == int(self.previous['JOURNEY_MCP_PORT']):
                netloc = parsed.netloc.rsplit(':', 1)[0] + ':' + self.current['JOURNEY_MCP_PORT']
            return urllib.parse.urlunsplit(parsed._replace(netloc=netloc, path=self.path(parsed.path)))
        return value

    def environment(self, entries):
        result = []
        for entry in entries:
            name, separator, value = entry.partition('=')
            if separator and name in {'PFM_RUN', 'PFM_BEAT', 'PFM_BEAT_FILE', 'PFM_UNIT_PREFIX', 'JOURNEY_SESSION', 'JOURNEY_MCP_PORT'}:
                value = self.current[name]
            elif separator and name == 'PATH':
                value = os.pathsep.join(self.path(part) for part in value.split(os.pathsep))
            elif separator and (name in self.previous or name in {'CLAUDE_CONFIG_DIR','CODEX_HOME','JOURNEY_OUT','JOURNEY_ACTIVITY','JOURNEY_DESIGN'}):
                value = self.address(value)
            result.append(name + separator + value if separator else entry)
        return result

    def arguments(self, entries):
        result, previous = [], ''
        for entry in entries:
            if previous in {'--settings', '--mcp-config'} and entry.startswith('{'):
                entry = json.dumps(self.configuration(json.loads(entry)), separators=(',', ':'))
            elif previous in {'--settings','--mcp-config','--system-prompt-file','--append-system-prompt-file','--prompt-file','--cwd','--dir','--config','--log','--ledger'}:
                entry = self.address(entry)
            result.append(entry)
            previous = entry
        return result

    def configuration(self, value, key=''):
        if key in {'message','messages','user','assistant','content','text','reply','prompt','input','name','label','title','description'}:
            return value
        if key == 'env' and isinstance(value, list):
            return self.environment(value)
        if key == 'env' and isinstance(value, dict):
            return dict(entry.split('=', 1) for entry in self.environment([name + '=' + text for name, text in value.items()]))
        if key in {'args','launch_argv','resume_argv'} and isinstance(value, list):
            return self.arguments(value)
        if isinstance(value, dict):
            return {(self.path(name) if key in {'projects','files'} else name): self.configuration(item, name) for name, item in value.items()}
        if isinstance(value, list):
            return [self.configuration(item, key) for item in value]
        if isinstance(value, str) and (key.lower() in {'cwd','identity_cwd','dir','directory','home','root','configdir','binary','shell','executable','path','paths','link_dirs','instructions','installpath','installlocation','pluginlogdir','instruction','config','socket','herdr_socket','tmux_socket','action_file','record_dir','sid_dir','proc_root','until_gone','url','endpoint'} or key.lower().endswith(('_path','_file','_dir'))):
            return self.address(value)
        if isinstance(value, str) and key == 'command':
            return self.path(value)
        return value

    def database(self, path):
        columns = {'seat': ('tmux_socket','herdr_socket'), 'chat': ('cwd',),
                   'comms': ('receiver_socket',), 'reminders': ('cwd',),
                   'transcripts': ('path','cwd'), 'rollouts': ('path','cwd'),
                   'oc_sessions': ('directory',)}
        with sqlite3.connect(path) as connection:
            for table, names in columns.items():
                present = {row[1] for row in connection.execute('SELECT * FROM pragma_table_info(?)', (table,))}
                for name in names:
                    if name not in present:
                        continue
                    # Names are fixed schema literals above; only cell values are bound.
                    prefix = self.old + '/'
                    values = connection.execute(f'SELECT DISTINCT {name} FROM {table} WHERE typeof({name})=\'text\' AND ({name}=? OR substr({name},1,?)=?)', (self.old, len(prefix), prefix)).fetchall()
                    for (value,) in values:
                        translated = self.address(value)
                        if translated != value:
                            connection.execute(f'UPDATE {table} SET {name}=? WHERE {name}=?', (translated, value))
            connection.commit()
            connection.execute('PRAGMA wal_checkpoint(TRUNCATE)')

    def file(self, path, root):
        relative = path.relative_to(root)
        parts = relative.parts
        if 'out' in parts or 'log' in parts or 'mock-records' in parts:
            return
        if path.is_symlink():
            original = os.readlink(path)
            translated = self.address(original)
            if path.is_relative_to(root/'home/.config/systemd/user') or path.is_relative_to(root/'home/.local/share/pfm/install/systemd'):
                translated = translated.replace(*self.prefix)
            if original != translated:
                path.unlink()
                path.symlink_to(translated)
            return
        if path.suffix == '.jsonl':
            return
        if not path.is_file():
            return
        with path.open('rb') as stream:
            head = stream.read(32)
        if head.startswith(b'SQLite format 3\x00'):
            self.database(path)
            return
        home = root / 'home'
        json_file = path in {root/'pfm.config.json', root/'carry.json', root/'scenario.json'} or (
            path.name in {'.claude.json','settings.json','hooks.json','workbenches.json','binary-ownership.json','mcp-ownership.json','settings-hook-ownership.json','known_marketplaces.json','installed_plugins.json','claude-env-ownership.json','theme-ownership.json','opencode-instructions.json','skill-links.json'}
            and path.is_relative_to(home)) or path.parent == root/'state/seats' or path == home/'.config/herdr-shield/config.json' or (path.name == 'session.json' and path.is_relative_to(home/'.config/herdr/sessions'))
        if json_file:
            original = read_json(path)
            translated = self.configuration(original)
            if translated != original:
                write_json(path, translated)
            return
        generated_unit = path.is_relative_to(home/'.config/systemd/user') or path.is_relative_to(home/'.local/share/pfm/install/systemd')
        herdr_config = path == home/'.config/herdr/config.toml'
        git_path = path.name in {'gitdir','commondir','.git','.gitmodules'} or (path.name == 'config' and '.git' in parts)
        source_pointer = path == home/'.local/share/pfm/install/source-repo'
        shell_profile = path.parent == home and path.name in {'.zshrc','.bashrc','.zshenv','.profile','.bash_profile'}
        wrapper = path == home/'.local/share/pfm/install/bin/pfm-herdr-shell'
        environment_file = path == home/'.config/environment.d/pfm-claude-config-dir.conf'
        if not (generated_unit or herdr_config or git_path or source_pointer or shell_profile or wrapper or environment_file):
            return
        original = path.read_text()
        if shell_profile:
            translated = ''.join(line.replace(self.old + '/', self.new + '/') if ('/pfm/internal/installer/assets/shim/' in line or 'CLAUDE_CONFIG_DIR=' in line or 'PATH=' in line) else line for line in original.splitlines(keepends=True))
        elif wrapper:
            translated = ''.join(line.replace(self.old + '/', self.new + '/') if line.startswith('pfm_wrapper=') else line for line in original.splitlines(keepends=True))
        else:
            translated = original.replace(self.old + '/', self.new + '/')
            if generated_unit:
                translated = translated.replace(*self.prefix).replace(*self.session)
                translated = translated.replace('--port ' + self.previous['JOURNEY_MCP_PORT'], '--port ' + self.current['JOURNEY_MCP_PORT'])
        if translated != original:
            path.write_text(translated)


def relocate(previous_env, current_env):
    previous, current = read_json(previous_env), read_json(current_env)
    source, target = Path(previous['JOURNEY_ROOT']), Path(current['JOURNEY_ROOT'])
    if source.parent != Path('/j') or target.parent != Path('/j') or source == target or source.is_symlink():
        raise ValueError('CHECKPOINT-ROOT: two distinct private /j roots required')
    if target.exists() or not source.is_dir():
        raise ValueError(f'CHECKPOINT-ROOT-OCCUPIED: source={source} target={target}')
    rebase = Rebase(previous, current)
    # Overlay snapshots cannot rename a lower-layer directory; move preserves symlinks.
    for path in source.rglob('*'):
        if path.is_socket() and not path.is_symlink():
            path.unlink()
    shutil.move(source, target)
    for path in target.rglob('*'):
        rebase.file(path, target)
    sessions = target/'home/.config/herdr/sessions'
    old_session = sessions/previous['JOURNEY_SESSION']
    if old_session.exists():
        old_session.rename(sessions/current['JOURNEY_SESSION'])
    for base in (target/'home/.config/systemd/user', target/'home/.local/share/pfm/install/systemd'):
        if base.exists():
            for directory, directories, files in os.walk(base, topdown=False):
                for name in [*files,*directories]:
                    if name.startswith(previous['PFM_UNIT_PREFIX'] + '-'):
                        path = Path(directory)/name
                        path.rename(path.with_name(current['PFM_UNIT_PREFIX'] + name[len(previous['PFM_UNIT_PREFIX']):]))
    for projects in (target/'home/.cc').glob('*/projects'):
        for project in projects.iterdir():
            old_prefix, new_prefix = str(source).replace('/', '-'), str(target).replace('/', '-')
            if project.is_dir() and project.name.startswith(old_prefix + '-'):
                project.rename(project.with_name(new_prefix + project.name[len(old_prefix):]))
    config = read_json(current['PFM_CONFIG'])
    config['mcp']['http']['port'] = int(current['JOURNEY_MCP_PORT'])
    write_json(current['PFM_CONFIG'], config)
    write_json(target/'env.json', current)
    write_json(target/'beat.json', {'run':current['PFM_RUN'],'beat':'root/restore'})
    receipts = target / 'checkpoint-births.json'
    if receipts.is_file():
        saved, carry = read_json(receipts), read_json(target / 'carry.json')
        write_json(target / 'checkpoint-terminals.json', checkpoint_terminals(saved, carry))
    print(f'CHECKPOINT RELOCATED {source} -> {target}')


def self_test():
    import tempfile
    from isolation import environment
    previous, current = environment('/j/old', 'old', 23000), environment('/j/new', 'new', 23001)
    rebase = Rebase(previous, current)
    message = '/j/old private label ' + previous['PFM_UNIT_PREFIX'] + ' PFM_RUN=old'
    with tempfile.TemporaryDirectory(prefix='journey-relocate-') as temporary:
        root = Path(temporary)
        config = root/'pfm.config.json'
        write_json(config, {'cwd':'/j/old/seat/work/atlas', 'label':message})
        recipe = root/'state/seats/fixture.json'
        write_json(recipe, {'binary':'/j/old/seat/.local/bin/claude', 'cwd':'/j/old/seat/work/atlas',
                            'args':['--name',message,'--system-prompt-file','/j/old/seat/prompt.md'],
                            'env':['PFM_RUN=old','PFM_BEAT_FILE=/j/old/beat.json','MY_MESSAGE=' + message]})
        transcript = root/'home/.cc/1/projects/fixture/session.jsonl'
        log = root/'home/.local/state/pfm/log/pfm.jsonl'
        artifact = root/'out/beats/terminal-open/open-picker/result.json'
        originals = {}
        for path in (transcript, log, artifact):
            path.parent.mkdir(parents=True, exist_ok=True)
            original = json.dumps({'cwd':'/j/old/seat/work/atlas','run':'old','message':message}).encode() + b'\n'
            path.write_bytes(original)
            originals[path] = original
        database = root/'state/pfm.db'
        history_link = root/'home/.cc/1/history.jsonl'
        history_link.parent.mkdir(parents=True, exist_ok=True)
        history_link.symlink_to('/j/old/seat/.cc/store/history.jsonl')
        wrapper = root/'home/.local/share/pfm/install/bin/pfm-herdr-shell'
        wrapper.parent.mkdir(parents=True, exist_ok=True)
        wrapper.write_text("# user note /j/old stays exact\npfm_wrapper='/j/old/seat/wrapper'\n")
        receipt = root/'checkpoint-births.json'
        receipt_bytes = b'{"session":{"status":{"session_id":"session","state":"idle","message":"/j/old"},"terminal":{"backend":"tmux","socket":"/j/old/tmux/default","pane":"%1"},"births":[{"pid":123,"start":"saved-birth"}]}}\n'
        receipt.write_bytes(receipt_bytes)
        with sqlite3.connect(database) as connection:
            connection.execute('CREATE TABLE chat(uuid TEXT, cwd TEXT, label TEXT)')
            connection.execute('INSERT INTO chat VALUES(?,?,?)', ('fixture','/j/old/seat/work/atlas',message))
        for path in (config, recipe, transcript, log, artifact, database, history_link, wrapper, receipt):
            rebase.file(path, root)
        assert read_json(config) == {'cwd':'/j/new/seat/work/atlas','label':message}, config
        assert read_json(recipe) == {'binary':'/j/new/seat/.local/bin/claude','cwd':'/j/new/seat/work/atlas',
                                    'args':['--name',message,'--system-prompt-file','/j/new/seat/prompt.md'],
                                    'env':['PFM_RUN=new','PFM_BEAT_FILE=/j/new/beat.json','MY_MESSAGE=' + message]}, recipe
        for path, original in originals.items():
            assert path.read_bytes() == original, path
        with sqlite3.connect(database) as connection:
            actual = connection.execute('SELECT cwd,label FROM chat WHERE uuid=?', ('fixture',)).fetchone()
        assert actual == ('/j/new/seat/work/atlas', message), (database, actual)
        assert os.readlink(history_link) == '/j/new/seat/.cc/store/history.jsonl', history_link
        assert wrapper.read_text() == "# user note /j/old stays exact\npfm_wrapper='/j/new/seat/wrapper'\n", wrapper
        assert receipt.read_bytes() == receipt_bytes, receipt
        chat = {'session_id':'session','terminal':{'backend':'tmux','socket':'/j/new/tmux/default','pane':'%1'}}
        actual = checkpoint_terminals(read_json(receipt), {'chats':[chat], 'foreign':{'session_id':'absent'}})
        assert actual == {'session':{'source':{'backend':'tmux','socket':'/j/old/tmux/default','pane':'%1'},'current':{'backend':'tmux','socket':'/j/new/tmux/default','pane':'%1'}}}, actual
    print('RELOCATE SELFTEST PASS: metadata rebased; messages, labels, logs and artifacts exact')


if __name__ == '__main__':
    try:
        if sys.argv[1:] == ['--self-test']:
            self_test()
        else:
            relocate(*sys.argv[1:])
    except (OSError, ValueError, KeyError, sqlite3.Error) as error:
        print(f'CHECKPOINT-RELOCATE-ERROR: {error}', file=sys.stderr)
        sys.exit(2)
