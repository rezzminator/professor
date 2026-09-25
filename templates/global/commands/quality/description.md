---
name: quality:description
description: MANDATORY — load before writing or certifying any `description:` (command, skill, agent, MCP tool or server); the four components and their order, the caps, the cut order, naming, family-chain and invocation-class law, the MCP self-containment rules, and the Approval gate. General prompt law → /quality:prompt.
argument-hint: [path…]
---

# Description Law

A `description:` is the routing surface. The harness injects every one of them into every session and every sub-agent spawn and loads a body only on a match, so it is both the most re-read text in a fleet and the only text that decides whether an entity is ever reached. `/quality:prompt` governs every prompt line; this file adds what a description needs beyond it. With `[path…]`, run § Approval over each path — a `.md` frontmatter, or an MCP tool's description string in source — and emit one verdict per entry.

## The four components, in one order

```
[CLASS] {function} — {when}. [Returns {shape}.] [Not for {x} → {home}.]
```

- CLASS: the invocation class, first word, where one applies — `MANDATORY`, `{CALLER}-ONLY`, `USER-ONLY` (§ Invocation class). No class means the model routes on its own judgment.
- function: what it does, verb first, five words at most, ending at the first ` — `. Modes, artifacts and detail belong to `when`. Omit it where the name already says it (§ Naming) and open with the class or the `when`.
- when: the call to action — the asks it answers (three phrasings at most), every subcommand, mode, flag and alias the body handles, each in the form the caller types, and for a family member its neighbours (§ Family). An entry point missing here is unreachable.
- Returns: the artifact KIND only — a path, a map, a verdict, one line. Required of an agent and an MCP tool, whose caller plans around the shape.
- Not for: a redirect, never a bare ban — name the home that should take the request instead. Include it only where a real misroute happens.

The description is WHEN and WHAT; the body is HOW. A rule the body enforces, a procedure step, or a quality bar ("zero-gap", "closed-world") stays in the body unless the caller must choose by it.

## Budget

- A body-bearing entry (command, skill, agent): 280 characters, and up to 400 where every character past 280 is an entry point, a family neighbour, or the Returns clause.
- An MCP tool: 600 characters, one of them an example call.
- An MCP server's instruction block: 900 characters; 1200 for one server that carries the routing of two tool families, since two servers would each have had their own 900.
- A skill also has the consumer's own hard ceiling on `description` plus `when_to_use`. Know it before writing; it is a limit, never a target.

An entry the model never sees costs nothing: once `disable-model-invocation: true` hides an entry (§ Invocation class), its description is a menu label for a human and the cap stops binding.

Cut order when over, first cut first: the mechanism (the library, engine, model tier or internal stage the caller never selects); the name echo; rationale and body-enforced rules; Returns detail past the artifact kind; trigger phrasings past three. Never cut the function, the class, a family neighbour, or an MCP tool's example call.

Count the PARSED value, not the source line — a folded value joins to one line, a quoted one drops its quotes, and a source literal carries its own syntax. Eyeballing a wrapped block over-reports every time.

## Naming

The name is read beside the description and is the first routing signal, so spend it: `family:verb-noun`, a pairing whose meaning needs no gloss. The description then never respends the name's words — an entry named for prompt quality that opens "Prompt quality —" pays twice for one fact.

## Family

A `family:*` set is one pipeline, and its ORDER must be legible from the descriptions alone, without opening a body. The entry member carries the whole chain once, as arrows. Every other member names only its predecessor and its successor, plus its `{CALLER}-ONLY` class when no person invokes it. Cross-family hand-offs use the same arrow. The chain lives in exactly one member; a second copy rots at the next insert.

## Invocation class — who may call it

Three different facts with three different mechanisms. Name the fact, then use the strongest mechanism available for it:

