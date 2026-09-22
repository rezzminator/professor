# The testing manual

A testing manual is one project's living law of testing: how its unit and integration tests are designed, where a test lives, which gates exist, the tricks and traps, what to test and what not to. One file per project, one fixed section order, three readers. It closes the hole through which flights ignored project test law: neither `flights-speccer` nor `flights-orchestrator` knew such law existed, and tests complied only where someone restated the rules by hand in a `0-` file.

Decisions live in this file. The template lives in [`templates/project/commands/per-project/testing-manual.md`](../../../templates/project/commands/per-project/testing-manual.md).

## Contents

- [What it replaces](#what-it-replaces)
- [Where it lives](#where-it-lives)
- [The sections](#the-sections)
- [Who reads what](#who-reads-what)
- [How it reaches a flight](#how-it-reaches-a-flight)
- [What stays out](#what-stays-out)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## What it replaces

In the adopter this was measured on, project test law was spread over twelve files in two trees: about 34 KB across six QA agent prompts and six child contract files, beside a 19.8 KB shared testing command of which 87% was generic mechanism. Every reader loaded the shared command plus an agent prompt to extract its own slice. The generic mechanism moves into the [flight executors](flights-executors.md) (writing tests) and [`flights-gater`](flights-gater.md) (attacking them); the project law moves into the manual; the per-project `developer` and `qa` agents go.

## Where it lives

`.claude/commands/{project}-testing-manual.md`, invoked as `/{project}-testing-manual`; a single-project repository names it `testing-manual`. A command, because it is the documentation a human also opens and because the mirrors compile commands for Codex and OpenCode. Agents read it by path; only its `description:` line rides in the chat's command listing. It is a project-tier file the install interview writes, one per roster entry — `pfm init` skips `commands/per-project/` as it skips every roster-only source — then pinned with `pfm update pin --template` and owned and kept true by the project. Target size 4 to 9 KB; the manual cites a runbook for stack detail rather than restating it.

## The sections

Fixed order and fixed headings, so a reader greps the same heading in any project's manual. A section that does not apply says `none`.

1. Tiers: what counts as unit and as integration or e2e here, and the boundary that decides.
2. Where a test lives: the owning module rule, the path convention, extend the owner before adding a file.
3. Lanes and registries: the lane or beat a capability lands with, the registry rows that land in the same change, the shared-core files with one editor.
4. Mock boundary: what may be mocked and what is always real.
5. Environments and cleanup: environment files, stack start, ports, the cleanup targets.
6. Run commands: the command per scope — affected, full — and the timeout each needs.
7. Concurrency: workers, isolation, what may run beside what.
8. Gates and floors: coverage floor, lint, type check, format, any scored gate and its thresholds.
9. Bug classes: the finding codes only this project raises.
10. Tricks and traps: the hard-won local knowledge that a newcomer would get wrong.
11. What not to test: the tests this project refuses, and what a removal takes with it.

## Who reads what

| Reader | Sections | For |
| --- | --- | --- |
| `flights-speccer` | 1, 2, 3, 8, and the removal clause of 11 | The tier decides a task's `shares`; the test home and the registry rows are `Files` entries; a floor is a `Done when` row; a removal lists its retiring tests |
| `flights-mechanical-executor`, `flights-hard-executor` | all | Writing the covering tests in the project's pattern |
| `flights-gater` | all, 5 to 9 most | Running the gate, sweeping test validity, raising the project's bug classes |

The speccer takes facts from the manual into the task file as `Decisions` and `Files` lines; it never puts the manual in a task's `reads`, since the manual would consume the task's 16,000-character budget.

## How it reaches a flight

- The caller of a flight names the project; `flights-speccer` opens that project's manual during intake and applies the four sections above. A project without a manual is a `NOTES` line in the speccer's return, never a silent skip.
- The orchestrator's brief to every executor and gater carries the manual's path as a standing rule.
- A `Done when` test row names the tier, and `Files` names the test home and every registry file: a test outside the pattern is then a task that cannot verify as `DONE`.

## What stays out

- The generic mechanism of writing and attacking tests: in the two agents.
- The cross-suite design of an integration suite — landscape, map, lanes, budgets: `/quality:integration-suite` designs it, and the manual's section 3 states the duty it leaves on every change.
- Pipeline glue (who spawns whom, where reports go): in the orchestrator.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The template | `templates/project/commands/per-project/testing-manual.md` | The eleven headings with placeholder bodies |
| This repository's own manual | `.claude/commands/pfm-testing-manual.md` | The pfm instance, drawn from `pfm/CLAUDE.md` and `docs/dev/testing/` |
| The speccer | [`flights-speccer`](flights-speccer.md) | Reads sections 1, 2, 3, 8 at intake |
| The executor and the gater | [flight executors](flights-executors.md), [`flights-gater`](flights-gater.md) | Read it whole |
| Setup | `docs/SETUP.md`, `templates/refresh-map.json` | Generation of one manual per project |
| The scaffold | `pfm/internal/professor/scaffold.go`, lane `A` (`infra/fence/lanes/A.sh`), landscape rows P4 and P13 | `commands/per-project/` is never deployed by a bare `pfm init` |
