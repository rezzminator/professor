# RR — Claude Code skills after compaction
Question: does Claude Code's own context compaction (manual /compact and automatic auto-compact) lose skills that were invoked earlier in the session — i.e. after the compaction, is the invoked skill's body (SKILL.md content) still in the model's context, re-attached, or summarized away?

Partly: current documented Claude Code re-attaches invoked skill bodies after both manual `/compact` and automatic compaction—not merely their summaries—but retains only the first 5,000 tokens per skill within a 25,000-token combined budget, dropping older skills first ([official context documentation](https://code.claude.com/docs/en/context-window), [skills lifecycle](https://code.claude.com/docs/en/skills#skill-content-lifecycle)). Separately, v2.1.304 restored automatic re-sending of the available skill list; that change concerns discovery, not the introduction of body retention ([CHANGELOG](https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md)).

## Evidence map

### 1. Documented lifecycle: body versus list versus summary

The official skills documentation states:

> “When you or Claude invoke a skill, the rendered `SKILL.md` content enters the conversation as a single message and stays there across later turns.”

> “When the conversation is summarized to free context, Claude Code re-attaches the most recent invocation of each skill after the summary, keeping the first 5,000 tokens of each. Re-attached skills share a combined budget of 25,000 tokens. Claude Code fills this budget starting from the most recently invoked skill, so older skills can be dropped entirely after compaction if you have invoked many in one session.”

[Source: official skills lifecycle](https://code.claude.com/docs/en/skills#skill-content-lifecycle).

The context-window documentation removes the possible ambiguity that the skills page introduces this under auto-compaction:

> “The automatic pass works the same way as the `/compact` step in the timeline.”

Its preservation table states:

> “Invoked skill bodies | Re-injected, capped at 5,000 tokens per skill and 25,000 tokens total; oldest dropped first”

> “Skill descriptions | Rebuilt from your current skills and re-injected. Requires Claude Code v2.1.304 or later”

[Source: official context-window documentation](https://code.claude.com/docs/en/context-window).

**Thus, full retention is not guaranteed.** The documented selection unit is the most recent invocation of each skill, not every historical invocation. Prefix truncation is explicit: “Truncation keeps the start of the file, so put the most important instructions near the top of `SKILL.md`.” These pages specify token budgets, not a fixed maximum number of re-attached skills. [Skills](https://code.claude.com/docs/en/skills#skill-content-lifecycle), [context window](https://code.claude.com/docs/en/context-window).

Do not confuse this with the skill catalog: “In a regular session, skill descriptions are loaded into context so Claude knows what's available, but full skill content only loads when invoked.” [Skills](https://code.claude.com/docs/en/skills).

### 2. Version history: exactly what changed

Verbatim changelog entries:

- **2.1.45:** “Fixed skills invoked by subagents incorrectly appearing in main session context after compaction” — this establishes that child-only invocation should not leak into the parent through compaction; it does not establish a particular internal registry implementation. [Versioned CHANGELOG](https://raw.githubusercontent.com/anthropics/claude-code/v2.1.119/CHANGELOG.md).
- **2.1.119:** “Fixed skills invoked before auto-compaction being re-executed against the next user message” — retained instructions should not be mistaken for a fresh invocation against a later message. [Versioned CHANGELOG](https://raw.githubusercontent.com/anthropics/claude-code/v2.1.119/CHANGELOG.md).
- **2.1.299:** “Improved `/reload-skills` to say it re-sends the full skill list to Claude, which is how to get skills back after compaction” — this is about the listing. [Current CHANGELOG](https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md).
- **2.1.304:** “Changed compaction to send Claude the skill list again afterwards; `/reload-skills` now sends only new or changed skills” — automatic listing restoration. [Current CHANGELOG](https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md).

**The introduction version for invoked-body reattachment could not be established.** Two changelog digs found the above fixes but no explicit introduction entry; whole-document analysis timed out, although targeted earlier slices were read. Therefore this research does not assert a universal pre-/post-version boundary for body retention. In particular, **2.1.304 is not evidence that bodies first began surviving then**.

A concrete historical explanation in issue #74990, discussing reproduction on v2.1.233, says:

> “Compaction deliberately does not re-send the skills listing (it costs a few thousand tokens per compaction and the reasoning was that the invoked-skill bodies, which are carried over, would be enough).”

The author is bcherny. The fetched closure was the bot message “Closing for now — inactive for too long,” not a demonstrated fix. [Issue/comment](https://github.com/anthropics/claude-code/issues/74990#issuecomment-5303443158).

### 3. Mechanism, limits, and conflicting explanations

The official contract is **re-attachment after the summary**, within the limits above. The literal attachment name **`invoked_skills`** appears in reporter reproductions, including the budget-boundary fixture below; it should not be treated as a separately documented public API. [Skills lifecycle](https://code.claude.com/docs/en/skills#skill-content-lifecycle), [#96168](https://github.com/anthropics/claude-code/issues/96168).

**Subagents:** the 2.1.45 fix explicitly calls child-skill inclusion in the main session after compaction incorrect. A skill invoked only inside a child should therefore not be counted as a parent invocation merely because compaction occurs. The exact registry ownership and treatment of every fork/preload variant remain unverified. [CHANGELOG](https://raw.githubusercontent.com/anthropics/claude-code/v2.1.119/CHANGELOG.md).

**DISPUTED historical explanation:** in #72549, bcherny, shown with a “Collaborator” badge, wrote on August 17, 2026:

> “On the mechanism: a skill body is not re-injected after compaction or on resume.”

The same comment reports unsuccessful reproduction on 2.1.233 (Linux), after invoking an AFK-mode skill and explicitly disabling that mode before compaction. It adds: “So the only thing that can carry a mode across a compaction is the summary itself”. This is a general body-retention explanation, not merely a claim about skill descriptions. It conflicts with the explicit current documentation and the same author's carried-over-bodies explanation in #74990. No verified version transition reconciles them; do not use that comment as the current contract. Original reported version: 2.1.196. Issue closed “not planned”; later locked as resolved. [#72549 comment](https://github.com/anthropics/claude-code/issues/72549#issuecomment-5310642923), [contrary explanation](https://github.com/anthropics/claude-code/issues/74990#issuecomment-5303443158), [current documentation](https://code.claude.com/docs/en/skills#skill-content-lifecycle).

### 4. GitHub failure reports and their status

These are **reporter observations**, not independently reproduced tests or maintainer-confirmed fixes. States below are the fetched states at research time.

| Issue | Version / state | Direct evidence and response |
| --- | --- | --- |
| [#96168 — Compaction silently omits older invoked skills above the 25,000-unit aggregate budget; --resume reconstructs none](https://github.com/anthropics/claude-code/issues/96168) | “Claude Code 2.1.280 (Linux)”; **Closed as not planned**, closed by the reporter | “At 25,001 units, the oldest skill A is absent from `invoked_skills` without a marker or error; B–F remain. The 25,000-unit fixture retains all six.” Explicit limitation: “Auto-compaction and `--continue` were not runtime-tested.” No maintainer reply was visible; visible activity was reporter closure and bot labeling. This supports conditional omission, not universal loss. |
| [#83306 — Post-compaction skill reminders present stale skill content as currently binding](https://github.com/anthropics/claude-code/issues/83306) | “Claude Code 2.1.220, Linux”; **Open** | “The injected text was the skill as it read at invocation time, not the current file. There is no indication that the file has changed since.” This reports retained-but-stale content, not disappearance. No identified maintainer reply was visible; bcherny added a label, which is not a reply. |
| [#94564 — Invoked skill not re-attached after manual /compact; only the summary's paraphrase survives](https://github.com/anthropics/claude-code/issues/94564) | “Claude Code 2.1.270, macOS, model claude-fable-5-1”; **Open** | “The skills docs say compaction re-attaches the most recent invocation of each skill after the summary (first 5,000 tokens each, 25,000 combined). In my session that did not happen after a manual `/compact`.” No identified maintainer reply was visible. The page contains community explanations involving elapsed time versus process/resume state; those explanations are not established implementation facts here. |

The older direct-loss lead [#13919](https://github.com/anthropics/claude-code/issues/13919) remained **UNREAD**: issue page and alternate issue API returned HTTP 404; comments extraction timed out. Its title, version, state and maintainer response are not asserted.

### 5. Memory and hooks: separate restoration paths

The context table distinguishes skill replay from memory and earlier hook output: project-root CLAUDE.md/unscoped rules and auto memory are “Re-injected from disk,” while “Context that hooks added earlier” is “Summarized with the rest of the conversation.” SessionStart hooks matching the `compact` source run and add their output to the compacted context. [Official context-window documentation](https://code.claude.com/docs/en/context-window); [memory documentation](https://code.claude.com/docs/en/memory).

The hook guide explicitly offers: “Use a `SessionStart` hook with a `compact` matcher to re-inject critical context after every compaction.” This is a configured reinjection route, not proof that every earlier hook's output persists. PreCompact can “Exit with code 2 to block compaction”; PostCompact hooks “can't affect the compaction result but can perform follow-up tasks.” [Hooks guide](https://code.claude.com/docs/en/hooks-guide#re-inject-context-after-compaction), [hooks reference](https://code.claude.com/docs/en/hooks). These quoted hook facts were fetched by the documentation digger but were not among the eight verification pages.

## Coverage

- Version history (release engineering): **partial** — exact versioned changes established; body-retention introduction undated. Reports: `sub-rr/01-version-history.md`, `sub-rr/05-history-boundary.md`.
- Documented lifecycle (technical specification): **settled** — explicit body/list distinction, limits, manual/automatic equivalence, memory and hook contracts. Report: `sub-rr/02-documented-lifecycle.md`.
- Mechanism and limits (software implementation): **partial** — documented attachment policy and child-leak fix established; contradictory collaborator explanations and exact implementation remain unresolved. Reports: `sub-rr/03-mechanism-limits.md`, `sub-rr/06-scope-reconciliation.md`.
- Reported failures (incident analysis): **partial** — three representative issue states, versions and direct quotations established; original issue inaccessible and no identified maintainer resolution. Reports: `sub-rr/04-reported-failures.md`, `sub-rr/07-issue-states.md`.

Digging ended: a round settled nothing and added no sub-area

## Verification

30 numbered statements checked against 8 source pages; the older changelog required a second slice of the same page. Quotes, not the fetcher's YES/NO labels, were used. Issue versions and open/closed states previously marked unquoted were recovered as exact text.

NOT ON PAGE: the stronger assertion that editing SKILL.md *requires* reinvocation to take effect was not established as a necessity by the skills page; excluded. The inference that reporter closure itself establishes a maintainer verdict was not stated by #96168; excluded. The first changelog slice did not contain 2.1.45; the next slice of that same page supplied the exact quote. UNCHECKED: none of the sampled checks failed. The historical body-introduction date remains unknown, not a verified negative claim about every release.

## Open rabbit holes

- **dug, unsettled:** earliest body-reattachment introduction version; no explicit changelog entry recovered.
- **dug, unsettled:** reconcile bcherny's conflicting explanations in #72549 and #74990 without inventing a version transition.
- **dug, unsettled:** recover inaccessible #13919 and its comments/state.
- **dug, unsettled:** resume-related skill registry reconstruction, exact accounting units, and the reported omission mechanism; reporter hypotheses are not confirmed implementation contracts.
- **dug, unsettled:** exact parent/child registry ownership and fork/preloaded-skill edge cases beyond the explicit 2.1.45 child-leak fix.
- **undug:** later fixes for stale invocation snapshots and resume omissions; linked requests #85138 and #81518 were not independently investigated.
- **undug:** exact cross-event ordering of PostCompact and SessionStart(compact), beyond their documented individual contracts.

Sources: [Skills](https://code.claude.com/docs/en/skills), [Context window](https://code.claude.com/docs/en/context-window), [Memory](https://code.claude.com/docs/en/memory), [Hooks](https://code.claude.com/docs/en/hooks), [Hooks guide](https://code.claude.com/docs/en/hooks-guide), [Current CHANGELOG](https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md), [Versioned CHANGELOG](https://raw.githubusercontent.com/anthropics/claude-code/v2.1.119/CHANGELOG.md), [#74990](https://github.com/anthropics/claude-code/issues/74990), [#72549](https://github.com/anthropics/claude-code/issues/72549), [#96168](https://github.com/anthropics/claude-code/issues/96168), [#83306](https://github.com/anthropics/claude-code/issues/83306), [#94564](https://github.com/anthropics/claude-code/issues/94564).
