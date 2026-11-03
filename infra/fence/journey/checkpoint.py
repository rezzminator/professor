#!/usr/bin/env python3
"""Checkpoint identities bind a root to each preceding lane's source."""
import hashlib
import json
import sys
from pathlib import Path
from ledger import subject

LANES = ('machine-lifecycle', 'terminal-open', 'chat-birth', 'chat-reload', 'chat-attention',
         'chat-mesh', 'fleet-reader', 'machine-restart', 'chat-ending', 'machine-lifecycle-final')


def lane_command(directory, lane):
    if lane not in LANES:
        raise ValueError('unknown lane ' + lane)
    final = lane == 'machine-lifecycle-final'
    source = 'machine-lifecycle' if final else lane
    return [str(Path(directory) / 'lanes' / (source + '.sh')), *(['--final'] if final else [])]


def key(root_hash, directory, order):
    digest = hashlib.sha256(root_hash.encode())
    digest.update(json.dumps(subject(Path(directory).resolve().parents[2]), sort_keys=True).encode())
    for lane in order.split():
        path = Path(lane_command(directory, lane)[0])
        digest.update(lane.encode() + b"\0" + path.read_bytes())
    return digest.hexdigest()[:24]


def self_test():
    import os
    import shutil
    import tempfile
    from unittest.mock import patch
    from projects import git
    with tempfile.TemporaryDirectory(prefix='journey-checkpoint-') as temporary:
        root = Path(temporary)
        directory = root / 'infra/fence/journey'
        (directory / 'lanes').mkdir(parents=True)
        (directory / 'lanes/chat-ending.sh').write_text('ending-fixture\n')
        (directory / 'lanes/machine-lifecycle.sh').write_text('machine-fixture\n')
        assert lane_command(directory, 'machine-lifecycle-final') == [str(directory / 'lanes/machine-lifecycle.sh'), '--final']
        assert lane_command(directory, 'chat-ending') == [str(directory / 'lanes/chat-ending.sh')]
        with patch(__name__+'.subject', return_value={'tree':'fixture-tree','dirty':'fixture-dirty'}):
            actual = key('fixture-root', directory, 'chat-ending machine-lifecycle-final')
        assert actual == '7d9ff59043d6f8ae56b29309', actual
        try:
            lane_command(directory, 'unregistered')
        except ValueError as error:
            assert str(error) == 'unknown lane unregistered', error
        else:
            raise AssertionError('unknown lane accepted')
        source = Path(__file__).resolve().parents[3]
        shutil.copytree(source/'scripts/suite-checks', root/'scripts/suite-checks', ignore=shutil.ignore_patterns('__pycache__'))
        (root/'pfm/scripts').mkdir(parents=True)
        shutil.copyfile(source/'pfm/scripts/repo-git.sh', root/'pfm/scripts/repo-git.sh')
        design = root/'docs/design/pfm-integration-suite'
        design.mkdir(parents=True)
        shutil.copyfile(source/'docs/design/pfm-integration-suite/exclusions.txt', design/'exclusions.txt')
        helper = directory/'lib.sh'
        helper.write_text('fixture helper\n')
        git(root,'init','-q','-b','main')
        git(root,'add','-A')
        git(root,'commit','-q','-m','invented checkpoint fixture')
        with patch.dict(os.environ, {'PFM_DEV_REPO_GIT_DIR':str(root/'.git'),'PFM_DEV_REPO_WORK_TREE':str(root),
                                     'PYTHONDONTWRITEBYTECODE':'1'}):
            original = key('fixture-root', directory, 'chat-ending machine-lifecycle-final')
            helper.write_text('changed fixture helper\n')
            changed = key('fixture-root', directory, 'chat-ending machine-lifecycle-final')
            assert changed != original, ('helper change retained checkpoint',changed)
            helper.write_text('fixture helper\n')
            assert key('fixture-root', directory, 'chat-ending machine-lifecycle-final') == original
            (design/'runs').mkdir()
            (design/'runs/fixture.tsv').write_text('invented run output\n')
            assert key('fixture-root', directory, 'chat-ending machine-lifecycle-final') == original
    print('CHECKPOINT PLAN PASS: full source fingerprint; helper changes invalidate, restored bytes and run outputs preserve key')


if __name__ == "__main__":
    try:
        if sys.argv[1:] == ['--self-test']:
            self_test()
        elif sys.argv[1:] == ['order']:
            print(' '.join(LANES))
        elif sys.argv[1] == 'script':
            print(lane_command(*sys.argv[2:])[0])
        elif sys.argv[1] == 'argument':
            print(' '.join(lane_command(*sys.argv[2:])[1:]))
        elif sys.argv[1] == "key":
            print(key(*sys.argv[2:]))
        else:
            raise ValueError("usage: checkpoint.py order | script|argument JOURNEY-DIR LANE | key ROOT-HASH JOURNEY-DIR 'LANE ...'")
    except (ValueError, OSError, RuntimeError) as error:
        print(f"CHECKPOINT-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
