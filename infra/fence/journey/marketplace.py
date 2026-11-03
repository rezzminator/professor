#!/usr/bin/env python3
"""Declare offline repository inputs; mocks alone produce marketplace state."""
from pathlib import Path
import sys
from urllib.parse import urlsplit

from runtime import read_json, write_json


def source_checkouts(previous, current):
    previous, current = Path(previous), Path(current)
    catalog = read_json(previous / '.claude-plugin/marketplace.json')
    owner = urlsplit(catalog['owner']['url'])
    if owner.scheme != 'https' or owner.hostname != 'github.com':
        raise ValueError('MOCK-MARKETPLACE-SOURCE: expected a declared GitHub catalog')
    result = {owner.path.strip('/'): str(previous)}
    for plugin in catalog['plugins']:
        source = plugin['source']
        relative = Path(source['path'])
        if source['source'] != 'git-subdir' or relative.is_absolute() or '..' in relative.parts:
            raise ValueError('MOCK-PLUGIN-SOURCE: unsupported catalog source ' + plugin['name'])
        candidates = (current / 'plugins' / plugin['name'], Path('/') / plugin['name'])
        checkout = next((path for path in candidates if (path / relative / '.claude-plugin/plugin.json').is_file()), None)
        if checkout is None:
            raise ValueError('MOCK-SOURCE-MISSING: ' + plugin['name'] + ' at ' + str(relative))
        manifest = read_json(checkout / relative / '.claude-plugin/plugin.json')
        if not isinstance(manifest.get('version'), str) or not manifest['version']:
            raise ValueError('MOCK-SOURCE-VERSION: ' + plugin['name'])
        result[source['url']] = str(checkout)
    return result


def self_test():
    import tempfile
    with tempfile.TemporaryDirectory(prefix='journey-marketplace-') as directory:
        root = Path(directory)
        previous, current = root / 'previous', root / 'current'
        manifest = current / 'plugins/fixture/plugins/fixture/.claude-plugin/plugin.json'
        write_json(manifest, {'version': '1.0.0'})
        write_json(previous / '.claude-plugin/marketplace.json', {
            'owner': {'url': 'https://github.com/journey/fixture'},
            'plugins': [{'name': 'fixture', 'source': {'source': 'git-subdir', 'url': 'https://example.invalid/fixture.git', 'path': './plugins/fixture'}}]})
        actual = source_checkouts(previous, current)
        assert actual == {'journey/fixture': str(previous), 'https://example.invalid/fixture.git': str(current / 'plugins/fixture')}, actual
        manifest.unlink()
        try:
            source_checkouts(previous, current)
        except ValueError as error:
            assert str(error) == 'MOCK-SOURCE-MISSING: fixture at plugins/fixture', error
        else:
            raise AssertionError('missing checkout accepted')
    print('MARKETPLACE INPUTS PASS: exact catalog sources resolve; missing manifest refused')


if __name__ == '__main__':
    try:
        if sys.argv[1:] == ['--self-test']:
            self_test()
        else:
            scenario = read_json(sys.argv[1])
            scenario['source_checkouts'] = source_checkouts('/opt/pfm-journey/previous/source', '/opt/pfm-journey/current/source')
            write_json(sys.argv[1], scenario)
    except (OSError, ValueError, KeyError) as error:
        print('MARKETPLACE-INPUTS-ERROR: ' + str(error), file=sys.stderr)
        sys.exit(2)
