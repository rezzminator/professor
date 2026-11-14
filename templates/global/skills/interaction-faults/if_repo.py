"""Shared read-only git and language-independent test discovery."""
from pathlib import PurePosixPath
import re
import subprocess

TEST_DIRS = {'test', 'tests', 'testing', 'spec', 'specs', '__tests__', '__test__',
             'e2e', 'integration_tests', 'it', 'testdata', 'fixtures'}
TEST_NAME = re.compile(r'^(?:test_.*\..+|.*_test\..+|.*\.test\..+|.*\.spec\..+|.*_spec\..+|.*Tests?\..+|.*IT\..+|conftest\.py)$')


def git(repo, *args, no_match=False):
    result = subprocess.run(['git', '-C', str(repo), *args], capture_output=True, text=True,
                            encoding='utf-8', errors='surrogateescape')
    if no_match and result.returncode == 1:
        return ''
    if result.returncode:
        raise ValueError(f"git {' '.join(args[:3])}: {result.stderr.strip()}")
    return result.stdout


def is_test(path, override=None):
    path = str(path)
    parts = PurePosixPath(path).parts
    return bool((override and re.search(override, path)) or
                any(part.lower() in TEST_DIRS for part in parts[:-1]) or
                TEST_NAME.fullmatch(parts[-1] if parts else ''))


# Fixtures, configs and docs sit in test dirs but run nothing; a tracer hint wants runnable tests.
TEST_DATA_SUFFIXES = {'.md', '.rst', '.txt', '.adoc', '.yml', '.yaml', '.json', '.toml', '.ini',
                      '.cfg', '.csv', '.xml', '.html', '.lock', '.snap', '.golden', '.env'}


def is_runnable_test(path, override=None):
    if override and re.search(override, str(path)):
        return True
    return is_test(path) and PurePosixPath(str(path)).suffix.lower() not in TEST_DATA_SUFFIXES


def in_file_test_paths(repo, paths):
    if not paths:
        return set()
    return set(filter(None, git(repo, 'grep', '-I', '-l', '-z', '-E', '-e',
        r'#[[:space:]]*\[[[:space:]]*(cfg[[:space:]]*\([[:space:]]*test[[:space:]]*\)|test)[[:space:]]*\]',
        '--', *paths, no_match=True).split('\0')))
