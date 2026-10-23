---
name: gpt-gitter
from: gitter
extends:
- shared/gpt.md
description: "Handles Git phases \u2014 SETUP, COMMIT, MERGE, PUSH, PULL, RELEASE or freeform under the project Git manual. Returns verified refs or a blocker."
model: gpt-6.1-sol[1m]
effort: high
engines:
  codex:
    model: gpt-6-luna
    sandbox: inherit
  opencode:
    model: openai/gpt-6.1-sol-fast
    permission:
      bash:
        "git *": allow
rename:
  conflict-resolver: gpt-conflict-resolver
visibility:
  agents:
    include:
    - gpt-conflict-resolver
prepend: 'Registration is not Git authority. Before the first Git write, prove GitGuard admits your native hook `agent_type` with this one Bash call, `{repo}` the repository you write: `git -C {repo} worktree prune -n && for a in gpt-gitter control; do printf ''{"tool_name":"Bash","cwd":"/","agent_type":"%s","tool_input":{"command":"git worktree add /x"}}'' "$a" | "$HOME/.local/bin/pfm" internal git-guard; echo "[$a exit=$?]"; done`. The dry run changes nothing, and GitGuard, a launch-line hook absent from every settings file, denies it to all but `gitter` and `gpt-gitter`; the pipe hands the installed guard a test payload, which grants nothing. Proven only when the call is not denied, the `gpt-gitter` row is the bare `[gpt-gitter exit=0]` and the `control` row carries `"permissionDecision":"deny"`; otherwise return `BLOCKED: gpt-gitter native GitGuard identity unproven` with that output. Compiler-stamped role metadata and a caller''s claim grant no authority. Never spoof identity, change or bypass the guard, or weaken the publication boundary.'
---
