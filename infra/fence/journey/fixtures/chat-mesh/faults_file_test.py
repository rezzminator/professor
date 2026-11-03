#!/usr/bin/env python3
"""Reversible file faults preserve actual bytes, names and links."""
import os
import errno
from pathlib import Path
import stat
import socket
import sys
import tempfile
import unittest

import faults_test
import faults
from runtime import command, now_ms, write_text


class FileFaultTests(unittest.TestCase):
    setUp = faults_test.FaultTests.setUp

    def test_unlistable_directory_restores_inode_and_retains_conflicting_writer(self):
        for action in ("complete", "error", "conflict"):
            with self.subTest(action=action):
                path = self.root / action
                path.mkdir(mode=0o750)
                (path / "settings").write_bytes(b"original settings\n")
                inode = path.stat().st_ino
                error = None
                try:
                    with faults.unlistable_directory(path) as actual:
                        self.assertEqual(path, actual)
                        self.assertEqual(str(path), os.readlink(path))
                        with self.assertRaises(OSError) as failure:
                            list(path.iterdir())
                        self.assertEqual(errno.ELOOP, failure.exception.errno)
                        if action == "error":
                            raise RuntimeError("owner failed")
                        if action == "conflict":
                            path.unlink()
                            path.mkdir()
                            (path / "settings").write_bytes(b"concurrent settings\n")
                except (ValueError, RuntimeError) as caught:
                    error = str(caught)
                if action == "conflict":
                    backups = list(self.root.glob("directory-unlistable-*/original"))
                    self.assertEqual(1, len(backups))
                    self.assertEqual(f"FAULT-UNLISTABLE-CONFLICT: {path}; backup={backups[0]}", error)
                    self.assertEqual(b"concurrent settings\n", (path / "settings").read_bytes())
                    path = backups[0]
                else:
                    self.assertEqual("owner failed" if action == "error" else None, error)
                self.assertEqual(True, path.is_dir())
                self.assertEqual(inode, path.stat().st_ino)
                self.assertEqual(0o750, stat.S_IMODE(path.stat().st_mode))
                self.assertEqual(b"original settings\n", (path / "settings").read_bytes())
        with self.assertRaisesRegex(ValueError, "^FAULT-UNLISTABLE-NOT-DIRECTORY:"):
            with faults.unlistable_directory(path / "settings"):
                self.fail("regular file treated as directory")

    def test_file_symlink_restores_original_inode_and_preserves_referents(self):
        for shape in ("regular", "symlink"):
            for self_link in (False, True):
                with self.subTest(shape=shape, self_link=self_link):
                    directory = self.root / (shape + str(self_link))
                    directory.mkdir()
                    path, original, other = [directory / name for name in ("settings", "original", "other")]
                    original.write_bytes(b"original\x00\xff\n")
                    original.chmod(0o640)
                    other.write_bytes(b"other settings\n")
                    if shape == "symlink":
                        path.symlink_to("original")
                    else:
                        path.write_bytes(b"original\x00\xff\n")
                        path.chmod(0o640)
                    inode = path.lstat().st_ino
                    target = path if self_link else other
                    with self.assertRaisesRegex(RuntimeError, "^owner failed$"):
                        with faults.file_symlink(path, target) as actual:
                            self.assertEqual(path, actual)
                            self.assertEqual(str(target), os.readlink(path))
                            if self_link:
                                with self.assertRaises(OSError) as failure:
                                    path.read_bytes()
                                self.assertEqual(errno.ELOOP, failure.exception.errno)
                            else:
                                self.assertEqual(b"other settings\n", path.read_bytes())
                                self.assertEqual(other, path.resolve(strict=True))
                            raise RuntimeError("owner failed")
                    self.assertEqual(inode, path.lstat().st_ino)
                    self.assertEqual(shape == "symlink", path.is_symlink())
                    if shape == "symlink":
                        self.assertEqual("original", os.readlink(path))
                    for restored in (path, original):
                        self.assertEqual(b"original\x00\xff\n", restored.read_bytes())
                        self.assertEqual(0o640, stat.S_IMODE(restored.stat().st_mode))
                    self.assertEqual(b"other settings\n", other.read_bytes())
        with tempfile.TemporaryDirectory(prefix="journey-symlink-foreign-") as foreign:
            foreign_path = Path(foreign) / "settings"
            foreign_path.write_bytes(b"foreign\n")
            for target in (foreign_path, directory):
                with self.assertRaises(ValueError):
                    with faults.file_symlink(path, target):
                        self.fail("foreign or directory target accepted")
                self.assertEqual(inode, path.lstat().st_ino)
                self.assertEqual(b"foreign\n", foreign_path.read_bytes())

    def test_aged_socket_keeps_real_inode_and_restores_unless_product_removes_it(self):
        directory = self.root / "not-socket"
        directory.mkdir()
        for removed in (False, True):
            with self.subTest(removed=removed), socket.socket(socket.AF_UNIX) as endpoint:
                path = self.root / ("socket-" + str(removed))
                endpoint.bind(str(path))
                before = path.stat()
                with self.assertRaisesRegex(RuntimeError, "^owner action failed$"):
                    with faults.aged_files([path], age_seconds=3601, kind="socket") as actual:
                        self.assertEqual([path], actual)
                        observed = path.stat()
                        self.assertEqual((True, before.st_ino), (stat.S_ISSOCK(observed.st_mode), observed.st_ino))
                        self.assertGreater(now_ms() * 1_000_000 - observed.st_mtime_ns, 3600_000_000_000)
                        if removed:
                            path.unlink()
                        raise RuntimeError("owner action failed")
                if removed:
                    self.assertEqual(False, path.exists())
                else:
                    after = path.stat()
                    self.assertEqual((before.st_ino, before.st_atime_ns, before.st_mtime_ns),
                                     (after.st_ino, after.st_atime_ns, after.st_mtime_ns))
                    with self.assertRaisesRegex(ValueError, "^FAULT-AGE-NOT-SOCKET:"):
                        with faults.aged_files([directory], kind="socket"):
                            self.fail("directory accepted as socket")

    def test_read_error_is_a_non_directory_walk_entry_and_restores_original(self):
        probe = ("import errno,os,pathlib,sys; entry=next(e for e in os.scandir(sys.argv[1]) if e.name=='transcript.jsonl'); "
                 "print('directory='+str(entry.is_dir(follow_symlinks=False)));\n"
                 "try: pathlib.Path(entry.path).read_bytes()\nexcept OSError as error: print('read='+('socket-refused' if error.errno in (errno.ENXIO,errno.EOPNOTSUPP) else str(error.errno)))")
        for linked, kind in ((False, "directory-link"), (True, "directory-link"), (False, "socket"), (True, "socket")):
            with self.subTest(linked=linked, kind=kind):
                directory = self.root / ("read-error-" + str(linked) + "-" + kind)
                directory.mkdir()
                path, referent = directory / "transcript.jsonl", directory / "original"
                referent.write_bytes(b"original transcript\n")
                referent.chmod(0o640)
                if linked:
                    path.symlink_to("original")
                else:
                    path.write_bytes(b"original transcript\n")
                    path.chmod(0o640)
                inode = path.lstat().st_ino
                with self.assertRaisesRegex(RuntimeError, "^owner action failed$"):
                    with faults.file_read_error(path, kind=kind) as actual:
                        self.assertEqual(path, actual)
                        expected = "directory=False\nread=" + ("21" if kind == "directory-link" else "socket-refused") + "\n"
                        self.assertEqual(expected, command([sys.executable, "-c", probe, str(directory)]))
                        self.assertEqual(kind == "socket", stat.S_ISSOCK(path.lstat().st_mode))
                        self.assertEqual(b"original transcript\n", referent.read_bytes())
                        raise RuntimeError("owner action failed")
                self.assertEqual(b"original transcript\n", path.read_bytes())
                self.assertEqual(0o640, stat.S_IMODE(path.stat().st_mode))
                self.assertEqual(linked, path.is_symlink())
                self.assertEqual(inode, path.lstat().st_ino)
                if linked:
                    self.assertEqual("original", os.readlink(path))
                    self.assertEqual(inode, path.lstat().st_ino)
        absent = self.root / "absent-transcript"
        with self.assertRaisesRegex(ValueError, "^FAULT-READ-NOT-FILE:"):
            with faults.file_read_error(absent):
                self.fail("absent transcript became a manufactured read failure")
        self.assertEqual(False, absent.exists())

    def test_aged_files_restore_untouched_timestamps_and_keep_product_changes(self):
        original_times = (1_400_000_000_000_000_000, 4_000_000_000_000_000_000)
        refreshed_times = (1_500_000_000_000_000_000, 1_600_000_000_000_000_000)
        for action in ("complete", "error", "replace", "refresh", "delete"):
            with self.subTest(action=action):
                directory = self.root / ("age-" + action)
                directory.mkdir()
                paths = [directory / "cache", directory / "cache.attempt"]
                for path in paths:
                    path.write_text("original")
                    os.utime(path, ns=original_times)
                error = None
                try:
                    with faults.aged_files(paths) as actual:
                        self.assertEqual(paths, actual)
                        self.assertEqual([True, True], [now_ms() * 1_000_000 - path.stat().st_mtime_ns > 300_000_000_000 for path in paths])
                        if action == "replace":
                            write_text(paths[0], "refreshed")
                            os.utime(paths[0], ns=refreshed_times)
                        elif action == "refresh":
                            paths[0].write_text("refreshed")
                            os.utime(paths[0], ns=refreshed_times)
                        elif action == "delete":
                            paths[0].unlink()
                        elif action == "error":
                            raise RuntimeError("owner action failed")
                except RuntimeError as caught:
                    error = str(caught)
                self.assertEqual("owner action failed" if action == "error" else None, error)
                expected = refreshed_times if action in ("replace", "refresh") else original_times
                if action == "delete":
                    self.assertEqual(False, paths[0].exists())
                else:
                    self.assertEqual(expected, (paths[0].stat().st_atime_ns, paths[0].stat().st_mtime_ns))
                self.assertEqual(original_times, (paths[1].stat().st_atime_ns, paths[1].stat().st_mtime_ns))
                with self.assertRaises(ValueError):
                    with faults.aged_files([paths[1], self.root / "home"]):
                        self.fail("non-regular aging target accepted")
                self.assertEqual(original_times, (paths[1].stat().st_atime_ns, paths[1].stat().st_mtime_ns))

    def test_file_scopes_restore_bytes_mode_absence_and_same_symlink_after_rename(self):
        for shape in ("regular", "absent", "symlink", "dangling"):
            for temporary in (False, True, "untouched"):
                with self.subTest(shape=shape, temporary=temporary):
                    directory = self.root / (shape + str(temporary))
                    directory.mkdir()
                    path, referent = directory / "profile", directory / "referent"
                    if shape in ("regular", "symlink"):
                        referent.write_bytes(b"original\x00\xff\n")
                        referent.chmod(0o640)
                    if shape == "regular":
                        path.write_bytes(b"original\x00\xff\n")
                        path.chmod(0o640)
                    elif shape in ("symlink", "dangling"):
                        path.symlink_to("referent")
                    inode = path.lstat().st_ino if path.is_symlink() else None
                    scope = faults.temporary_text(path, "replacement\n", 0o700) if temporary is True else faults.preserved_file(path, retain_inode=temporary == "untouched")
                    with self.assertRaisesRegex(RuntimeError, "^owner action failed$"):
                        with scope as actual:
                            self.assertEqual(path, actual)
                            if temporary is False:
                                command(["bash", "-c", 'printf "replacement\\n" > "$1.next" && chmod 700 "$1.next" && mv "$1.next" "$1"', "profile-updater", str(path)])
                            if temporary != "untouched":
                                self.assertEqual(b"replacement\n", path.read_bytes())
                                self.assertEqual(0o700, stat.S_IMODE(path.stat().st_mode))
                                self.assertEqual(False, path.is_symlink())
                            raise RuntimeError("owner action failed")
                    if shape == "regular":
                        self.assertEqual(b"original\x00\xff\n", path.read_bytes())
                        self.assertEqual(0o640, stat.S_IMODE(path.stat().st_mode))
                    elif shape == "absent":
                        self.assertEqual(False, path.exists() or path.is_symlink())
                    else:
                        self.assertEqual(True, path.is_symlink())
                        self.assertEqual("referent", os.readlink(path))
                        self.assertEqual(inode, path.lstat().st_ino)
                    if shape in ("regular", "symlink"):
                        self.assertEqual(b"original\x00\xff\n", referent.read_bytes())
                        self.assertEqual(0o640, stat.S_IMODE(referent.stat().st_mode))
                    else:
                        self.assertEqual(False, referent.exists())
        with faults.temporary_text(path.parent / "referent", "default-mode"):
            self.assertEqual("default-mode", (path.parent / "referent").read_text())

    def test_file_scopes_refuse_foreign_symlink_referent_without_mutation(self):
        with tempfile.TemporaryDirectory(prefix="journey-foreign-") as other:
            foreign = Path(other) / "profile"
            foreign.write_bytes(b"foreign\n")
            link = self.root / "foreign-link"
            link.symlink_to(foreign)
            for scope in (faults.preserved_file(link), faults.temporary_text(link, "refused")):
                with self.assertRaises(ValueError):
                    with scope:
                        self.fail("foreign referent accepted")
                self.assertEqual(str(foreign), os.readlink(link))
                self.assertEqual(b"foreign\n", foreign.read_bytes())

    def test_missing_file_restores_on_exit_and_retains_both_versions_on_conflict(self):
        for action in ("complete", "error", "conflict", "link-conflict"):
            with self.subTest(action=action):
                path = self.root / action
                path.write_bytes(b"observed transcript\n")
                path.chmod(0o640)
                error = None
                try:
                    with faults.temporarily_missing_file(path) as actual:
                        self.assertEqual(path, actual)
                        self.assertEqual(False, path.exists())
                        if action == "error":
                            raise RuntimeError("owner action failed")
                        if action == "conflict":
                            path.write_bytes(b"concurrent replacement\n")
                        elif action == "link-conflict":
                            path.symlink_to("missing-referent")
                except (ValueError, RuntimeError) as caught:
                    error = caught
                if action in ("conflict", "link-conflict"):
                    self.assertIsInstance(error, ValueError)
                    backup = Path(str(error).split("; backup=", 1)[1])
                    self.assertEqual(f"FAULT-MISSING-CONFLICT: {path}; backup={backup}", str(error))
                    self.assertEqual(self.root, backup.parent.parent)
                    self.assertEqual("original", backup.name)
                    self.assertEqual(b"observed transcript\n", backup.read_bytes())
                    self.assertEqual(0o640, stat.S_IMODE(backup.stat().st_mode))
                    if action == "conflict":
                        self.assertEqual(b"concurrent replacement\n", path.read_bytes())
                    else:
                        self.assertEqual("missing-referent", os.readlink(path))
                else:
                    self.assertEqual("owner action failed" if action == "error" else None, str(error) if error else None)
                    self.assertEqual(b"observed transcript\n", path.read_bytes())
                    self.assertEqual(0o640, stat.S_IMODE(path.stat().st_mode))
        link = self.root / "missing-link"
        link.symlink_to("complete")
        for path in (link, self.root / "absent", self.root / "home"):
            with self.subTest(refused=path), self.assertRaisesRegex(ValueError, "^FAULT-(MISSING-NOT-REGULAR|PATH):"):
                with faults.temporarily_missing_file(path):
                    self.fail("missing-file scope accepted a non-regular resource")



if __name__ == "__main__":
    unittest.main()
