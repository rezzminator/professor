#!/usr/bin/env python3
"""Rebase prior-release fixture units before the current prefix-aware upgrade."""
import os
from pathlib import Path
import subprocess


def systemctl(*arguments):
    subprocess.run(["systemctl", "--user", *arguments], check=True)


def main():
    prefix = os.environ["PFM_UNIT_PREFIX"]
    if prefix == "pfm":
        return
    # The previous release cannot name its units; this is fixture preparation.
    home = Path(os.environ["HOME"])
    managed = home / ".local/share/pfm/install/systemd"
    installed = home / ".config/systemd/user"
    old = sorted(managed.glob("pfm-*"))
    if not old:
        return
    systemctl("stop", *[path.name for path in old])
    for path in old:
        name = prefix + path.name.removeprefix("pfm")
        target = path.with_name(name)
        target.write_text(path.read_text().replace("pfm-", prefix + "-"))
        link = installed / name
        if link.is_symlink():
            link.unlink()
        link.symlink_to(target)
        prior = installed / path.name
        if prior.is_symlink():
            prior.unlink()
    for directory in installed.glob("*.wants"):
        for link in directory.glob("pfm-*"):
            if link.is_symlink():
                link.unlink()
    systemctl("daemon-reload")
    systemctl("enable", "--now", *[prefix + "-" + suffix for suffix in ("mcp.service", "name-sync.path", "name-sync.timer", "schedule.timer")])
    print("previous-units: PASS namespace " + prefix)


if __name__ == "__main__":
    main()
