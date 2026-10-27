# Submitting Professor to hesreallyhim/awesome-claude-code

## Contents

- [1. Submission mechanics — the exact form](#1-submission-mechanics--the-exact-form)
- [2. What happens after submitting](#2-what-happens-after-submitting)
- [3. What others did — 8 recent submissions read in full](#3-what-others-did--8-recent-submissions-read-in-full)
- [4. Fitting category for Professor](#4-fitting-category-for-professor)
- [5. Ready-to-paste form values for Professor](#5-ready-to-paste-form-values-for-professor)
- [6. Risks specific to Professor](#6-risks-specific-to-professor)
- [Gaps](#gaps)

Research-only. Nothing was opened, forked, starred, or commented. All facts below were read via `gh api` / `gh issue list` / `gh issue view` on 2026-09-14 against `hesreallyhim/awesome-claude-code` (53,988 stars, not archived, pushed same day).

## 1. Submission mechanics — the exact form

Source: `.github/ISSUE_TEMPLATE/recommend-resource.yml` (fetched via `gh api repos/hesreallyhim/awesome-claude-code/contents/.github/ISSUE_TEMPLATE/recommend-resource.yml`), plus `CONTRIBUTING.md`.

- **Channel:** web UI issue form only — <https://github.com/hesreallyhim/awesome-claude-code/issues/new?template=recommend-resource.yml>. CONTRIBUTING.md states explicitly: *"Do not open a PR. Just fill out the form... you risk being restricted from interacting with this repository temporarily."* Also: *"It is **not** possible to submit a resource recommendation using the `gh` CLI."*
- **One resource per issue.** Submitter must be human (agent-authored resources are fine; the recommendation itself must not be agent-submitted).
- **Eligibility gate (bot-enforced):** resource must meet (a) at least 14 days since first commit on the default branch AND active development (commits after day one), OR (b) at least 100 stars.
- **Form fields, in order, exactly as declared in the YAML:**

| Field id | Label | Type | Required | Notes |
| --- | --- | --- | --- | --- |
| `display_name` | Display Name | short text | yes | Name as it will appear in the list. Placeholder: "e.g., Dev Browser" |
| `category` | Category | dropdown, single-select | yes | Fixed list of 26 options (see §4) |
| `link` | Link | URL text | yes | Must start with `https://`; "Prefer the GitHub repo" |
| `author_name` | Author Name | short text | yes | Name, alias, or GitHub username |
| `author_link` | Author Link | URL text | yes | Link to author's GitHub profile or site |
| `description` | Description | textarea | yes | "1-3 sentences. Descriptive, not promotional. Don't address the reader. (10-500 characters.)" |
| `checklist` | Checklist | 6 checkboxes | 5 required, 1 must stay unchecked | See below |

Checklist items (5 required-checked + 1 required-UNCHECKED trap):

1. "I have visited this repo before with my own eyes, and I have confirmed that this resource is sufficiently distinct from any existing resource"
2. "All links are working and publicly accessible"
3. "This resource is specific to Claude Code"
4. "I have read the CONTRIBUTING.md"
5. "I promise I actually did these things and not doing so is shameful and lazy"
6. "Do not check the following box — leave it unchecked. By checking this box, I admit that I am not reading any of these statements." (must stay unchecked; a trap for form-skimmers)

Style rules from CONTRIBUTING.md: *"Resource descriptions should be written as descriptions — not a sales pitch. Don't address the reader... Keep it formatted to one line. Don't use any emojis."* License is auto-discovered from the repo by the bot, not entered manually. Closed-source, signup-gated, or paywalled projects are only recommendable via community-adoption signal, not by default.

## 2. What happens after submitting

- Issue is auto-labeled `resource-submission` + `validation-pending` on creation.
- A `github-actions` bot posts a **Validation Results** comment within minutes: parses the form fields back as JSON and checks the eligibility gate.
  - **Pass:** comment reads *"✅ All validation checks passed! Your recommendation is ready for a maintainer to review."* Label flips to `validation-passed`. No PR yet — a human maintainer (hesreallyhim) must still act.
  - **Fail (age/stars gate):** comment reads *"This resource does not currently satisfy the required conditions stated in the CONTRIBUTING guidelines."* Label becomes `auto-closed` and the **issue is closed automatically**, same-minute in the cases observed (issues #2805, #2806, #2814, #2818 all closed within ~10-20 seconds of creation).
- **Maintainer review is manual and has no SLA.** CONTRIBUTING.md: *"there is no formal submission/review process at the moment... Recommendations are reviewed in a best-effort way, and no guarantee is made as to whether you will receive a response."* Evidence: as of 2026-09-14, dozens of `validation-passed` issues dating back to 2026-09-02 (12+ days, e.g. #2708 "great_cto") remain open with zero maintainer comment beyond the bot.
- **Maintainer commands, driven by slash-style comments that the bot parses:**
  - `/approve` → bot posts *"✅ Resource Approved! A pull request has been opened: <PR URL>. It updates the CSV and regenerates the list."* — i.e., the README entry is **machine-generated from a CSV** the bot edits and PRs; the maintainer does not hand-write the markdown line.
  - `/request-changes <text>` → bot reposts the maintainer's text under "🔄 Changes Requested", issue stays open for the submitter to edit.
  - A maintainer rejection comment followed by bot closing → *"❌ Submission Rejected. Reason: See comment above."* (or "No reason provided" if the maintainer didn't leave one before closing).
- Labels seen across the repo's full label set relevant to this flow: `validation-pending`, `validation-passed`, `validation-failed`, `auto-closed`, `approved`, `pr-created`, `rejected`, `changes-requested`, `needs-template`, `duplicate`, `broken-links`, `error-creating-pr`, `excused`, `incompetence`, `libel` (the last two suggest the maintainer has dealt with bad-faith submissions/spam before).

## 3. What others did — 8 recent submissions read in full

| # | Title | Outcome | Stated reason (maintainer, quoted) |
| --- | --- | --- | --- |
| [#2830](https://github.com/hesreallyhim/awesome-claude-code/issues/2830) | Claude Style Patch | **Approved**, PR [#2832](https://github.com/hesreallyhim/awesome-claude-code/pull/2832) opened, closed same day | *"approving this even though it violates its own guidance because the problem it addresses is so severe"* — `/approve` |
| [#2818](https://github.com/hesreallyhim/awesome-claude-code/issues/2818) | Kin (graph-native code repo for agents) | **Auto-closed** by bot | Bot only: *"does not currently satisfy the required conditions"* — repo is 59★, created 2026-03-10 (>14d old), yet still auto-closed; exact bot criterion unclear (possibly "active development" sub-check, not confirmed) |
| [#2814](https://github.com/hesreallyhim/awesome-claude-code/issues/2814) | humanizer-ru | **Auto-closed** by bot | Same generic gate message; repo not individually re-checked |
| [#2806](https://github.com/hesreallyhim/awesome-claude-code/issues/2806) | Universal Agent Plugins | **Auto-closed** by bot | Same generic gate message |
| [#2805](https://github.com/hesreallyhim/awesome-claude-code/issues/2805) | whatileaked | **Auto-closed** by bot | Repo created 2026-08-27, submitted 2026-09-10 (~14d, borderline), 6★ — right at/under the gate |
| [#166](https://github.com/hesreallyhim/awesome-claude-code/issues/166) | Claude Code Web Shell | **Rejected** by maintainer | *"the project needs a bit more development before I'm comfortable sharing it, in particular due to the security risks"* |
| [#165](https://github.com/hesreallyhim/awesome-claude-code/issues/165) | Claude Code Cheat Sheet | **Rejected** after changes-requested round | *"I don't think it has enough value-add over the Anthropic doc-site... I don't know if it merits an additional entry"* |
| [#2796]-[#2833] (13 issues sampled, e.g. #2796 "Jarvis", #2811 "trailhead", #2825 "ClaudeGate") | Various | **validation-passed, still open** 1-14 days later | No maintainer comment yet — confirms review is a slow, best-effort backlog, not a fast turnaround |

**Patterns that correlate with acceptance, from the one approved case read in full plus rejection reasons above:**

- The one visible approval (#2830) was for a **narrowly-scoped, single-purpose** resource (a CLAUDE.md prose-style patch) with a **concrete, specific description** naming exact behaviors it changes ("bans specific habits... each rule names the habit, shows an example, gives the rewrite") — not a feature list.
- Both read rejections were about **substance, not form**: security immaturity ("needs a bit more development... security risks") and **insufficient differentiation from existing/official resources** ("not enough value-add over the Anthropic doc-site"). Neither rejection cited star count or age — those are filtered pre-review by the bot gate.
- Auto-closures dominate the volume (most-common outcome by far in the sample): failing the 14-day/100-star gate closes an issue within seconds, with no human ever seeing it. Star/age math should be checked twice before submitting.
- No evidence found of category choice itself causing rejection — rejections were about the resource's substance/uniqueness, not the dropdown value.

## 4. Fitting category for Professor

Current dropdown options (from the issue template, 26 total, `>` = subcategory of parent):

Start Here · Documentation, Knowledge & Learning (+ Obsidian) · Open Source Software · Research & Scientific Inquiry · Providers, Runtime & Integration Infrastructure · Remote Control, Notifications & Voice I/O · Alternative Clients · Status Lines · Design & UI/UX · Writing & Prose Quality · Creative Media · Infrastructure & DevOps · Security · **Agent Orchestration** (+ Ralph Wiggum, + Dynamic Workflows) · Skills · Memory & Context Persistence · Observability & Monitoring (+ Session Monitors, + Usage & Cost, + Observability) · Configuration · Testing · Linting · **Multi-Purpose**

Recommendation: **Multi-Purpose**, with **Agent Orchestration** as the closest single-category fallback.

Reasoning: Professor spans three things no single narrower category covers — (1) pfm's cross-chat fleet control/messaging (closest to Agent Orchestration, but that category's subcategories, Ralph Wiggum and Dynamic Workflows, are about orchestration *patterns/loops*, not a fleet-wide CLI/TUI across independent chats and accounts), (2) a Claude Code discipline layer of agents/commands/hooks/rules also compiled to Codex/OpenCode (closer to Configuration or Skills, neither of which covers the orchestration half), and (3) Harvester, an MCP document fetcher (closer to Providers, Runtime & Integration Infrastructure). The template only allows one category selection, and Multi-Purpose exists precisely for resources that don't reduce to one bucket — this list's own dropdown makes that the documented escape valve rather than a weak fit. If forced to a single specific category instead, Agent Orchestration is the best second choice since fleet-wide chat control and messaging is the most distinctive, load-bearing feature of the three.

## 5. Ready-to-paste form values for Professor

- **Display Name:** `Professor`
- **Category:** `Multi-Purpose`
- **Link:** `https://github.com/rezzminator/professor`
- **Author Name:** `rezzminator`
- **Author Link:** `https://github.com/rezzminator`
- **Description** (single paragraph, no line breaks, no emojis, third person, 1-3 sentences, within 10-500 chars):

  > An LLM-harness fleet boost framework for Claude Code, Codex, and OpenCode. Its Go CLI/TUI (pfm) lists and controls every AI coding chat on the machine, including a Limits dashboard and cosmos view, lets chats message each other, and can reload a chat onto another account while keeping its history. It pairs this with a discipline layer of agents, commands, hooks, and rules for Claude Code that also compiles to Codex and OpenCode mirrors, and Harvester, a document fetcher with a multi-rung fallback ladder exposed over MCP.

  (This mirrors the concrete, behavior-naming style of the one approved submission read in full, #2830, rather than a feature-adjective pitch.)
- **Checklist:** check items 1-5, leave item 6 (the trap box) unchecked.

## 6. Risks specific to Professor

- **Age/star gate is close but should clear on paper.** Repo created 2026-04-25 — well past the 14-day floor by the 2026-09-14 research date — with stated daily activity, so it should satisfy branch (a) of the gate (14 days + active development) even at only ~4 stars, which is far under the 100-star branch (b). Caveat from the evidence: issue #2818 (Kin, 59★, created 2026-03-10, clearly past 14 days) was still auto-closed by the bot with only the generic gate message, and the bot's exact "active development" check (e.g., minimum commit count, commit cadence, or a specific lookback window) could not be read from any public source in this research — this is a genuine unknown, not a guess.
- **Multi-Purpose is a documented-fine category but the read-in-full approval (#2830) was for a much narrower, single-purpose resource.** No approved Multi-Purpose example was found in the sample to confirm the maintainer treats broad, multi-component submissions favorably; the two rejections read were both about narrow single-purpose tools, so this is a gap, not a contradiction.
- **"Value-add over existing/official resources" was an explicit rejection reason** (#165, cheat-sheet vs. Anthropic docs). Professor's description should keep emphasizing what's genuinely distinctive (cross-chat messaging, account-preserving reload, tri-runtime compilation) rather than restating that it's "a Claude Code framework," since that alone reads as commodity.
- **Security/maturity scrutiny is real** for anything touching process control or credentials (#166's rejection cited security risk in a browser-CC bridge). Professor's chat call/reload machinery operates across accounts and processes on the machine — if a maintainer asks about this, be ready to address it, though no submission in the sample was rejected for this exact shape of feature.
- **Review has no SLA and a real backlog.** Dozens of `validation-passed` issues from as far back as 2026-09-02 sat open with zero maintainer response as of 2026-09-14. Getting a form-valid submission through the bot gate does not mean a timely (or any) maintainer look.
- **Only one resource per issue** — Professor's three components (pfm, discipline layer, Harvester) cannot be split into multiple simultaneous submissions to this list the way the draft's cross-list document treats other lists; one issue, one category, must represent the whole project.

## Gaps

- The bot's precise "active development" sub-check (beyond "14 days old") could not be determined — no bot source code, Action workflow YAML, or documented spec was located in the paths checked (`.github/ISSUE_TEMPLATE/`, `CONTRIBUTING.md`, root README). Issue #2818's auto-close (59★, 6-month-old repo) despite apparently clearing both age and star thresholds is unexplained; flagged as a real risk in §6 rather than resolved with a guess.
- Only 8-13 resource-submission issues were read in full text (2 approved/rejected historical, ~5 recent auto-closed, ~1 request-changes-then-rejected historical, several open validation-passed skimmed for status only). This is within the requested 8-10 range but is not exhaustive of the ~2800-issue history.
- No maintainer response could be found for a `Multi-Purpose`-category submission specifically — the only fully-read approval (#2830) was `Writing & Prose Quality`. Category-specific acceptance patterns for Multi-Purpose remain unverified beyond the template documenting it as a legitimate option.
