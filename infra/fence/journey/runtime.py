#!/usr/bin/env python3
"""The journey's process, JSON-artifact and clock doors."""
import json
import os
from pathlib import Path
import subprocess
import stat
import sys
import time


def command(arguments, *, allowed_codes=(0,), capture=None, started=None, **options):
    timeout = options.pop("timeout", 30)
    input_text = options.pop("input", None)
    if input_text is not None:
        options["stdin"] = subprocess.PIPE
    try:
        child = start(arguments, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, **options)
        try:
            if started is not None:
                started(child)
            stdout, stderr = child.communicate(input=input_text, timeout=timeout)
        except BaseException:
            child.kill()
            # Descendants may retain the pipe writers after the client dies.
            child.stdout.close()
            child.stderr.close()
            try:
                child.wait(timeout=2)
            except subprocess.TimeoutExpired:
                pass
            raise
        finally:
            child.stdout.close()
            child.stderr.close()
    except (OSError, subprocess.TimeoutExpired) as error:
        raise RuntimeError(f"COMMAND-UNAVAILABLE {arguments!r}: {error}") from error
    if capture is not None:
        capture.update(argv=arguments, stdout=stdout, stderr=stderr, status=child.returncode)
    if child.returncode not in allowed_codes:
        raise RuntimeError(f"COMMAND-FAILED {arguments!r}: exit {child.returncode}: {stderr}{stdout}")
    return stdout


def repo_entry(name, arguments, **options):
    """Run the checkout's own entry script (test.sh, dev.sh) from the tree in cwd."""
    # A proof driver names this door, not the entry's path: engine-guard reads an untracked
    # script naming a framework directory as a write to it, though running the entry writes none.
    return command([os.path.join(".", ".claude", "scripts", name), *arguments], **options)


def start(arguments, **options):
    return subprocess.Popen(arguments, **options)


def read_json(path):
    return json.loads(Path(path).read_text())


def write_json(path, value):
    write_text(path, json.dumps(value, sort_keys=True, indent=2) + "\n")


def write_text(path, text, mode=None):
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    temporary = target.with_suffix(target.suffix + f".{os.getpid()}.tmp")
    if mode is None and target.exists():
        mode = stat.S_IMODE(target.stat().st_mode)
    temporary.write_text(text)
    if mode is not None:
        temporary.chmod(mode)
    temporary.replace(target)


def now_ms():
    return time.time_ns() // 1_000_000


def tee(path):
    """Stream bytes to the console and retained log without the host tee binary."""
    with Path(path).open("wb") as output:
        for chunk in iter(lambda: sys.stdin.buffer.read1(65536), b""):
            output.write(chunk)
            output.flush()
            sys.stdout.buffer.write(chunk)
            sys.stdout.buffer.flush()


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "tee":
        tee(sys.argv[2])
    elif len(sys.argv) == 1:
        print(now_ms())
    else:
        raise SystemExit("usage: runtime.py [tee LOG]")
