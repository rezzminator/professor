#!/usr/bin/env python3
"""A run-owned, key-only SSH login inside the private journey machine."""
import json
import os
from pathlib import Path
import pwd
import re
import shlex
import shutil
import subprocess
import sys
import tempfile

from runtime import command, read_json, start, write_json


def owned_file(path, text, mode, account):
    temporary = path.with_name(path.name + f'.{os.getpid()}.tmp')
    with temporary.open('x') as output:
        output.write(text)
    temporary.chmod(mode)
    os.chown(temporary, account.pw_uid, account.pw_gid)
    temporary.replace(path)


def configuration():
    if os.environ.get('PFM_DEV_FENCE') != '1' or not Path('/.dockerenv').is_file():
        raise ValueError('SSH-FENCE: private Docker fence required')
    if os.geteuid() != 0:
        raise ValueError('SSH-ROOT: start requires the private machine administrator')
    root = Path(os.environ['JOURNEY_ROOT'])
    if not root.is_absolute() or root == Path('/') or root != root.resolve():
        raise ValueError('SSH-ROOT-PATH: canonical absolute run root required')
    run = os.environ['PFM_RUN']
    user = os.environ['USER']
    if not re.fullmatch(r'[A-Za-z0-9_-]+', run) or not re.fullmatch(r'[a-z_][a-z0-9_-]*', user):
        raise ValueError('SSH-IDENTITY: invalid run or fenced login')
    account = pwd.getpwnam(user)
    home = root / 'home'
    if Path(os.environ['HOME']) != home or Path(account.pw_dir) != home:
        raise ValueError('SSH-HOME: fenced account and run HOME must agree')
    port = int(os.environ['JOURNEY_SSH_PORT'])
    if not 1024 <= port <= 65535 or port == int(os.environ['JOURNEY_MCP_PORT']):
        raise ValueError('SSH-PORT: distinct nonprivileged SSH port required')
    directory = root / 'ssh' / run
    if directory.resolve() != directory:
        raise ValueError('SSH-ESCAPE: run SSH directory contains a symlink')
    directory.mkdir(parents=True, exist_ok=True)
    directory.chmod(0o700)
    os.chown(directory.parent, account.pw_uid, account.pw_gid)
    os.chown(directory, account.pw_uid, account.pw_gid)
    return root, directory, account, port


def client(endpoint, remote, *, identity=None, tty=False):
    return command(['ssh', '-F', '/dev/null', '-tt' if tty else '-T',
        '-o', 'BatchMode=yes', '-o', 'IdentitiesOnly=yes', '-o', 'IdentityAgent=none',
        '-o', 'StrictHostKeyChecking=yes', '-o', 'GlobalKnownHostsFile=/dev/null',
        '-o', 'UserKnownHostsFile=' + endpoint['known_hosts'], '-o', 'ConnectTimeout=2',
        '-o', 'ConnectionAttempts=1', '-o', 'ControlMaster=no',
        '-i', str(identity or endpoint['identity']), '-p', str(endpoint['port']),
        endpoint['user'] + '@' + endpoint['host'], remote], timeout=8)


