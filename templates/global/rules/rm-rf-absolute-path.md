# rm -rf takes a literal absolute path

Binds every shell command you run yourself: main chat, sub-agent, any model, any engine. Code you write into files (scripts, tests, Makefiles) is outside it.

- Never give `rm -rf` a variable, command substitution, glob or relative path: no `rm -rf "$DIR"`, `rm -rf "$X/build"`, `rm -rf $(…)`, `rm -rf ./out`. An unset or wrong variable deletes `/` or the wrong tree.
- Write the full absolute path, starting with `/`: `rm -rf /tmp/myapp/test/run-3`. Several targets: each named literally in one command, never looped through a variable.
- `mktemp -d` is no exception: scratch you will delete lives at a fixed literal path (`/tmp/{project}/{purpose}/`), reset and removed by that path (`rm -rf /tmp/myapp/checks && mkdir -p /tmp/myapp/checks`).
- Inspect the target (`ls -la /exact/path`) before removing it.
