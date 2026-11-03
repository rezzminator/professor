#!/usr/bin/env python3
"""Run-owned native version inputs; launch and pruning actions belong to callers."""
from contextlib import contextmanager
import hashlib
import os
from pathlib import Path
import shutil
import sys

from faults import owned_path, preserved_file
from runtime import command, read_json, write_json

MOCK = Path('/usr/local/libexec/pfm-journey/mock-engine')
OLD = '2.1.250'
SUCCESSOR = '2.1.999'


@contextmanager
def prior_version(binary=MOCK):
    """Yield old/successor paths while preserving original config and version files.

    Publicly birth the target inside this context, verify its recorded Binary,
    then use faults.temporarily_missing_file(paths['old']) around real restart.
    """
    source = Path(binary).resolve(strict=True)
    if not source.is_file() or not os.access(source, os.X_OK):
        raise ValueError('CLAUDE-VERSION-SOURCE: executable required: ' + str(source))
    home = owned_path(Path(os.environ['HOME']) / '.local', preserve=True)
    versions = home / 'share/claude/versions'
    created = []
    directory = versions
    while not directory.exists():
        created.append(directory)
        directory = directory.parent
    versions.mkdir(parents=True, exist_ok=True)
    old, successor = versions / OLD, versions / SUCCESSOR
    config = Path(os.environ['PFM_CONFIG'])
    try:
        with preserved_file(config), preserved_file(old), preserved_file(successor):
            for path in (old, successor):
                if path.is_symlink():
                    path.unlink()
                shutil.copyfile(source, path)
                path.chmod(0o755)
            settings = read_json(config)
            settings.setdefault('claude', {})['binary'] = str(old)
            write_json(config, settings)
            yield {'old':str(old), 'successor':str(successor),
                   'source_sha256':hashlib.sha256(source.read_bytes()).hexdigest()}
    finally:
        for directory in created:
            directory.rmdir()


def self_test(binary):
    import tempfile
    from unittest.mock import patch
    with tempfile.TemporaryDirectory(prefix='journey-claude-versions-') as temporary:
        root = Path(temporary)
        home = root/'home'
        home.mkdir()
        config = root/'config.json'
        raw = b'{"claude":{"cache1h":true},"accounts":[]}\n'
        config.write_bytes(raw)
        config.chmod(0o600)
        scenario = root/'scenario.json'
        write_json(scenario, {'version':'2.1.250 (Claude Code)', 'steps':[]})
        env = {'JOURNEY_ROOT':str(root),'HOME':str(home),'PFM_CONFIG':str(config),
               'MOCK_ENGINE_ENGINE':'claude','MOCK_ENGINE_SCENARIO':str(scenario)}
        with patch.dict(os.environ, env):
            for fail in (False,True):
                try:
                    with prior_version(binary) as paths:
                        assert read_json(config) == {'claude':{'cache1h':True,'binary':str(home/'.local/share/claude/versions/2.1.250')},'accounts':[]}
                        for key, literal in [('old','2.1.250'),('successor','2.1.999')]:
                            path = Path(paths[key])
                            assert path == home/'.local/share/claude/versions'/literal, paths
                            assert path.read_bytes() == Path(binary).read_bytes(), path
                            assert path.stat().st_mode & 0o777 == 0o755, path
                        assert Path(paths['old']).stat().st_ino != Path(paths['successor']).stat().st_ino, paths
                        assert command([paths['old'],'--version']) == '2.1.250 (Claude Code)\n'
                        if fail:
                            raise RuntimeError('fixture interrupted')
                except RuntimeError as error:
                    assert str(error) == 'fixture interrupted', error
                assert config.read_bytes() == raw, config
                assert config.stat().st_mode & 0o777 == 0o600, config
                assert list(home.iterdir()) == [], home
            denied = root/'not-executable'
            denied.write_bytes(b'fixture input')
            denied.chmod(0o600)
            try:
                with prior_version(denied):
                    raise AssertionError('non-executable source accepted')
            except ValueError as error:
                assert str(error) == 'CLAUDE-VERSION-SOURCE: executable required: ' + str(denied), error
            assert config.read_bytes() == raw, config
    print('CLAUDE VERSION INPUT PASS: actual mock ELF runs; config and files restored on success/error')


if __name__ == '__main__':
    if len(sys.argv) == 3 and sys.argv[1] == '--self-test':
        self_test(Path(sys.argv[2]))
    else:
        raise SystemExit('usage: import prior_version() | claude_versions.py --self-test MOCK-ELF')
