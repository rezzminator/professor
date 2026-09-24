# sub-rr

`sub-rr` is the family's digger: it answers one numbered batch of sub-queries for a lead and returns a quote-anchored finding per sub-query plus every rabbit hole its sources raised. It is spawned only by `rr`, `super-rr` and `heavy-rr`, never delegated to directly. This file holds every decision of the digger; the lead's side of the exchange (grouping, the stop rule, verification) is in `rr.md` in this directory.

## Contents

- [Identity](#identity)
- [The brief it receives](#the-brief-it-receives)
- [The fetch path](#the-fetch-path)
- [The return](#the-return)
- [Sources and marks](#sources-and-marks)
- [Why it never writes](#why-it-never-writes)
- [What the runs measured](#what-the-runs-measured)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Identity

| Field | Value |
| --- | --- |
| Kind | original agent, `templates/global/agents/sub-rr.md`, linked by `pfm install` |
| Class | `RR-ONLY` |
| Model, effort | `sonnet`, `low` |
| Tools | `WebSearch, WebFetch, mcp__harvester__read, mcp__harvester__search_literature, mcp__harvester__search_web` |
| Spawns | nothing — it holds no `Agent` |
| Writes | nothing — it holds no `Write`, `Edit` or `Bash` |

Description, verbatim: `RR-ONLY digs a batch of rabbit holes — spawned by rr, super-rr and heavy-rr, never delegated to directly. Returns a 2-4 sentence cited finding per rabbit hole, then every new rabbit hole its sources raised.`

## The brief it receives

The lead writes each brief from one group of its frontier (one unsettled sub-area):

- a numbered list of self-contained sub-queries, one subject each, named in full;
- the pages already read for that group, each with what it lacked — the digger skips them;
- one `Goal:` line, the query in one line, identical in every brief of the run, which fills the footer's `{goal}` slot so rabbit holes serve the run and not the page in general.

## The fetch path

1. WebSearch each sub-query, then read the 2-3 best sources; a source serving two sub-queries is read once.
2. `read` with `publications` for a paper, book chapter, PDF or document identifier, `search_literature` first when only a title is known. WebFetch is the default for a page.
3. `read` with `urls` when a WebFetch fails or returns an encoded or empty body.
4. Every WebFetch prompt asks the key question first, asks for the exact sentence behind every figure and date, then appends the rabbit-hole footer with the `Goal:` line in its slot. A harvester-read document carries no footer; the digger lists the rabbit holes it raises itself.

The harvester tools cover what WebFetch cannot: a PDF WebFetch returns as an encoded stream, a page too large for it, a leaderboard it cannot render. Without them a digger has no second road, and the finding goes `unquoted` or empty.

## The return

- One finding per sub-query, under its number, in 2-4 sentences; the message opens with finding 1.
- Every figure and date, and the claim the finding turns on, sits inside a verbatim quote: a sentence the fetch result presents as the page's own words, never the fetch model's own prose. A fact it could not quote is marked `unquoted`.
- Sources that disagree are both given, each with its quote, marked `DISPUTED`.
- One `Rabbit holes` list follows, every rabbit hole the sources surfaced, each a concrete next web-search query and one line on why it matters. The lead decides which to follow; the digger drops none.

## Sources and marks

- A finding links only pages the digger fetched for it. A WebSearch result's title or snippet is a lead to fetch, never a source, and never one side of a `DISPUTED`. The prompt carries one contrastive example at the return step: ✗ an arXiv link seen in search results, cited without a fetch — ✓ fetch it first, or list it under `Rabbit holes`.
- Only a web URL or a document identifier is a source, never a local path.
- A sub-query whose search errors or whose every fetch fails opens with `DIG FAILED — {the error}`; one whose searches ran and answered nothing opens with `NOTHING FOUND — {the queries tried}`.

## Why it never writes

Up to eight diggers run at once, and any design where they write into the run's one document is a race or costs lead calls: a `Write` from each digger erases the others, an `Edit` from each is a read-modify-write that collides, part files need a merger. The findings must reach the lead's context anyway, because the lead steers and synthesizes from them, and the digger's return already does that at no extra call. So the return is the only channel, and the `tools:` line enforces it.

## What the runs measured

Digger returns across the family's runs, counted by script over the transcripts (links inside findings, before the `Rabbit holes` list, against the URLs the digger fetched):

- Citations of pages never fetched ran at 12-36% of findings' links while the rule sat in the closing paragraph; nearly every one was a URL taken from a WebSearch result. With the rule at the return step and its contrastive example, the next run's diggers fell to 5%.
- Harvester reads rose from 0-2 calls a run, when the `tools:` line named harvester tools that no longer existed, to 10-22 calls a run once it named the live ones.
- A preamble line ("Findings ready.", "Enough coverage gathered.") opens most returns. A contrastive example against it backfired: one digger opened with the example's ✗ sentence word for word. The prompt keeps the plain rule and no example; a preamble costs the lead a few tokens it reads past.

## Surfaces that stay in sync

| Surface | File |
| --- | --- |
| The agent | `templates/global/agents/sub-rr.md` |
| The briefs it receives | `templates/global/agents/rr.md`, step 4 |
| The family doc | `docs/design/RR/rr.md` |
