# 2026-10-19 — a landing commit carried a generated agent unequal to a fresh render

Observed: the one-lander landing (3b366fdf) committed `templates/generated/agents/gpt-flights-lander.md` with one extra blank line against what `engine-sync global` renders from its `from:` source. Nothing before the commit compared it; the full gate's `templates.generated-agents` step caught it at the host update, which then stopped at the gate and left the host uninstalled until a follow-up commit (4af845fa).

Amend: the pcm agent's pre-commit checks — a change touching any `from:` source or `templates/generated/agents/**` re-renders with the gate's own `engine-sync global` call (infra/fence/checks.sh) and requires `diff -r` against the tracked directory to be empty before handing the commit to gitter.
