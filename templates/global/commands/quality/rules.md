---
name: quality:rules
description: MANDATORY — load before writing or editing a rule file (`.claude/rules/**`, `~/.claude/rules/**`); `check [path…]` certifies each (APPROVED/REJECTED), `write <path>` drafts or restructures one. Prose → /quality:prompt; CLAUDE.md → /quality:claude-md.
argument-hint: "[check [path…] | write <path>]"
---

# Rule File Law

A rule file is a prompt the harness injects unasked. Always-on (no frontmatter) reaches every agent, sub-agents included, every turn, so the whole fleet pays each word; path-scoped (`paths:`) loads once per agent on a Read, Write or Edit of a matching file. `paths` is the only frontmatter key read, and YAML that does not parse loads the rule always-on, silently. `/quality:prompt` governs each line; this law governs the file's concern, class and shape. Engine delivery and probe evidence: `docs/design/rules/rules.md` in the Professor clone (`readlink -f ~/.claude/commands/quality/rules.md`, four directories up).

## The spine

```
---
paths:
  - "{glob}"
---

# {the law, stated as a claim}

{one sentence: whom it binds, the exception; at most one cost clause}

1. {law, in workflow order}
2. …
```

- Frontmatter only on a path-scoped rule: `paths:` alone, a YAML list, every glob double-quoted (an unquoted `*` opens a YAML alias, the parse fails, the rule goes always-on).
- A glob matches the path relative to the session's project root: `**/` matches zero or more directories, dot directories included. A `~/` glob never matches, and a user rule never fires on a file outside the project.
- H1 states the law (`Only gitter writes git`), never a topic (`Git`).
- The binding sentence names whom the rule binds and what everyone else does: a rule cannot target an agent, so every agent its class reaches reads it.
- The core is one list: numbered when its items run in order, bulleted otherwise. No `##`, nothing after the list.
- A rule holding a single law is that one item alone: no H1, no binding sentence; the item names whom it binds.

## One file per concern

- A concern's every law and its workflow order live in one rule file: never split across files, never restated in a second file, a `CLAUDE.md` or a co-loaded prompt.
- Rules sharing a trigger or an owner merge into one file.
- A path-scoped rule is its own file: its `paths:` is its scope.
- A Bash write (`sed -i`, a redirect) never loads a path-scoped rule, so a law that must bind every write to certain files is always-on.
- Class by reach: binds every agent in every session → always-on; binds work on certain files → path-scoped; one task's procedure → a command or skill; one project's convention → its `CLAUDE.md`.
- A hook-enforced invariant keeps one item naming the route the hook permits, and the rule says it is the guard where the hook is absent.

## Words

- Aggressively compact: one law per bullet, one step per numbered item, the fewest words that keep it, a precise word over a phrase (`/quality:prompt` § Compact aggressively).
- Impact by naming: the agent, tool, command, path or flag to use; a definition inline as `{term} = {meaning}`; an example only where it pins behavior (`no rm -rf "$DIR"`).
- Engine-neutral: a Claude mechanism is named with what holds on an engine without it.
- The always-on global set stays under 8,192 bytes (`engine-sync doctor --machine` reports `GLOBAL-BUDGET`).

## Modes

- No argument: the law above, applied while writing.
- `check [path…]`: read-only; no path means every `.md` under the project's `.claude/rules/` and `~/.claude/rules/`, symlinks resolved. Emit `APPROVED: {path}`, `REJECTED: {path} — checks {n,…}` with each failure in one line, or `UNREAD: {path} — {error}`; only a file read earns a verdict.
- `write <path>`: the steps below, to the law.

Checks:

1. Frontmatter: frontmatter on an always-on rule; a key other than `paths`; an unquoted glob; a `~/` glob; YAML that does not parse (`python3 -c 'import sys,yaml; yaml.safe_load(open(sys.argv[1]).read().split("---")[1])' {path}`).
2. Concern: two concerns in one file; a law of its concern held in another rule file, a `CLAUDE.md` or a co-loaded prompt (grep the concern's key nouns across both rule directories and every `CLAUDE.md` on the path).
3. Class: always-on while it binds only work on certain files and need not bind every write there, or path-scoped while it binds regardless of files or must bind every write; a one-task procedure or a one-project convention.
4. Spine: an H1 naming a topic; a multi-law rule missing its H1 or binding sentence, or a single-law rule carrying either; a core not one list; ordered items bulleted or unordered items numbered; a `##` or text after the list.
5. Words: a bullet holding two laws or a numbered item two steps; a phrase where one word carries it; rationale past one cost clause; a directive naming no tool, path, command or threshold; a date, an incident, "now / no longer".
6. Truth: an agent, command, hook or path it names that does not exist where it binds.
7. Budget: the always-on global set over 8,192 bytes.
8. Prose: the `/quality:prompt` pre-commit self-check fails.

Write steps:

1. Name the concern: its trigger and whom it binds; grep both rule directories and every `CLAUDE.md` for its key nouns; an existing home takes the edit, and rules sharing its trigger or owner merge in.
2. Pick the class; for path-scoped, write the globs project-relative and quoted.
3. Draft the spine, items in workflow order.
4. Compact until neither `/quality:prompt` pass fires.
5. Run `check`; report the byte change, each merge and each cut in one line.
