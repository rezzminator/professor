# 2026-09-20 — an agent description with ": " in it sends the model line's comment to the API

Observed: a `description:` holding a colon-space (`… on any code entity: a stored table …`) is invalid as a YAML plain scalar. The harness then falls back to a line parser that keeps inline comments, so `model: sonnet # spec-execution default — retune to your model tier` reaches the API verbatim and every spawn dies with `model_not_found` (HTTP 404). `mapper` died twice this way; the shipped global `tracer.md` carried the same description and would have failed for every adopter. `ruby -ryaml -e 'YAML.safe_load(...)'` over `templates/global/agents/*.md` still fails on `speccer.md` line 3, which also carries a commented `model:` line.

Amend: fix `speccer.md`'s description (no colon-space inside the scalar, or quote it); add a gate that strict-YAML-parses every agent, command and skill frontmatter under `templates/**` and `.claude/**` and reports `FRONTMATTER-INVALID {file}:{line} — {parser error}`, a file it could not read as `UNREAD — {error}`, never a clean pass. `quality:description` gains the rule: a description is a valid YAML plain scalar or it is quoted.

Resolved: 2026-09-22 — codexgen strips trailing inline comments from scalar frontmatter values (b030e015 task 4-a)
