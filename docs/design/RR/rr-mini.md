# rr-mini

`rr-mini` reads a source the caller already located and returns each fact with its URL. `rr` maps an unknown knowledge area; `rr-mini` fetches from a known location, such as Anthropic's documentation, a named docs page or a repository file.

## Identity

| Field | Value |
| --- | --- |
| Kind | original agent, `templates/global/agents/rr-mini.md` |
| Model, effort | `haiku`, `high` |
| Spawns | nothing — it holds no `Agent` tool |
| Long pages | `Grep` and `Read`, opened only on the Markdown file a harvester `read` with `include_content: false` names: WebFetch cuts a page at 100,000 characters and harvester's inline output at 50,000, while the saved file holds the whole page |
| Writes | no run directory; it gets no `RR-DIR:` line |
| Start hook | none: the `rr-dir` matcher (`rr\|rr-pro\|rr-pro-max`) does not include it |

## When a caller picks it

- The caller can name where the data lives: a documentation page, a URL, a repository file. `rr-mini` reads that source directly and returns each fact with its URL.
- The caller does not know where the answer lives, or the question spans several fields: use `rr`, which plans the sub-areas, digs, verifies and saves a run.

## What the runs measured

One run, 2026-11-23: two questions on the Claude Code hooks reference, budget 4. It made one WebFetch. The first question came back answered with three verbatim quotes. The second came back part-answered: the page was cut at 100,000 characters before its `SubagentStart` section, and the run spent no retry on it. That run led to the long-page rule in its READ step and the SEARCH THE TEXT step.

A second run, the same day, on the same brief, after the fix: five tool calls. Both questions came back answered. The second quoted the `SubagentStart` section, found by Grep in harvester's saved copy of the 1,395,215-character page. Its quotes match the page's text, apart from one curly apostrophe turned straight and `.md` appended to one embedded link.

## Surfaces that stay in sync

| Surface | Holds |
| --- | --- |
| `templates/global/agents/rr-mini.md` | the agent |
| `rr.md` § The family | its row |
