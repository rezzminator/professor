#!/usr/bin/env python3
"""Private file-fault mechanisms; callers use faults public context managers."""
from contextlib import contextmanager, ExitStack
import fcntl
import hashlib
import math
import os
from pathlib import Path
import shutil
import socket
import stat
import tempfile

from runtime import now_ms, write_text


@contextmanager
def preserved_file(value, *, missing=False, retain_inode=False):
    from faults import owned_path
    path = owned_path(value, preserve=True)
    present = path.exists() or path.is_symlink()
    if missing and (not present or path.is_symlink() or not path.is_file()):
        raise ValueError(f"FAULT-MISSING-NOT-REGULAR: {path}")
    if present and not path.is_symlink() and not path.is_file():
        raise ValueError(f"FAULT-PRESERVE-NOT-FILE: {path}")
    scratch = Path(tempfile.mkdtemp(prefix="file-backup-", dir=os.environ["JOURNEY_ROOT"]))
    backup = scratch / "original"
    try:
        if path.is_symlink() or (present and retain_inode):
            os.link(path, backup, follow_symlinks=False)
        elif present:
            shutil.copy2(path, backup)
    except OSError:
        shutil.rmtree(scratch)
        raise
    removed = False
    try:
        if missing:
            path.unlink()
            removed = True
        yield path
    finally:
        if removed and (path.exists() or path.is_symlink()):
            raise ValueError(f"FAULT-MISSING-CONFLICT: {path}; backup={backup}")
        if path.is_dir() and not path.is_symlink():
            raise ValueError(f"FAULT-PRESERVE-REPLACED-DIRECTORY: {path}; backup={backup}")
        if present:
            try:
                unchanged = os.path.samestat(backup.lstat(), path.lstat())
            except FileNotFoundError:
                unchanged = False
            if unchanged:
                backup.unlink()
            else:
                backup.replace(path)
        elif path.exists() or path.is_symlink():
            path.unlink()
        scratch.rmdir()

@contextmanager
def temporary_text(value, contents, mode=None):
    with preserved_file(value) as path:
        write_text(path, contents, mode)
        yield path

def temporarily_missing_file(value):
    return preserved_file(value, missing=True)

@contextmanager
def unreadable(value):
    from faults import owned_path
    path = owned_path(value)
    identity, mode = path.stat(), stat.S_IMODE(path.stat().st_mode)
    path.chmod(0)
    try:
        if os.access(path, os.R_OK):
            raise ValueError(f"FAULT-PRIVILEGED-READER: chmod cannot deny this uid: {path}")
        yield path
    finally:
        actual = path.stat()
        if (identity.st_dev, identity.st_ino) != (actual.st_dev, actual.st_ino):
            raise ValueError(f"FAULT-FILE-REPLACED: {path}")
        path.chmod(mode)

@contextmanager
def deleted_cwd(value):
    from faults import owned_path
    path = owned_path(value)
    if not path.is_dir():
        raise ValueError(f"FAULT-CWD-NOT-DIRECTORY: {path}")
    # A rename leaves the cwd inode alive; remove the tree after making a private backup.
    scratch = Path(tempfile.mkdtemp(prefix="cwd-backup-", dir=os.environ["JOURNEY_ROOT"]))
    backup = scratch / "contents"
    shutil.copytree(path, backup, symlinks=True)
    shutil.rmtree(path)
    try:
        yield path
    finally:
        if path.exists():
            raise ValueError(f"FAULT-CWD-RECREATED: {path}; backup={backup}")
        shutil.copytree(backup, path, symlinks=True)
        shutil.rmtree(scratch)

@contextmanager
def locked_activity(log):
    from faults import owned_path
    owned_path(log)
    lock = owned_path(str(log) + ".lock")
    with locked_file(lock):
        yield lock

@contextmanager
def locked_file(value):
    from faults import owned_path
    lock = owned_path(value)
    if not lock.is_file():
        raise ValueError(f"FAULT-LOCK-NOT-REGULAR: {lock}")
    with lock.open("r+") as handle:
        fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        try:
            yield lock
        finally:
            fcntl.flock(handle, fcntl.LOCK_UN)

@contextmanager
def locked_reporter(chat, scope="session"):
    from faults import owned_path, owned_target
    owned_target(chat["terminal"])
    session = chat.get("session_id") or chat["seat_id"]
    if Path(session).name != session or session in {".", ".."}:
        raise ValueError("FAULT-REPORTER-SESSION: plain identity required")
    directory = Path(os.environ["HOME"]) / ".local/state/pfm/herdr"
    if scope == "session":
        path = directory / (session + ".lock")
    elif scope == "activity":
        path = directory / ("activity-" + hashlib.sha256(session.encode()).hexdigest() + ".lock")
    elif scope == "bridge":
        path = directory / "pfm-herdr-bridge/transaction.lock"
    else:
        raise ValueError(f"FAULT-REPORTER-SCOPE: {scope}")
    with locked_file(path):
        yield path



