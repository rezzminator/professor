#!/usr/bin/env python3
"""Root cache retention; Docker inventory stays in the host orchestration door."""
import csv
import io
import json
from pathlib import Path
import re
import shutil
import sys

from runtime import command, read_json, write_json

ROOT = re.compile(r'[0-9a-f]{20}')
KEEP_NEWEST = 2


def remove_image(reference):
    command(['docker', 'image', 'rm', reference], timeout=120)


def checkpoints():
    containers = command(['docker', 'ps', '-aq']).split()
    used = set(command(['docker', 'inspect', '--format', '{{.Image}}', *containers]).split()) if containers else set()
    references = command(['docker', 'image', 'ls', 'pfm-journey-checkpoint', '--format', '{{.Repository}}:{{.Tag}}']).splitlines()
    if not references:
        return
    if any(not re.fullmatch(r'pfm-journey-checkpoint:[0-9a-f]{24}', reference) for reference in references):
        raise ValueError('CHECKPOINT-CACHE-REFERENCE: unexpected tag')
    images = json.loads(command(['docker', 'image', 'inspect', *references]))
    images.sort(key=lambda image: image['Created'], reverse=True)
    keep = {image['Id'] for image in images[:KEEP_NEWEST]} | used
    for image in images:
        labels = image['Config'].get('Labels') or {}
        if labels.get('pfm.journey') != '1' or not ROOT.fullmatch(labels.get('professor.journey-root', '')):
            raise ValueError('CHECKPOINT-CACHE-OWNER: ' + image['Id'])
        if image['Id'] in keep:
            continue
        for reference in image['RepoTags']:
            if reference in references:
                remove_image(reference)
                print('journey checkpoint-image-removed ' + reference)


def inventory(path):
    output = command(['docker','ps','-a','--filter','label=pfm.journey=1','--format',
                      '{{.ID}}\t{{.Label "professor.journey-root"}}'])
    containers = {}
    for row in csv.reader(io.StringIO(output), delimiter='\t'):
        if len(row) != 2:
            raise ValueError('ROOT-CACHE-CONTAINERS: malformed Docker inventory')
        identity, root = row
        if not root:
            continue
        if not ROOT.fullmatch(root):
            raise ValueError('ROOT-CACHE-CONTAINERS: malformed root label ' + root)
        containers[identity] = root
    write_json(path, {'roots':sorted(set(containers.values())), 'containers':containers})
    return set(containers.values())


def entries(shared):
    result = []
    for cache in sorted(Path(shared).glob('*/timing/journey-roots')):
        for directory in sorted(cache.iterdir()):
            if not ROOT.fullmatch(directory.name):
                continue
            if directory.is_symlink() or not directory.is_dir():
                raise ValueError('ROOT-CACHE-ALIAS: ' + str(directory))
            manifest = directory / 'manifest.json'
            if not manifest.is_file():
                marker = directory / 'owner.json'
                if not marker.is_file():
                    continue  # Legacy incomplete output has no positive ownership marker.
                proof = read_json(marker)
                if proof != {'owner':'journey-root','root':directory.name}:
                    raise ValueError('ROOT-CACHE-OWNER: ' + str(marker))
                stamp = marker.stat().st_mtime_ns
                complete = False
            else:
                proof = read_json(manifest)
                if manifest.is_symlink() or proof.get('root') != directory.name or proof.get('boot') != 'systemd':
                    raise ValueError('ROOT-CACHE-OWNER: ' + str(manifest))
                archive = directory / 'root.tar'
                if archive.is_symlink():
                    raise ValueError('ROOT-CACHE-ALIAS: ' + str(archive))
                complete = archive.is_file() and archive.stat().st_size > 0
                accessed = directory / 'accessed'
                stamp = (accessed if accessed.exists() else manifest).stat().st_mtime_ns
            result.append({'path':directory, 'root':directory.name, 'stamp':stamp, 'complete':complete})
    return result


def retained(caches, used, current):
    newest = {}
    for cache in caches:
        if cache['complete']:
            newest[cache['root']] = max(cache['stamp'], newest.get(cache['root'], 0))
    ordered = sorted(newest, key=lambda root:(newest[root],root), reverse=True)
    return set(ordered[:KEEP_NEWEST]) | set(used) | {current}


