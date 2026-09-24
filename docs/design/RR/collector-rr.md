# collector-rr

`collector-rr` fetches web sources a caller has already located and returns their own words. It maps nothing: no plan, no diggers, no rabbit-hole footer, no document. Each numbered order names a source and what to take from it; the return is, per order, verbatim quotes with their links, or a mark saying why not. This file holds every decision of the agent.

## Contents

- [Identity](#identity)
- [What it takes](#what-it-takes)
- [The read path](#the-read-path)
- [The return](#the-return)
- [Marks](#marks)
- [What it leaves out, and why](#what-it-leaves-out-and-why)
- [What the runs measured](#what-the-runs-measured)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Identity

| Field | Value |
| --- | --- |
| Kind | original agent, `templates/global/agents/collector-rr.md`, linked by `pfm install` |
| Model, effort | `sonnet`, `low` |
| Tools | `WebSearch, WebFetch, Grep, mcp__harvester__findWorks, mcp__harvester__readWork, mcp__harvester__readPage` |
| Spawns | nothing — it holds no `Agent` |
| Writes | nothing — it holds no `Write`; the return is the whole artifact |
| Start hook | none; the `rr-dir` matcher does not name it |

Description, verbatim: `Fetches named web sources verbatim — delegate when the caller knows where to look: "get X from this page", "pull the figures from these URLs/DOIs". Pass numbered orders. Returns per order verbatim quotes with links, or a mark. Mapping a topic → rr.`

## What it takes

Numbered orders. Each names one source and what to take from it:

| Source form | How it is resolved |
| --- | --- |
| A URL | used as given |
| A paper, book or report by DOI, arXiv id, PMID, ISBN or title | `mcp__harvester__findWorks`, then `mcp__harvester__readWork` on the handle |
| A page named but not linked ("the Go 1.24 release notes") | one WebSearch, taking the result on the named site |

It opens no source an order does not name. A source shared by two orders is read once.

## The read path

1. `mcp__harvester__readPage` for a web page, `mcp__harvester__readWork` for a work, always with `size_only: true`. The result carries no body, only each item's `path`: a Markdown copy of the whole source, one line per paragraph.
2. Every item is taken from that `path` with Grep, `output_mode: "content"`, `-C` for the surrounding lines. Grep's lines are the source's own words.
3. WebFetch only for a table, or an item Grep cannot find, its prompt asking for the exact sentences, rows or code.

Why this order:

- WebFetch answers a question about a page through a second model; it does not hand back the page. What it returns is that model's rendering of the page, which a verbatim order cannot rest on.
- A full `readPage` body is large (the Claude Code hooks page alone is 133,910 characters); the harness spills an oversized tool result to a file as one JSON line, and Grep over a one-line file answers `[Omitted long matching line]`. `size_only` returns the `path` of the multi-line Markdown copy instead, which Grep reads line by line.
- A page cut short is not a page read. The collector's first design fetched with WebFetch and marked `SubagentStart`'s input fields `NOT ON PAGE`; the sentence sits about 89,000 characters into the 134,000-character page, where WebFetch's answers never reached. Reading the whole copy by Grep removes that failure.
- The harvester's HTML conversion drops tables (every table on the hooks page), and WebFetch saw them; so a table is WebFetch's job.
- Tools are named in full in the prompt: a short name (`readPage`) is not a tool the agent holds, and the call fails with `No such tool available`.

`Grep` is safe on this agent because it holds no `Write`. The harness relaxes its refusal to overwrite an unread file for an agent that can read files; the family's leads write the RR document and keep no read tool for that reason, but the collector writes nothing.

## The return

- The message opens with `1.`; every line is the source's own words, a source link, or a mark — no preamble, no summary, no conclusion of its own. The prompt carries one contrastive example (✗ `Note on process: …` / `I have gathered the content…`, ✓ a message starting `1. Source:`), because the plain rule alone did not hold.
- A table or list is copied whole.
- A sentence that bounds an item — a version, a condition, an exception — is copied with it, so the caller never receives a rule without its limit.
- An item the source states only in part carries the part it states and names what is missing.

## Marks

An order that yields nothing opens with one mark, never another's wording:

| Mark | Means |
| --- | --- |
| `FETCH FAILED — {the error}` | the source could not be read |
| `NOTHING FOUND — {the queries tried}` | a named source could not be located |
| `PARTIAL READ — {the part read}` | the source was cut short before the item could be ruled out |
| `NOT ON PAGE — {what was looked for}` | the whole source was read and does not carry it |

An error never renders as an absence: `FETCH FAILED` and `PARTIAL READ` stand against `NOT ON PAGE`.

## What it leaves out, and why

| Left out | Reason |
| --- | --- |
| The rabbit-hole footer | The caller did the looking; a footer buys leads nobody follows |
| A plan, diggers, rounds | One source per order and a known item leave nothing to plan; a digger would cost a spawn and a return for what one Grep answers |
| A saved document | The return is the artifact; the caller decides where it goes |
| Verification | The item is the source's own lines, copied by Grep; there is no paraphrase for a check to catch |

## What the runs measured

Four runs of one brief (three orders: the hooks reference's matcher rules; `SubagentStart`'s matcher field and input fields; Go 1.24's `T.Context` and `B.Loop`) moved as follows:

- WebFetch-first returns quoted the matcher table correctly but marked `SubagentStart`'s input fields `NOT ON PAGE`, the sentence lying deep in the page, past what WebFetch's answers covered.
- `readPage` without `size_only` spilled 49.8 KB and 88,397-character results the agent could not Grep, and it fell back to WebFetch.
- With `size_only`, Grep and full tool names, the return opened `1.`, carried only verbatim text, and quoted the sentence the earlier runs could not reach: "In addition to the common input fields, SubagentStart hooks receive `agent_id` with the unique identifier for the subagent and `agent_type` with the agent name that the matcher filters on."

## Surfaces that stay in sync

| Surface | File |
| --- | --- |
| The agent | `templates/global/agents/collector-rr.md` |
| The family doc | `docs/design/RR/rr.md` |
| The roster line | `docs/BLUEPRINT.md` |
