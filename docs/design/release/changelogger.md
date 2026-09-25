# changelogger

`changelogger` writes one release's notes for the adopter who updates onto it: `releases/v{NEW}.md`, the line prepended to `CHANGELOG.md`, and the coverage ledger that proves every commit in the range was accounted for. It writes and revises; it never judges its own note. It does run the grammar and coverage rules of `release-check notes` before it returns, so a round trip with the `releaser` is spent on judgment rather than on a malformed line; the verdict stays the `releaser`'s, which runs the full check with the version and command rules. The grammar it writes is public and lives in `docs/RELEASE.md` § Release notes; this file holds why the grammar is what it is and how the agent reaches a note that grammar accepts. Family decisions live in [release.md](release.md).

## Contents

- [What the reader needs](#what-the-reader-needs)
- [Delivery routes](#delivery-routes)
- [Grounding](#grounding)
- [The traps](#the-traps)
- [The coverage ledger](#the-coverage-ledger)
- [Grammar decisions](#grammar-decisions)
- [Revise mode](#revise-mode)
- [Input and return](#input-and-return)

## What the reader needs

The reader is the adopter's update chat ([release.md § The reader](release.md#the-reader)): a model that merges every skipped release's actions into one checklist and follows it literally. For each change, it needs four answers, and a note that leaves one to inference has failed it:

1. What changed, as the adopter sees it: a command, a file in their project, a behavior, a cost — never the implementation.
2. How it reaches them: the delivery route, which decides whether they must do anything at all.
3. What to do, when, and on what: one action line per step, with its timing and its surface.
4. How to see it done, when that can be checked: the command whose output shows it.

Across releases it needs two more: which release is a required stop, and which later action supersedes an earlier one on the same surface.

## Delivery routes

The bullet label names the route by which a change reaches an adopter, because the route decides the action. The labels are a closed set so the check can enforce them and the reader can sort by them.

| Label | Paths | Reaches the adopter | An action is needed when |
| --- | --- | --- | --- |
| `Global` | `templates/global/**`, `workflows/**` | the moment the clone moves: machine-global originals are symlinked live; Codex roles recompile at the update's `pfm install` | something outside the clone must change: a setting, a file the installer does not remove, a command the adopter types by habit |
| `Project` | `templates/project/**` | never by itself: `pfm update check` reports `UPDATED`, `NEW`, `GONE-UPSTREAM`, and the adopter hand-applies and pins | always: the bullet names the template path and whether to adopt; the action is `per project` |
| `pfm` | `pfm/**`, including the embedded fleet prompt and installer assets | with `pfm update`'s rebuild and install | a config key, hook, environment variable, installed file, command or flag is added, renamed or removed |
| `Repo` | everything else: CI, docs, infra, scripts, tests | never at runtime | never; the bullet is informational and short |

A change on two routes is two bullets, one per route.

## Grounding

Three sources, in order:

1. The commit messages of the range: the author wrote them when the change was made, and they carry the why that a model reading a diff misses (Evidence in [release.md](release.md#evidence)). The project contract requires an adopter-facing change to say so in its message.
2. The diff, for the surfaces: which template paths, global roles, commands, flags, config keys and hooks changed; `DIR/scope.md` lists the paths and the removed names.
3. The code, to settle a question the first two leave open: what the installer does with a symlink whose original was deleted, whether `pfm config validate` rejects a key that no longer exists, whether the update prints the new line or the old one. A question like that goes to a `tracer` with the exact code question; a note never asserts an adopter-side effect it did not read.

Every bullet traces to the commits the ledger gives it. A statement no commit supports is not written.

## The traps

- The previous binary runs the update. Whatever the update prints, checks or migrates on the way into this release is the previous release's code. A change to the update path shows first on the next update, and the note says so where an adopter would otherwise look for it now.
- Removal leaves residue. Deleting a global agent, a hook or a config key upstream does not delete it from an adopter's machine unless the installer does; the note names the residue and the action that clears it, or says the installer clears it, after reading which.
- A rename is a removal plus an addition to an adopter's habits: the old name stops working the moment the clone moves.
- A project template the adopter customized is hand-applied, never replaced: the bullet says what the change is for, so the adopter can merge intent, not text.
- A required stop. When the update path changes in a way the previous binary cannot carry across — a migration the older code would undo, a state it cannot read — the note declares this release a stop, and every later release's reader lands on it first.

## The coverage ledger

`DIR/notes/coverage.tsv`, one row per non-merge commit in the range, tab-separated:

```text
{sha7}	{section}	{Label}: {scope}
{sha7}	none	{reason}
```

A commit reaches a bullet by naming the bullet's section and its label-and-scope text verbatim; many commits may name one bullet. A commit that reaches no adopter says why in words: a test-only change, an internal refactor with no behavior change, a fix of a defect that never shipped because it came and went inside this range. `release-check notes` fails a commit missing from the ledger, a row naming a commit outside the range, a row naming a bullet that does not exist, and a `none` without a reason. The ledger is what turns omission, the measured failure of release notes, into a check that goes red.

## Grammar decisions

- Actions carry a timing, one of `before update`, `after update`, `per project`. The reader already sorts actions into before and after; a timing in the text replaces a guess with a token. `per project` is the third timing because project actions run in each adopted project, after the machine update, alongside `pfm update check`.
- Actions carry a surface: the path, key, command or template the action touches. A later release's action on the same surface supersedes an earlier one only when the reader can see they share it.
- Actions ride under their bullet, so the reader holds the why beside the what to do.
- `#### → Stop:` sits under the lead paragraph, before any section: it changes the order of the whole update, not one bullet's.
- `## Breaking` and `## Migration` come first, then `Added`, `Changed`, `Fixed`, `Removed`, then `## Verification`. An upgrader reads top-down and stops early; the part that can break them is on top.
- A bullet is one line, so a grep hit is a whole record and the ledger can name it by its text.
- The action marker keeps its spelling, `#### → For:`, so the update prompt already in adopters' binaries still finds every action.
- `(opt-in)` and `(cost)` end a bullet's change text, never a label or a scope: an adopter may skip an opt-in addition, and a cost — an environment, hook, permission or model-configuration change — is named before the adopter pays it. N5 reads them as part of the change.

## Revise mode

Spawned with the existing note and a list of items — the check's failure lines, a rehearsal friction routed `notes`, commits that landed after the first write. It changes what the items require and nothing else: a revision that rewrites unrelated bullets makes the next review of the note start over. It updates the ledger for every new commit.

## Input and return

Input: the candidate worktree, `BASE`, HEAD, `NEW`, `PREV`, `BUMP`, `SUMMARY`, the release directory, the review reports' paths; in revise mode, the items.

Return:

```text
DONE notes | BLOCKED notes: {question}
BULLETS {section} {n} …
ACTIONS before {n} · after {n} · per-project {n} · stops {n}
LEDGER {commits} commits · {bulleted} to bullets · {none} none
TRACER {questions asked} — {answers used}
RETRO {lesson} | none
```
