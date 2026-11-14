# The discipline layer (`templates/`)

Clone it into a repo and you get the complete agent, command, hook and script template set; `CLAUDE.md` is yours to write, and `pfm init` points you at `/law:claude-md write CLAUDE.md` for it. `docs/SETUP.md` walks an interview that substitutes your project's names into every placeholder; `docs/PLACEHOLDERS.md` is the substitution law. Every template is the live source file verbatim — never a skeleton.

The single idea underneath it is the **honest-looking absence** — an instrument that answers "nothing found" both when nothing is there and when the instrument itself is broken. The rules say it out loud:

> An empty enumeration is never a verdict.

- **One agent writes git.** `gitter` is the only agent that commits; every other agent is read-only.
- **Framework edits.** A change to `.claude/**`, a rule file, a `CLAUDE.md` or `templates/**` goes through the `pcm` agent; it loads the `/law:prompt` contract before its first edit.
- **The judge is never the thing being judged.** Verdicts are read from disk, never from a brief that asserts green.
- **The flight pipeline.** Work too large to do directly goes to one foreman, its tier picked by difficulty and ambiguity, never size: `flights:foreman-sonnet` for a clear path where the pattern is in the code, `flights:foreman` for design choices, ambiguity, unfamiliar code, an unknown cause, several modules or the toughest work. The foreman reads what the build needs and writes the whole change by its own hand, spawning a child foreman only for an area sharing no file with its own. Once every foreman on the worktree has returned, the main chat sends one `flights:lander`: it computes its review plan from the diff, spawns cheap diff-checkers, spec-checkers and usage-checkers in parallel, verifies every candidate on disk, fixes every finding, runs the gate, then the main chat lands it through the repo's `worktree.sh` (`gitter` takes every refusal). `/flights:doctor` meters the flight from transcripts and git — landed lines per million input tokens — never from what an agent said it did.
- **On-demand specifications.** The user, or the main chat on the user's request, invokes `flights:speccer` for a spec and token price. It returns to its caller, outside the build/land lifecycle; no lifecycle agent or command invokes it.
- **The persona is load-bearing.** The Professor prompt replaces the vendor system prompt; the vendor baselines are pinned by sha256 so `pfm doctor` reports `MATCHES / DRIFT / CHECK FAILED / CANNOT CAPTURE` — never silence.

Professor ships no GPT agents. `claude.gptAgents` in the machine config `pfm.config.json` (default `false`, disabled) is the switch for the engine-sync GPT-twin feature: it declares the engine-sync capability `gpt-agents`, and engine-sync installs only agents whose `requires` that capability satisfies. No agent requires it today. [gpt-gateway](https://github.com/rezzminator/gpt-gateway) is a separate model route that serves GPT model IDs over your ChatGPT login.

A legal skill shelf ships for teams that want one.

## Rules that bite

A subagent tries to run `git commit`. The `git-guard` PreToolUse hook answers:

```text
git-guard blocked `git commit -m wip`.
Only gitter writes this: spawn Agent(subagent_type: "gitter") with the repo path and the exact change. Never retry, rephrase, split or wrap the denied command to get past the guard.
```

The refusal names the right way. Its twin, `pcm-guard`, refuses a write to a `CLAUDE.md`, a `.claude/` file or a template by anyone but the `pcm` agent (or an agent the project grants a `.claude/` directory in `.claude/pcm-guard.json`), whether the Edit tools or a `sed -i` make it. That is one of the mandatory rules every install ships with: only `gitter` writes git; framework edits go through the `pcm` agent; and **an error never renders as ABSENCE** — "we failed to look" is never shown as "nothing there".
