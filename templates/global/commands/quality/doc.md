---
name: quality:doc
description: MANDATORY — load before writing or restructuring any reference doc under docs/ (root or child project), and to certify one via the Approval gate (APPROVED/REJECTED); owns doc SHAPE — cluster + _index.md, ≤500-line topic files, table-vs-sections, grep-true headings, current-state only. Prose → /quality:prompt; a `description:` → /quality:description; markdown mechanics → /quality:md-forlint.
---

# Doc Format

Reference docs under `docs/` are read by LLM agents (whole-file `Read`, `grep`), not by humans in a rendered viewer. Shape them for that reader, at write-time.

**When to load:** the main-loop session loads this before writing any permanent reference doc. Load it yourself before hand-editing or restructuring `docs/agents/*`, child `*/docs/*`, or any large reference doc.

## The deciding principle

Format choice barely affects whether the model _understands_ the content — model capability dominates. Decide on the mechanics the reader actually pays for: token cost, grep context, edit/diff locality, formatter stability. Optimize those; comprehension takes care of itself.

## The cluster model

A reference doc is a cluster — a directory, not a monolith. A consumer reads `_index.md` (cheap), then opens the one topic file it needs; two cheap reads replace one impossible one.

- `_index.md`: navigation only — a pointer table `| Topic | File | Covers |` listing exactly the topic files on disk, ≤150 lines, no prose.
- Topic file: one self-contained slice, readable in one `Read`. Target ≤500 lines, hard cap ~80 KB. Table of Contents at the top of any topic file over ~100 lines, so a partial read still shows its scope.
- Split when ANY holds: over ~500 lines; covers more than one subject; sections have different edit cadence; the content reads as a per-pipeline append-log (`New X (flight-23)`) rather than current state. A file between the target and the cap is a split that hasn't happened yet.
- Split by moving the largest self-contained section into a sibling topic file and registering it in `_index.md`.

## Record format — table vs sections

The highest-leverage rule. Decide by field shape, not habit:

- Short, uniform cells (port maps, access matrices, the `_index.md` pointer tables themselves) → markdown table: genuinely tabular, no padding waste, one grep hit shows the whole record on one line.
- Any long free-text field (descriptions, rationale, prose) → heading-per-record sections: one `###` per record, a one-line bold metadata strip for the short fields (`**Projects:** api, web — **Status:** Active`), then the long field as a prose paragraph.

Long prose belongs in a section, never a table cell. A 600-char cell forces its column that wide for every other row, so editing one record reflows the whole column into a giant diff and a grep hit drags the padding with it. Sections keep a one-record edit local and give each record its own greppable `###` anchor. (Column padding itself is stripped by the format policy — `/quality:md-forlint` — so a genuinely tabular table stays compact; the rule above is about edit locality and grep, not about padding.)

## Edit locality

A change to one record touches only that record's lines — zero reflow of its neighbors. This is the rule behind sections-over-tables, delete-don't-annotate, and one-record-per-`###`. If editing one fact rewrites unrelated lines, the format is wrong.

## Current-state only — delete, don't annotate

A reference doc describes what IS, now. When a record is removed, delete it — no `~~strikethrough~~`, no "Removed {date}" / "Deprecated" / "Added in flight-N" note, and no grouping of records by the build that added them. Stale annotations poison retrieval: the agent reads a dead endpoint as real and builds on it. Rationale prose ("Background", "Why we chose X in 2024") goes the same way — encode the current rule, drop the story. History lives in `git log` and epic manifests.

Authorship follows the same law: no `> Author:` / `> Last updated:` / `> Flight:` byline. Git owns authorship and last-edited date; the path owns ownership (root `docs/agents/` and each project's `docs/` → the main-loop session; `docs/business/**` → its owning command).

## Name fidelity — docs are grep-true

Every identifier is the exact code/DB name, verbatim: a table or column is its database name as the schema spells it (never the ORM's mapped field name), an API operation its schema name, a component/chain/queue its source symbol. When a record maps to a code symbol, the `###` heading IS that symbol (`### updateSubscription`, `### user_session_events`) — the grep landmark and the name are one string. Claude Code's grep is exact-match (ripgrep, no fuzzy), so a heading that paraphrases the symbol is invisible to the search that would find it. When the code renames, the doc renames in the same edit.

## Navigation contract — one hop

A topic file is self-contained. Point to another cluster only for the authoritative source; when a record depends on another cluster's detail, inline the essential fact instead of sending the reader on a doc → doc → doc chase (each extra read costs tokens and reasoning steps, and the agent often stops before reaching the end).

Consumer routes: one operation or contract → grep the cluster, read the matching topic file. One subsystem, or whole-domain context → read the cluster `_index.md`, then the topic files that matter.

When a split moves a doc that consumers reference by its old path, leave a one-line redirect stub at the old path naming the new `{cluster}/_index.md`, until those consumers are repointed.

## Finish

Run `rumdl fmt <file>` from the repo root on everything touched (`/quality:md-forlint`). The format hook covers Edit/Write on root-owned paths (`CLAUDE.md`, `.claude/`, `docs/`); child-project docs (a sub-project's own `docs/`) and Bash-written files need the manual run.

## Approval — certify a document

Every reference doc must pass this gate before it is considered done; run it over an existing doc, not just at write-time. A doc is **APPROVED** only when ALL hold; otherwise it is **REJECTED** with the failing checks named, and the fix is applied before re-checking.

- 1 Size: a topic file is >500 lines (split) or any file >80 KB.
- 2 ToC: a file >100 lines lacks a top Table of Contents.
- 3 Record format: long prose sits in a table cell instead of a `###` section.
- 4 Grep-true: a `###` heading paraphrases a code symbol the record maps to, instead of being that symbol verbatim.
- 5 Current-state: a tombstone, `~~strikethrough~~`, "removed/added/deprecated {date or flight}" note, or per-pipeline changelog framing is present.
- 6 One hop: a record sends the reader on a doc → doc → doc chase instead of inlining the essential fact.
- 7 Index: the cluster `_index.md` does not list exactly the files on disk.
- 8 Byline: a `> Author:` / `> Last updated:` / `> Flight:` line is present.
- 9 Mechanics: `/quality:md-forlint check <path>` reports a dead relative link (MD057), a dead or stale anchor (MD051 — a ToC entry included), or a byline term (MD061). Run it; a check that was not run is not a pass.

Emit the verdict per doc as `APPROVED: {path}` or `REJECTED: {path} — checks {n,…}`. A cluster is approved only when its `_index.md` and every topic file are approved.
