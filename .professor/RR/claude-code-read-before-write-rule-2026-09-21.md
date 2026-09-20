# RR — Claude Code's read-before-write rule for Write/Edit: triggers, read-state resets, and what the docs/changelog/issues say

Question: Claude Code's read-before-write rule for its Write and Edit tools — the "File has not been read yet. Read it first before writing to it." refusal: exactly when the check applies (new file vs existing file, a file the same agent created earlier), what resets an agent's file-read state (context compaction, a resumed session, a sub-agent or background agent being re-invoked), and what Anthropic's docs, changelog and GitHub issues say about it.

The rule is documented by Anthropic on the Claude Code Tools reference as "read-before-edit": the file must have been read **in the current conversation**, a `PARTIAL view` read does not count, and certain single-file Bash views (`cat`, `head`, `rg`, …) do count; since v2.1.208 newer models may edit an unread file when reading it would not need a permission prompt, while Opus 4.6, Haiku 4.5 and older models always require the read. The state is per-conversation bookkeeping, so it does not survive auto-compaction (open issue #85488, v2.1.220, error in 340 of 5,096 transcripts); whether it survives a resumed session or crosses into a sub-agent is documented nowhere and stayed unconfirmed.

## A. What the rule is, per Anthropic's own docs

All from the [Claude Code Tools reference](https://code.claude.com/docs/en/tools-reference), each sentence confirmed verbatim:

- "Claude reads the file in the current conversation before editing it, and a read cut short with a `PARTIAL view` notice doesn't count."
- "Claude Opus 4.6, Claude Haiku 4.5, and older models always require the read. Newer models can edit an unread file when reading it wouldn't need a permission prompt and the Read tool is available."
- "The relaxed handling of unread and changed files requires Claude Code v2.1.208 or later; before that, Claude Code refused any edit to a file it hadn't read in the conversation or that changed on disk after the read."
- Bash counts, narrowly: "Viewing a file with Bash also satisfies the read-before-edit requirement when the command is `cat`, `nl`, `bat`, `batcat`, `head`, `tail`, `sed -n 'X,Yp'`, `grep`, `egrep`, `fgrep`, or `rg` on a single file with no pipes or redirects."
- Changed-on-disk is a separate, softer path: "A file that changed on disk after Claude last read it can still be edited when `old_string` matches the current content exactly and unambiguously and Claude Code can read the file without prompting."

The changelog entry behind the v2.1.208 note is [release v2.1.208](https://github.com/anthropics/claude-code/releases/tag/v2.1.208): "Fixed the Edit tool failing on files modified after reading when the target text still matches uniquely". That release page has **no** entry about editing a file never read at all — the unread-file relaxation is documented only in the docs prose, not in that release's notes.

## B. New file vs existing file vs self-created file

- Existing file: the gate applies — this is the case every report describes.
- Brand-new / empty file: intended to be exempt, but reported broken. [Issue #17895](https://github.com/anthropics/claude-code/issues/17895) (closed as not planned, stale): "The Write tool requires a prior Read call before writing to a file. This creates a catch-22 for **new or empty files**", because "Read on empty/non-existent file doesn't register as successful read".
- A file the same session created via Bash (e.g. `touch`, a script) is **not** warm — reported in issue #53525 (v2.1.118, closed not planned) as "The same-session safety check only treats Read as a 'warm' signal" (relayed by a digger, `unquoted` by me; I did not fetch that page).
- Whether a successful Write itself warms the file for a later Edit is DISPUTED: a community reverse-engineering writeup ([markdown.engineering lesson 18](https://www.markdown.engineering/learn-claude-code/18-file-tools/)) says "readFileState updated: The new mtime and content are recorded"; issue #53525 reports the refusal anyway. Both relayed, neither verified by me.
- NotebookEdit: the Tools reference says nothing about a read requirement for it (confirmed absence). MultiEdit does not appear on that page.

## C. What resets the read state

- **Context compaction — confirmed.** [Issue #85488](https://github.com/anthropics/claude-code/issues/85488), title "Read-state is lost across auto-compaction → `File has not been read yet` on files already read", Claude Code version 2.1.220, open: "The problem is that its bookkeeping does not survive compaction, so a long session pays a redundant `Read` for every file it touches after each compact." Frequency: "the error appears in 340 of 5 096 transcript files." Maintainer reply: none visible on the fetched page (the fetch could not enumerate a comments section — treat as not established rather than proven absent).
- **Resumed session (`--continue` / `--resume`) — unconfirmed.** Two diggers found no issue or doc isolating file-read state across resume; only general "context not restored" issues (#43696, #15837, titles only). Open gap.
- **Sub-agent / Task agent — unconfirmed.** No GitHub issue, doc, or SDK page states whether a sub-agent inherits the parent's read bookkeeping. The separate-context-window architecture implies it does not, but that inference has no source behind it and is marked unverified.
- **Background agent re-invoked across turns — unconfirmed.** Nothing found tying re-invocation to read state; #63023 concerns background agents being killed, not bookkeeping.
- **External modification after a read** is a *different* error, "File has been unexpectedly modified. Read it again before attempting to write it." ([issue #10437](https://github.com/anthropics/claude-code/issues/10437)), which on Windows/MINGW fires spuriously and then degrades into the not-read error ([issue #12805](https://github.com/anthropics/claude-code/issues/12805)) — both relayed by a digger, not re-verified by me.

## D. The issue corpus and Anthropic's posture

Every issue a digger fetched is closed as not planned, closed as duplicate, or open without a visible maintainer reply: [#4230](https://github.com/anthropics/claude-code/issues/4230) (asks Write to auto-read; duplicate), [#16546](https://github.com/anthropics/claude-code/issues/16546) (model edits without reading; not planned), [#17895](https://github.com/anthropics/claude-code/issues/17895), #16182 (Windows infinite loop), #73281 (a `PARTIAL view` read failing the guard since v2.1.145 — matching the docs' own carve-out), #14964 (a model bypassed the guard with `touch` and caused data loss), and #85488. A digger's CHANGELOG sweep found no entry naming the refusal; the changelog is ~7,000 lines and was only partially retrievable, so that absence is partial, not proven.

## E. Implementation (unverified, community sources only)

The state is described as an in-memory map `readFileState`, keyed by full path, holding content plus `Math.floor(stat.mtimeMs)` and the read's offset/limit, with a mtime staleness check throwing the "unexpectedly modified" error — from [markdown.engineering](https://www.markdown.engineering/learn-claude-code/18-file-tools/) and issue #88118, both relayed by a digger and not verified against Anthropic material. Anthropic's API-level `str_replace_based_edit_tool` docs state no such precondition, suggesting the gate is a Claude Code product behaviour, not an API constraint — `unverified`. The Agent SDK permissions page documents hook/`canUseTool` ordering but never says a hook can waive the read-before-edit state check.

## Coverage

| Sub-area | Status |
| --- | --- |
| A. Trigger semantics (docs-level) | settled |
| B. New/self-created file cases | partial — new-file exemption is asserted by issue reporters, never stated by Anthropic's docs; the Write tool behavior section could not be retrieved |
| C. Read-state resets | partial — compaction settled; resume, sub-agent, background agent all open |
| D. Docs + changelog | settled for docs; partial for CHANGELOG (file too large to sweep fully) |
| E. Implementation mechanics | open — community reverse-engineering only |

## Verification

12 statements checked across 4 pages; all confirmed with quotes except: the Write tool behavior section on new files — **NOT ON PAGE** (the Tools reference fetch returned no Write tool behavior section); NotebookEdit read requirement — **NOT ON PAGE** (confirmed silence); "#85488 has no maintainer reply" — **UNCHECKED**, the fetch could not see a comments section; the v2.1.208 release notes containing any unread-file entry — **NO**, so the docs' unread-file relaxation has no matching changelog line. Issues #53525, #88118, #16182, #73281, #14964, #10437, #12805 and the markdown.engineering writeup are digger-relayed and not independently verified by me.

## Rabbit holes left open

- Whether a sub-agent inherits the parent's file-read state — asked of two diggers, no source found either way. The single biggest gap.
- Whether `--continue` / `--resume` restores read state (distinct from conversation context).
- The Write tool behavior section of the Tools reference — it exists as a cross-reference but neither the digger nor my own fetch retrieved its text; it likely holds the authoritative new-file answer.
- Which models count as "newer" for the v2.1.208 exception, and how "reading it wouldn't need a permission prompt" is determined operationally.
- MultiEdit's read requirement: absent from the Tools reference entirely.
- Whether the Windows/MINGW "unexpectedly modified" false positives share a root cause with the not-read refusal.
- A full CHANGELOG grep for the guard's history (file too large for a single fetch).
