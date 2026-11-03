#!/usr/bin/env python3
"""Real SSH authentication, PTY login and run-owned environment proof."""
import json
import os
from pathlib import Path
import pwd
import shlex
import socket
import tempfile
import unittest
from unittest.mock import patch

import journey_path
import ssh
from runtime import command, write_json


class PrivateSSH(unittest.TestCase):
    def test_run_identity_and_port_guards(self):
        with tempfile.TemporaryDirectory(prefix='journey-ssh-') as scratch:
            root = Path(scratch)
            home = root / 'home'
            account = pwd.struct_passwd(('fixture', 'x', 1234, 1234, '', str(home), '/bin/bash'))
            environment = {'PFM_DEV_FENCE': '1', 'JOURNEY_ROOT': scratch, 'HOME': str(home), 'USER': 'fixture',
                           'PFM_RUN': 'synthetic', 'JOURNEY_SSH_PORT': '23457', 'JOURNEY_MCP_PORT': '23456'}
            with patch.dict(os.environ, environment), patch.object(ssh.pwd, 'getpwnam', return_value=account):
                for key, value, marker in [('JOURNEY_SSH_PORT', '23456', 'SSH-PORT'),
                                           ('JOURNEY_SSH_PORT', '1023', 'SSH-PORT'),
                                           ('JOURNEY_SSH_PORT', '65536', 'SSH-PORT'),
                                           ('HOME', '/outside', 'SSH-HOME'), ('PFM_RUN', '../escape', 'SSH-IDENTITY')]:
                    with self.subTest(key=key, value=value), patch.dict(os.environ, {key: value}):
                        with self.assertRaisesRegex(ValueError, marker):
                            ssh.configuration()
                for port in ('1024', '65535'):
                    with self.subTest(port=port), patch.dict(os.environ, {'JOURNEY_SSH_PORT': port}):
                        self.assertEqual((root, root / 'ssh/synthetic', account, int(port)), ssh.configuration())

    def test_native_key_login_has_a_pty_and_only_the_owned_environment(self):
        if os.environ.get('PFM_DEV_FENCE') != '1':
            self.fail('real SSH test requires the fence')
        Path('/j').mkdir(mode=0o755, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='journey-ssh-', dir='/j') as scratch:
            root = Path(scratch)
            home = root / 'home'
            home.mkdir()
            user = 'jssh' + str(os.getpid())
            command(['useradd', '-M', '-U', '-d', str(home), '-s', '/bin/bash', user])
            account = pwd.getpwnam(user)
            os.chown(root, account.pw_uid, account.pw_gid)
            os.chown(home, account.pw_uid, account.pw_gid)
            with socket.socket() as reserve:
                reserve.bind(('127.0.0.1', 0))
                port = reserve.getsockname()[1]
            environment = {'PFM_DEV_FENCE': '1', 'JOURNEY_ROOT': scratch, 'HOME': str(home), 'USER': user,
                           'PFM_RUN': 'synthetic', 'JOURNEY_SSH_PORT': str(port), 'JOURNEY_MCP_PORT': str(port - 1),
                           'PATH': '/usr/local/bin:/usr/bin:/bin'}
            (root / 'env.sh').write_text('export HOME=' + shlex.quote(str(home)) + '\nexport SHELL=/bin/bash\n'
                'export PFM_RUN=synthetic PFM_BEAT_FILE=' + shlex.quote(str(root / 'beat.json')) + '\n')
            process = None
            try:
                with patch.dict(os.environ, environment):
                    endpoint, process = ssh.start_endpoint()
                    probe = 'import json,os;print(json.dumps({"user":os.getuid(),"home":os.environ["HOME"],"cwd":os.getcwd(),"run":os.environ["PFM_RUN"],"beat_file":os.environ["PFM_BEAT_FILE"],"tty":os.isatty(0),"remote":os.environ["SSH_CONNECTION"].split()[2],"ambient":os.environ.get("JOURNEY_HOST_SECRET","absent")}))'
                    with patch.dict(os.environ, {'JOURNEY_HOST_SECRET': 'must-not-cross'}):
                        actual = json.loads(ssh.client(endpoint, 'python3 -c ' + shlex.quote(probe), tty=True).strip())
                    self.assertEqual({'user': account.pw_uid, 'home': str(home), 'cwd': str(home), 'run': 'synthetic',
                        'beat_file': str(root / 'beat.json'), 'tty': True, 'remote': '127.0.0.1', 'ambient': 'absent'}, actual)
                    self.assertEqual(endpoint, ssh.start_endpoint()[0])
                    self.assertEqual(endpoint, json.loads((root / 'ssh.json').read_text()))
                    self.assertEqual(0o600, (root / 'ssh.json').stat().st_mode & 0o777)
                    wrong = root / 'wrong-key'
                    command(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(wrong)])
                    with self.assertRaisesRegex(RuntimeError, r'exit 255: .*Permission denied'):
                        ssh.client(endpoint, 'printf forbidden', identity=wrong)
                    process.terminate()
                    process.wait(timeout=5)
                    process = None
                    with self.assertRaisesRegex(RuntimeError, r'exit 255: .*Connection refused'):
                        ssh.client(endpoint, 'printf forbidden')
            finally:
                if process is not None:
                    process.terminate()
                    process.wait(timeout=5)
                command(['userdel', user])


if __name__ == '__main__':
    unittest.main()