def prune_os(shared):
    archives = sorted(Path(shared).glob('*/timing/journey-roots/os-*.tar'), key=lambda path: path.stat().st_mtime_ns, reverse=True)
    for path in archives:
        if path.is_symlink() or not path.is_file() or not re.fullmatch(r'os-[0-9a-f]{20}\.tar', path.name):
            raise ValueError('ROOT-OS-CACHE-OWNER: ' + str(path))
    for path in archives[KEEP_NEWEST:]:
        path.unlink()
        print('journey os-cache-removed ' + str(path))


def prune(shared, usage, current):
    used = set(read_json(usage)['roots'])
    if not ROOT.fullmatch(current) or any(not ROOT.fullmatch(root) for root in used):
        raise ValueError('ROOT-CACHE-USAGE: invalid root identity')
    caches = entries(shared)
    keep = retained(caches, used, current)
    retired = {cache['root'] for cache in caches if cache['root'] not in keep}
    pending = Path(usage).with_name('pruned-images.json')
    # Persist intent before deletion so interrupted filesystem cleanup cannot orphan its image.
    write_json(pending, sorted(set(read_json(pending) if pending.exists() else []) | retired))
    for cache in caches:
        if cache['root'] in keep:
            continue
        directory = cache['path']
        history = directory.parent / 'pruned'
        history.mkdir(exist_ok=True)
        for name in ('manifest.json','build.log','owner.json'):
            source = directory / name
            if source.is_file() and not source.is_symlink():
                shutil.copy2(source, history / (directory.name + '.' + name))
        shutil.rmtree(directory)
    for root in sorted(retired):
        print('JOURNEY_PRUNE_IMAGE=' + root)
    prune_os(shared)
    return retired


def images(usage, roots):
    used = inventory(usage)
    pending = Path(usage).with_name('pruned-images.json')
    remaining = set(read_json(pending) if pending.exists() else []) | set(roots)
    listed = set(command(['docker','image','ls','--format','{{.Repository}}:{{.Tag}}']).splitlines())
    for root in sorted(remaining.copy()):
        if not ROOT.fullmatch(root):
            raise ValueError('ROOT-CACHE-IMAGE: invalid root identity')
        reference = 'pfm-journey-root:' + root
        if root in used:
            print('journey cache-image-preserved used=' + root)
        elif reference in listed:
            label = command(['docker','image','inspect','--format','{{index .Config.Labels "professor.journey-root"}}',reference]).strip()
            if label != root:
                raise ValueError('ROOT-CACHE-IMAGE-OWNER: ' + reference)
            remove_image(reference)
            print('journey cache-image-removed root=' + root)
            remaining.remove(root)
        else:
            print('journey cache-image-absent root=' + root)
            remaining.remove(root)
        write_json(pending, sorted(remaining))


