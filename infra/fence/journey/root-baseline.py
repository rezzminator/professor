#!/usr/bin/env python3
"""Capture operator-owned bytes before the first installer touches the root."""
import base64
import hashlib
import os
from pathlib import Path
import shutil
import stat
import sys

from runtime import command, read_json, write_json, write_text

OPERATOR_FILES = ('.config/herdr/config.toml', '.config/herdr-shield/config.json', '.claude/settings.json')
EXCLUDED = {'.git', '.credentials.json', '.env'}


def entry(path):
    mode = stat.S_IMODE(path.lstat().st_mode)
    if path.is_symlink():
        return {'kind': 'symlink', 'mode': mode, 'target': os.readlink(path)}
    if path.is_dir():
        return {'kind': 'directory', 'mode': mode}
    if not path.is_file():
        raise ValueError('ROOT-BASELINE-NOT-FILE: ' + str(path))
    raw = path.read_bytes()
    return {'kind': 'file', 'mode': mode, 'sha256': hashlib.sha256(raw).hexdigest(),
            'base64': base64.b64encode(raw).decode('ascii')}


def capture(root):
    home = root / 'home'
    output = root / 'foreign-before-install.json'
    if output.exists():
        raise ValueError('ROOT-BASELINE-EXISTS: refusing to replace pre-install evidence')
    operator = {name: entry(home / name) for name in OPERATOR_FILES}
    projects = home / 'work'
    if not projects.is_dir():
        raise ValueError('ROOT-PROJECTS-ABSENT: ' + str(projects))
    manifest = {}
    for path in sorted(projects.rglob('*')):
        relative = path.relative_to(projects)
        if not EXCLUDED.intersection(relative.parts):
            manifest[str(relative)] = entry(path)
    write_json(output, {'version': 1, 'root': str(root), 'files': operator,
                        'projects': manifest, 'excluded': sorted(EXCLUDED)})
    output.chmod(0o600)
    print('ROOT BASELINE PASS: original operator bytes and project content captured')


