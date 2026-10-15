# templates/generated

Engine-sync output, never edited by hand.

- `agents/`: one generated agent per `from:` declaration in `templates/global/agents/`, each the render of its declaration. Line 2 of a render names its declaration and the command that rebuilds it, `engine-sync global`.
- Originals have no render: they link straight from `templates/global/agents/` into `~/.claude/agents/`. A shared file in `templates/global/agents/shared/` is never an agent.
- Commands and skills have no declaration, so nothing renders for them here. pfm links them from `templates/global/{commands,skills}/` into the Claude config's `commands/` and `skills/`; at `pfm install`, engine-sync writes each command's Codex and OpenCode form into that engine's home as a regular file, linking a skill-directory command into Codex's `skills/`. The skills reach neither engine's home. None of these is tracked.
- The fence step `templates.generated-agents` holds `agents/` equal to a fresh render.
