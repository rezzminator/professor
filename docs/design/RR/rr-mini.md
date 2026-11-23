# rr-mini

`rr-mini` reads a source the caller already located and returns each fact with its URL. `rr` maps an unknown knowledge area; `rr-mini` fetches from a known location, such as Anthropic's documentation, a named docs page or a repository file.

## Identity

| Field | Value |
| --- | --- |
| Kind | original agent, `templates/global/agents/rr-mini.md` |
| Model, effort | `haiku`, `high` |
| Spawns | nothing — it holds no `Agent` tool |
| Writes | no run directory; it gets no `RR-DIR:` line |
| Start hook | none: the `rr-dir` matcher (`rr\|rr-pro\|rr-pro-max`) does not include it |

## When a caller picks it

- The caller can name where the data lives: a documentation page, a URL, a repository file. `rr-mini` reads that source directly and returns each fact with its URL.
- The caller does not know where the answer lives, or the question spans several fields: use `rr`, which plans the sub-areas, digs, verifies and saves a run.

## What the runs measured

Nothing yet: no run has exercised it.

## Surfaces that stay in sync

| Surface | Holds |
| --- | --- |
| `templates/global/agents/rr-mini.md` | the agent |
| `rr.md` § The family | its row |