def create_symlink(path, target):
    path.symlink_to(target, target_is_directory=target.is_dir())


def replace_with_symlink(path, target):
    path.unlink()
    create_symlink(path, target)


@contextmanager
def unlistable_directory(value):
    from faults import owned_path
    path = owned_path(value)
    if not path.is_dir():
        raise ValueError(f"FAULT-UNLISTABLE-NOT-DIRECTORY: {path}")
    scratch = Path(tempfile.mkdtemp(prefix="directory-unlistable-", dir=os.environ["JOURNEY_ROOT"]))
    backup = scratch / "original"
    path.rename(backup)
    try:
        create_symlink(path, path)
        yield path
    finally:
        if path.is_symlink() and os.readlink(path) == str(path):
            path.unlink()
        elif path.exists() or path.is_symlink():
            raise ValueError(f"FAULT-UNLISTABLE-CONFLICT: {path}; backup={backup}")
        backup.rename(path)
        scratch.rmdir()


@contextmanager
def file_symlink(value, target):
    from faults import owned_path
    target = owned_path(target, preserve=True)
    if not target.is_file():
        raise ValueError(f"FAULT-SYMLINK-TARGET-NOT-FILE: {target}")
    with preserved_file(value, retain_inode=True) as path:
        if not path.is_file():
            raise ValueError(f"FAULT-SYMLINK-NOT-FILE: {path}")
        replace_with_symlink(path, target)
        yield path


@contextmanager
def file_read_error(value, *, kind="directory-link"):
    if kind not in {"directory-link", "socket"}:
        raise ValueError(f"FAULT-READ-KIND: {kind}")
    with preserved_file(value, retain_inode=True) as path:
        if not path.is_file():
            raise ValueError(f"FAULT-READ-NOT-FILE: {path}")
        directory = Path(tempfile.mkdtemp(prefix="file-read-error-", dir=os.environ["JOURNEY_ROOT"]))
        try:
            with ExitStack() as scope:
                if kind == "socket":
                    path.unlink()
                    transport = scope.enter_context(socket.socket(socket.AF_UNIX, socket.SOCK_STREAM))
                    node = directory / "unreadable"
                    transport.bind(str(node))
                    node.replace(path)
                else:
                    replace_with_symlink(path, directory)
                yield path
        finally:
            directory.rmdir()


@contextmanager
def aged_files(values, age_seconds=301, *, kind="regular"):
    from faults import owned_path
    if isinstance(age_seconds, bool) or not isinstance(age_seconds, (int, float)) or not math.isfinite(age_seconds) or age_seconds <= 0:
        raise ValueError("FAULT-AGE: finite positive seconds required")
    if kind not in {"regular", "socket"}:
        raise ValueError(f"FAULT-AGE-KIND: {kind}")
    def metadata(path, stream):
        return os.fstat(stream.fileno()) if stream else path.lstat()
    def times(path, stream, ns):
        os.utime(stream.fileno() if stream else path, ns=ns, **({} if stream else {"follow_symlinks": False}))
    with ExitStack() as stack:
        originals = []
        for value in values:
            path = owned_path(value)
            stream = stack.enter_context(os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK), "rb")) if kind == "regular" else None
            before = metadata(path, stream)
            if not (stat.S_ISREG if kind == "regular" else stat.S_ISSOCK)(before.st_mode):
                raise ValueError(f"FAULT-AGE-NOT-{kind.upper()}: {path}")
            originals.append((path, stream, before))
        aged = []
        try:
            timestamp = now_ms() * 1_000_000 - int(age_seconds * 1_000_000_000)
            for path, stream, before in originals:
                times(path, stream, (before.st_atime_ns, timestamp))
                applied = metadata(path, stream)
                aged.append((path, stream, before, applied))
            yield [path for path, _, _ in originals]
        finally:
            for path, stream, before, applied in reversed(aged):
                try:
                    current = path.lstat()
                except FileNotFoundError:
                    continue
                if (current.st_dev, current.st_ino) != (applied.st_dev, applied.st_ino):
                    continue
                current = metadata(path, stream)
                if (current.st_mtime_ns, current.st_ctime_ns) == (timestamp, applied.st_ctime_ns):
                    times(path, stream, (before.st_atime_ns, before.st_mtime_ns))