- MANDATORY — the model MUST load or route here at a step some law names. No harness flag expresses an obligation, so the token in the description is the whole mechanism and always stays.
- `{CALLER}-ONLY` — exactly one named entity invokes it. The token stops every other reader; the caller's own prompt carries the invocation.
- USER-ONLY — a person types it. `disable-model-invocation: true` IS the mechanism: the harness drops the entry from the model's registry, so self-invocation is unreachable rather than merely forbidden, and the description costs the model nothing. Two things follow. The description stops arguing with the model: keep the `USER-ONLY` token as the human's menu label, cut the prose ban no model will read. And it stops advertising spoken triggers — a hidden entry that lists "or says …" phrasings offers a route nothing can take, so either the triggers go or the flag does.

Two cases have no flag to reach for, and there the token is the only signal: a skill, which has no such field (its caller-side `tools:` allowlist is the real enforcement), and an entity a user may ASK for in prose rather than type, where hiding it would make a request the user actually made unroutable.

## MCP tools — self-contained, because there is no body

An MCP tool's description is everything a caller will ever read, beside a JSON input schema. Two levels, each said once:

- The server's instruction block: the family map (each tool's ask-phrase → its tool name), each confusable pair contrasted, the audience boundary naming who may call these tools and who may not, and any habit that applies to every tool in the family.
- Each tool's own description, in § The four components order, plus three parts a body would otherwise carry:
  1. One inline example call using the schema's real field names. A tool without its example is the tool that gets misused.
  2. The hand-off to a sibling where one tool's output feeds another, and the scope boundary wherever the tool is misusable — WHO may call it, for WHAT. A tool for talking between peers gets misused as a tool for talking to a parent unless the description says which it is.
  3. What its failure looks like versus what its EMPTY result looks like, distinguished. One shape for both teaches the caller to read an error as an absence.

Never narrate a field the schema already documents — cut it from the description and sharpen it in the schema. And a prompt clause is only half of any misuse fix: the other half is the caller's `tools:` allowlist, because an agent granted every tool is where the misuse enters.

## The shape of a cut

Same entity, over cap and under it:

- Before: `Lint and format markdown with {library} — {mode-name} reports, {mode-name} rewrites in place. Use when markdown is inconsistently wrapped, before committing a doc, or when adding a formatter to a project. Route markdown lint/format/style requests here.`
- After: `Lint/format markdown — {mode-name} reports, {mode-name} rewrites. Route every markdown lint/format ask here; {mode-name} before committing a doc.`

What left: the library (the caller never selects it), the rationale (the rule implies it), and the third trigger phrasing. What stayed: every entry point, and the one sentence saying when to come here.

## Approval — certify a description

Run over every entry, at write-time and on demand. An entry is APPROVED only when ALL checks hold; otherwise REJECTED with the failing checks named, fixed, and re-checked.

- 0 Present: the description is absent or empty — an unroutable entry.
- 1 Order: a class token is not the first word, the function clause runs past five words or past the first ` — `, or a component sits out of § The four components order.
- 2 Budget: a body-bearing entry exceeds 280 characters without the 400 tier's justification, or exceeds 400 at all; an MCP tool exceeds 600; a server block exceeds its cap. Count the parsed value.
- 3 Name echo: the opening clause restates the name's words.
- 4 Mechanism: a library, engine, model tier or internal stage the caller never selects is named.
- 5 Entry point: a subcommand, mode, flag or alias the body handles is missing from `when`.
- 6 Class: an obligation some law states lacks its token; a user-only entity that could carry `disable-model-invocation: true` does not; or a flagged entry still spends prose banning the model that cannot read it, or advertises a spoken trigger the flag makes unreachable.
- 7 Family: a member lacks its predecessor or successor, or the chain is missing from the entry member or duplicated in a second one.
- 8 Redirect: a `Not for` clause names no home, or restates a ban a harness mechanism already enforces.
- 9 MCP: a tool lacks its example call with real field names, a confusable sibling is uncontrasted, empty and error results share one shape, or the description narrates schema fields.
- 10 Body echo: the body's opening restates the description.

Emit `APPROVED: {path}` or `REJECTED: {path} — checks {n,…}` per entry; a file that could not be read or parsed emits `UNREAD: {path} — {error}`, never a verdict. A family or a server is approved only when every member is.