def start_endpoint():
    root, directory, account, port = configuration()
    executable = shutil.which('sshd') or shutil.which('sshd', path='/usr/sbin')
    if not executable:
        raise ValueError('SSH-DEPENDENCY: missing sshd')
    for tool in ('ssh', 'ssh-keygen'):
        if not shutil.which(tool):
            raise ValueError('SSH-DEPENDENCY: missing ' + tool)
    environment = root / 'env.sh'
    if not environment.is_file() or environment.is_symlink():
        raise ValueError('SSH-ENV: run env.sh must be a regular file')
    endpoint = {'host': '127.0.0.1', 'port': port, 'user': account.pw_name,
                'identity': str(directory / 'identity'), 'known_hosts': str(directory / 'known_hosts')}
    saved = root / 'ssh.json'
    if saved.is_symlink():
        raise ValueError('SSH-ESCAPE: endpoint must not be a symlink')
    if saved.exists() and read_json(saved) == endpoint:
        try:
            if client(endpoint, 'printf journey-ssh-ready') == 'journey-ssh-ready':
                return endpoint, None
        except RuntimeError:
            pass
    for name in ('identity', 'host_key'):
        key = directory / name
        if key.exists() or key.is_symlink():
            if key.is_symlink() or not key.is_file() or key.stat().st_nlink != 1:
                raise ValueError('SSH-KEY: expected private regular key: ' + str(key))
        else:
            command(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-C', 'journey-ssh-fixture', '-f', str(key)])
        public = Path(str(key) + '.pub')
        if public.is_symlink() or not public.is_file() or public.stat().st_nlink != 1:
            raise ValueError('SSH-KEY: expected regular public key: ' + str(public))
        key.chmod(0o600)
        os.chown(key, account.pw_uid, account.pw_gid)
        os.chown(public, account.pw_uid, account.pw_gid)
    host = command(['ssh-keygen', '-y', '-f', str(directory / 'host_key')]).strip()
    owned_file(directory / 'known_hosts', f'[127.0.0.1]:{port} {host}\n', 0o600, account)
    owned_file(directory / 'login', '#!/usr/bin/env bash\nset -euo pipefail\n' +
        'source ' + shlex.quote(str(environment)) + '\n' +
        'if [ -n "${SSH_ORIGINAL_COMMAND:-}" ]; then\n' +
        '  exec "$SHELL" -l -c "$SSH_ORIGINAL_COMMAND"\nfi\nexec "$SHELL" -l\n', 0o700, account)
    config = directory / 'sshd_config'
    owned_file(config, '\n'.join([
        'ListenAddress 127.0.0.1', f'Port {port}',
        'HostKey ' + json.dumps(str(directory / 'host_key')),
        'PidFile ' + json.dumps(str(directory / 'sshd.pid')),
        'AuthorizedKeysFile ' + json.dumps(str(directory / 'identity.pub')),
        'ForceCommand ' + json.dumps(str(directory / 'login')),
        'AllowUsers ' + account.pw_name, 'AuthenticationMethods publickey',
        'PubkeyAuthentication yes', 'PasswordAuthentication no', 'KbdInteractiveAuthentication no',
        'PermitRootLogin prohibit-password', 'PermitEmptyPasswords no', 'StrictModes yes',
        'UsePAM yes', 'PrintMotd no', 'PrintLastLog no', 'LogLevel VERBOSE',
        'AllowAgentForwarding no', 'AllowTcpForwarding no', 'X11Forwarding no',
        'PermitTunnel no', 'PermitUserEnvironment no', 'PermitTTY yes', '',
    ]), 0o600, account)
    Path('/run/sshd').mkdir(mode=0o755, exist_ok=True)
    command([executable, '-t', '-f', str(config)])
    with tempfile.NamedTemporaryFile(mode='w', prefix='sshd-', suffix='.log', dir=directory, delete=False) as output:
        log = Path(output.name)
        os.chown(log, account.pw_uid, account.pw_gid)
        process = start([executable, '-D', '-e', '-f', str(config)], stdin=subprocess.DEVNULL,
                        stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
    from bootstrap import wait
    last_login = ''
    def ready():
        nonlocal last_login
        if process.poll() is not None:
            raise ValueError(f'SSH-START: exit={process.returncode}: {log.read_text()} log={log}')
        try:
            return client(endpoint, 'printf journey-ssh-ready') == 'journey-ssh-ready'
        except RuntimeError as error:
            last_login = str(error)
            return False
    try:
        wait('run-owned SSH authenticated login', ready, 10)
    except (OSError, ValueError, RuntimeError) as error:
        process.terminate()
        process.wait(timeout=5)
        raise ValueError(f'SSH-START: {error}; last login: {last_login}; log={log}: {log.read_text()}') from error
    write_json(saved, endpoint)
    saved.chmod(0o600)
    os.chown(saved, account.pw_uid, account.pw_gid)
    return endpoint, process


if __name__ == '__main__':
    try:
        if sys.argv[1:] != ['start']:
            raise ValueError('usage: ssh.py start')
        print(json.dumps(start_endpoint()[0], sort_keys=True))
    except (OSError, ValueError, RuntimeError, KeyError) as error:
        print(f'SSH-ERROR: {error}', file=sys.stderr)
        sys.exit(2)
