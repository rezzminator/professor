#!/usr/bin/env python3
"""Invented repository topology; called only for a private fence HOME."""
import json
import os
import re
from pathlib import Path
import sys
from urllib.parse import urlsplit
from runtime import command


def git(directory, *arguments):
    return command(["git", "-C", str(directory), "-c", "user.name=journey", "-c", "user.email=journey@example.invalid", *arguments])


def skill_source(source):
    module = command(['go','-C',str(Path(source)/'pfm'),'list','-m','-f','{{.Path}}']).strip()
    return 'https://github.com/' + module.split('/')[1] + '/god-speed'


def source_transport(snapshot):
    canonical = skill_source(snapshot)
    origin = git(snapshot, 'remote', 'get-url', 'origin').strip()
    owner = urlsplit(origin).path.strip('/').split('/')[0]
    fixture = 'https://github.com/' + owner + '/god-speed'
    urls = sorted({url + suffix for url in (canonical, fixture) for suffix in ('','.git')})
    key = 'url.file:///opt/pfm-journey/external/god-speed.git.insteadOf'
    for index, url in enumerate(urls):
        git(snapshot, 'config', '--global', '--replace-all' if index == 0 else '--add', key, url)


def repository(path):
    path.mkdir(parents=True, exist_ok=True)
    if not (path / ".git").exists():
        git(path, "init", "-q", "-b", "main")
        (path / "README.md").write_text("Invented journey project.\n")
        git(path, "add", "README.md")
        git(path, "commit", "-q", "-m", "fixture root")
        git(path, "branch", "develop")


def private_home():
    if os.environ.get("PFM_DEV_FENCE") != "1":
        raise ValueError("projects: requires the fence")
    home = Path(os.environ["HOME"])
    root = Path(os.environ["JOURNEY_ROOT"])
    if home != root / "home":
        raise ValueError("projects: HOME is outside run root")
    return home


def linked_worktree(source, target, branch):
    if not (target / ".git").exists():
        target.parent.mkdir(parents=True, exist_ok=True)
        git(source, "worktree", "add", "-q", "-b", branch, str(target))
    actual = git(target, "rev-parse", "--path-format=absolute", "--git-common-dir").strip()
    if actual != str(source / ".git"):
        raise ValueError(f"projects: worktree {target}: expected common dir {source / '.git'}, actual {actual}")
    return str(target)


def worktree(project, name):
    home = private_home()
    if project not in {"atlas", "beacon", "cobalt"} or not re.fullmatch(r"[a-z][a-z0-9-]{0,63}", name):
        raise ValueError("projects: expected fixture project and plain worktree name")
    source = home / "work" / project
    return linked_worktree(source, source / ".worktrees" / ("journey-" + name), "journey-" + name)


def main():
    home = private_home()
    root = Path(os.environ["JOURNEY_ROOT"])
    for name in ("atlas", "beacon", "cobalt"):
        repository(home / "work" / name)
    atlas = home / "work/atlas"
    (atlas / "docs/specs").mkdir(parents=True, exist_ok=True)
    for relative, branch in (("atlas/.worktrees/flight-export", "flight-export"), ("atlas-worktrees/flight-review", "flight-review")):
        target = home / "work" / relative
        linked_worktree(atlas, target, branch)
    lantern = root / "fixtures/lantern"
    repository(lantern)
    if not (atlas / "plugins/lantern/.git").exists():
        target = atlas / "plugins/lantern"
        if target.exists():
            target.rmdir()
        git(atlas, "-c", "protocol.file.allow=always", "submodule", "add", "-q", str(lantern), "plugins/lantern")
        git(atlas, "commit", "-q", "-m", "fixture nested project")
    bench = atlas / "bench/report-desk/.professor"
    bench.mkdir(parents=True, exist_ok=True)
    (bench / "workbench.json").write_text(json.dumps({"title": "Report desk", "prompt": "report-desk.md"}) + "\n")
    (bench / "report-desk.md").write_text("Describe the invented project fixture.\n")
    repository(root / "tmp/scratch")
    actual = git(atlas / "plugins/lantern", "rev-parse", "--show-superproject-working-tree").strip()
    if actual != str(atlas):
        raise ValueError(f"projects: submodule: expected superproject {atlas}, actual {actual}")
    print("projects: PASS atlas/beacon/cobalt + worktrees/submodule/workbench/scratch")


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == 'skill-source':
        print(skill_source(sys.argv[2]))
    elif len(sys.argv) == 4 and sys.argv[1] == 'worktree':
        print(worktree(*sys.argv[2:]))
    else:
        main()