def self_test():
    import contextlib
    import tempfile
    from unittest.mock import patch
    with tempfile.TemporaryDirectory(prefix='journey-root-cache-') as temporary:
        base = Path(temporary)
        roots = ['a'*20,'b'*20,'c'*20,'d'*20]
        for index, root in enumerate(roots):
            folder = base / ('flight' if index%2 else 'frozen') / 'timing/journey-roots' / root
            write_json(folder/'manifest.json', {'root':root,'boot':'systemd'})
            (folder/'root.tar').write_bytes(b'fixture export')
            (folder/'build.log').write_bytes(b'compiler fixture log\n')
            (folder/'accessed').touch()
            import os
            os.utime(folder/'accessed', ns=(index+1,index+1))
        for index, letter in enumerate('abc'):
            archive = base/'frozen/timing/journey-roots'/('os-'+letter*20+'.tar')
            archive.write_bytes(b'OS fixture archive')
            os.utime(archive, ns=(index+1,index+1))
        usage = base/'usage.json'
        write_json(usage, {'roots':['a'*20]})
        incomplete = base/'flight/timing/journey-roots'/('e'*20)
        write_json(incomplete/'owner.json', {'owner':'journey-root','root':'e'*20})
        (incomplete/'root.tar.next').write_bytes(b'incomplete fixture export')
        with patch(__name__+'.shutil.rmtree', side_effect=RuntimeError('fixture interrupted')):
            try:
                prune(base, usage, 'd'*20)
            except RuntimeError as error:
                assert str(error) == 'fixture interrupted', error
            else:
                raise AssertionError('interrupted retirement returned successfully')
        pending = base/'pruned-images.json'
        assert (read_json(pending) if pending.exists() else []) == ['b'*20,'e'*20], pending
        with contextlib.redirect_stdout(io.StringIO()):
            removed = prune(base, usage, 'd'*20)
        assert removed == {'b'*20,'e'*20}, removed
        assert read_json(base/'pruned-images.json') == ['b'*20,'e'*20]
        assert {item['root'] for item in entries(base)} == {'a'*20,'c'*20,'d'*20}
        assert (base/'flight/timing/journey-roots/pruned'/('b'*20+'.build.log')).read_bytes() == b'compiler fixture log\n'
        foreign = base/'flight/timing/journey-roots'/('f'*20)
        write_json(foreign/'manifest.json', {'root':'not-owned','boot':'systemd'})
        try:
            prune(base, usage, 'd'*20)
        except ValueError as error:
            assert str(error) == 'ROOT-CACHE-OWNER: ' + str(foreign/'manifest.json'), error
        else:
            raise AssertionError('foreign cache removed')
        assert read_json(foreign/'manifest.json') == {'root':'not-owned','boot':'systemd'}
        calls = []
        def docker(argv, **options):
            calls.append(argv)
            if argv[1] == 'ps':
                return 'existing\t' + 'a'*20 + '\n'
            if argv[1:3] == ['image','ls']:
                return '\n'.join('pfm-journey-root:'+root for root in roots)
            if argv[1:3] == ['image','inspect']:
                return argv[-1].split(':',1)[1]
            return ''
        with patch(__name__+'.command', docker), contextlib.redirect_stdout(io.StringIO()):
            images(usage, ['a'*20])
        assert [call for call in calls if call[1:3] == ['image','rm']] == [['docker','image','rm','pfm-journey-root:'+'b'*20]], calls
        assert read_json(base/'pruned-images.json') == ['a'*20]
        assert sorted(path.name for path in base.glob('*/timing/journey-roots/os-*.tar')) == [
            'os-'+'b'*20+'.tar', 'os-'+'c'*20+'.tar']
        checkpoint_rows = [{'Id': letter, 'Created': str(index),
                            'Config': {'Labels': {'pfm.journey': '1', 'professor.journey-root': 'a'*20}},
                            'RepoTags': ['pfm-journey-checkpoint:' + letter*24]}
                           for index, letter in enumerate('abcd')]
        calls = []
        def checkpoint_docker(argv, **options):
            calls.append(argv)
            if argv == ['docker', 'ps', '-aq']:
                return 'owned-container\n'
            if argv[1:3] == ['inspect', '--format']:
                return 'a\n'
            if argv[1:3] == ['image', 'ls']:
                return '\n'.join(row['RepoTags'][0] for row in checkpoint_rows)
            if argv[1:3] == ['image', 'inspect']:
                return json.dumps(checkpoint_rows)
            return ''
        with patch(__name__ + '.command', checkpoint_docker), contextlib.redirect_stdout(io.StringIO()):
            checkpoints()
        assert [call for call in calls if call[1:3] == ['image', 'rm']] == [
            ['docker', 'image', 'rm', 'pfm-journey-checkpoint:' + 'b'*24]], calls
    print('ROOT CACHE PASS: newest2 and container roots retained across profiles; only obsolete images retired')


if __name__ == '__main__':
    try:
        if sys.argv[1:] == ['--self-test']:
            self_test()
        elif sys.argv[1] == 'inventory':
            inventory(sys.argv[2])
        elif sys.argv[1] == 'prune':
            prune(*sys.argv[2:])
        elif sys.argv[1] == 'images':
            images(sys.argv[2], sys.argv[3:])
        elif sys.argv[1] == 'checkpoints':
            checkpoints()
        else:
            raise ValueError('usage: root-cache.py inventory FILE | prune SHARED FILE CURRENT | images FILE ROOT...')
    except (OSError, ValueError, KeyError, RuntimeError) as error:
        print('ROOT-CACHE-ERROR: ' + str(error), file=sys.stderr)
        sys.exit(2)
