#!/usr/bin/env python3
"""Retain run-owned launch inputs and native receipts before container cleanup."""
import os
from pathlib import Path
import shutil
import tempfile

from runtime import now_ms, write_json

SOURCES = ("branches", "mock-records", "carry.json", "previous-install")


def copy_file(source, destination):
    destination = Path(destination)
    with tempfile.NamedTemporaryFile(dir=destination.parent, delete=False) as stream:
        temporary = Path(stream.name)
    try:
        shutil.copy2(source, temporary, follow_symlinks=False)
        if temporary.is_symlink():
            raise ValueError("ARTIFACT-SOURCE-REFUSED: " + str(source))
        temporary.replace(destination)
    finally:
        temporary.unlink(missing_ok=True)


def retain(root, output):
    root, output = Path(root), Path(output)
    destination = output / "root"
    copied, absent = [], []
    for name in SOURCES:
        source = root / name
        if not source.exists() and not source.is_symlink():
            absent.append(name)
            continue
        for path in (source, *source.rglob("*")) if source.is_dir() else (source,):
            if path.is_symlink() or path.name == ".credentials.json" or not (path.is_file() or path.is_dir()):
                raise ValueError("ARTIFACT-SOURCE-REFUSED: " + str(path))
        destination.mkdir(parents=True, exist_ok=True)
        if source.is_dir():
            shutil.copytree(source, destination / name, dirs_exist_ok=True, symlinks=True, copy_function=copy_file)
        else:
            copy_file(source, destination / name)
        copied.append(name)
    write_json(output / "root-artifacts.json", {"source_root": str(root), "artifact_root": "root",
                                                "copied": copied, "absent": absent, "captured_ms": now_ms()})


if __name__ == "__main__":
    retain(os.environ["JOURNEY_ROOT"], os.environ["JOURNEY_OUT"])