def capture_previous(root, pfm):
    """Retain each inventory account's settings as the previous release's installer left them."""
    home = root / 'home'
    output = root / 'previous-install'
    if output.exists():
        raise ValueError('ROOT-PREVIOUS-EXISTS: refusing to replace previous-install evidence')
    accounts = read_json(root / 'inventory.json')['claude_accounts']
    if not accounts:
        raise ValueError('ROOT-PREVIOUS-NO-ACCOUNTS: ' + str(root / 'inventory.json'))
    binary = pfm.resolve()
    provenance = {'version': 1, 'pfm_version': command([str(pfm), 'version']).strip(), 'pfm_binary': str(binary),
                  'pfm_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(), 'accounts': {}}
    for account in accounts:
        source = home / '.cc' / str(account) / 'settings.json'
        raw = source.read_bytes()
        artifact = output / str(account) / 'settings.json'
        write_text(artifact, raw.decode('utf-8'), mode=0o400)
        provenance['accounts'][str(account)] = {
            'source': str(source), 'source_entry': {k: v for k, v in entry(source).items() if k != 'base64'},
            'artifact': str(artifact), 'mode': stat.S_IMODE(source.stat().st_mode),
            'sha256': hashlib.sha256(raw).hexdigest()}
    write_json(output / 'provenance.json', provenance)
    (output / 'provenance.json').chmod(0o400)
    print(f'ROOT PREVIOUS PASS: {len(accounts)} account settings retained after the previous install')


def self_test():
    import tempfile
    with tempfile.TemporaryDirectory(prefix='journey-root-baseline-') as temporary:
        root = Path(temporary)
        for name in OPERATOR_FILES:
            path = root / 'home' / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b'abc')
            path.chmod(0o640)
        project = root / 'home/work/atlas'
        project.mkdir(parents=True)
        (project / 'README.md').write_bytes(b'abc')
        (project / '.env').write_bytes(b'ignored')
        (project / 'link').symlink_to('README.md')
        capture(root)
        actual = read_json(root / 'foreign-before-install.json')
        assert set(actual['files']) == {'.config/herdr/config.toml', '.config/herdr-shield/config.json', '.claude/settings.json'}, actual
        expected = {'kind':'file','mode':0o640,'sha256':'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad','base64':'YWJj'}
        assert all(value == expected for value in actual['files'].values()), actual
        assert set(actual['projects']) == {'atlas','atlas/README.md','atlas/link'}, actual
        assert actual['projects']['atlas/link'] == {'kind':'symlink','mode':0o777,'target':'README.md'}, actual
        try:
            capture(root)
        except ValueError as error:
            assert str(error) == 'ROOT-BASELINE-EXISTS: refusing to replace pre-install evidence', error
        else:
            raise AssertionError('modified baseline replaced original')
        write_json(root / 'inventory.json', {'claude_accounts': [1, 2]})
        shared = root / 'home/.claude/settings.json'
        shared.parent.mkdir(parents=True, exist_ok=True)
        shared.write_bytes(b'{"theme":"dark"}\n')
        shared.chmod(0o644)
        own = root / 'home/.cc/1/settings.json'
        own.parent.mkdir(parents=True)
        own.write_bytes(b'abc')
        own.chmod(0o600)
        (root / 'home/.cc/2').mkdir(parents=True)
        (root / 'home/.cc/2/settings.json').symlink_to('../../.claude/settings.json')
        pfm = root / 'pfm-previous'
        pfm.write_text('#!/bin/sh\necho "pfm 0.81.0"\n')
        pfm.chmod(0o755)
        capture_previous(root, pfm)
        previous = root / 'previous-install'
        assert (previous / '1/settings.json').read_bytes() == b'abc'
        assert (previous / '2/settings.json').read_bytes() == b'{"theme":"dark"}\n'
        assert stat.S_IMODE((previous / '2/settings.json').stat().st_mode) == 0o400
        provenance = read_json(previous / 'provenance.json')
        assert provenance == {
            'version': 1, 'pfm_version': 'pfm 0.81.0', 'pfm_binary': str(pfm.resolve()),
            'pfm_sha256': hashlib.sha256(b'#!/bin/sh\necho "pfm 0.81.0"\n').hexdigest(),
            'accounts': {
                '1': {'source': str(own), 'artifact': str(previous / '1/settings.json'), 'mode': 0o600,
                      'source_entry': {'kind': 'file', 'mode': 0o600, 'sha256': expected['sha256']},
                      'sha256': expected['sha256']},
                '2': {'source': str(root / 'home/.cc/2/settings.json'), 'artifact': str(previous / '2/settings.json'),
                      'mode': 0o644, 'source_entry': {'kind': 'symlink', 'mode': 0o777, 'target': '../../.claude/settings.json'},
                      'sha256': hashlib.sha256(b'{"theme":"dark"}\n').hexdigest()}}}, provenance
        try:
            capture_previous(root, pfm)
        except ValueError as error:
            assert str(error) == 'ROOT-PREVIOUS-EXISTS: refusing to replace previous-install evidence', error
        else:
            raise AssertionError('second previous capture replaced evidence')
    print('ROOT PREVIOUS SELFTEST PASS: per-account settings bytes, 0400 artifacts, provenance and overwrite refusal')
    print('ROOT BASELINE SELFTEST PASS: exact original bytes, modes, exclusions and overwrite refusal')


if __name__ == '__main__':
    try:
        if sys.argv[1:] == ['--self-test']:
            self_test()
            sys.exit(0)
        if os.environ.get('PFM_DEV_FENCE') != '1':
            raise ValueError('ROOT-BASELINE: requires the fence')
        root = Path(os.environ['JOURNEY_ROOT'])
        if root.parent != Path('/j') or Path(os.environ['HOME']) != root / 'home':
            raise ValueError('ROOT-BASELINE: private run HOME required')
        if sys.argv[1:] == ['--previous']:
            capture_previous(root, Path(shutil.which('pfm') or 'pfm'))
        else:
            capture(root)
    except (OSError, ValueError, KeyError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(2)
