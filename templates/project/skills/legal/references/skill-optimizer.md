# Skill-Optimizer — Mine a Session's Mistakes, Patch the Skill

> Distilled from the `skill-optimizer` (self-improve) skill in github.com/lawve-ai/awesome-legal-skills (AGPL-3.0, © Malik Taiar / Lawvable) — summarised meta-loop, not copied. The meta-layer that keeps this `legal` skill improving from real use.

Use after a legal-drafting or compliance session to capture what was learned and propose precise improvements to the relevant reference file. In this repo, infra edits route through `/pcm` — so this is a **propose-then-PCM-applies** loop, not a self-editing one.

## The loop

1. **Identify the target** — which reference/skill does the learning belong to?
2. **Detect signals** in the session:
   - **Correction** — "no", "that's wrong", "it's missing X", "always do Y", or the user rewrote the output.
   - **Success** — "perfect", "exactly", accepted unchanged (confirms a rule works — leave it alone).
   - **Edge case** — the user needed a workaround the skill didn't cover.
3. **Grade each correction** against four criteria — add only if all four pass; otherwise ask for clarification:
   - **Complete** — fully specified, nothing to assume ("structure as: Key Terms / Risk Areas / Revisions", not "use the standard format").
   - **Precise** — two readers understand it identically ("flag non-competes over 12 months as high risk", not "be more thorough").
   - **Atomic** — one instruction, one requirement (split "check governing law, jurisdiction, and arbitration" into three).
   - **Stable** — version- or date-anchored if it references a standard ("under policy X dated 2024-12-12", not "latest market standards").
4. **Propose the change** — the exact instruction to add, with the source quote from the session. Route it through `/pcm` to apply (infra discipline) rather than editing the reference inline.

## Principles

- Never guess intent — if a correction is unclear, ask; do not infer requirements from context.
- One instruction = one check; split bundled feedback.
- Fewer good instructions beat many vague ones.
- A success signal is data too — it tells you which rule earned its place.

## {PROJECT_NAME} application

After a meaty `/officer` or `legal`-skill session where the user corrected an output, run this to turn the correction into a candidate rule for the right reference file — then hand it to `/pcm` to land. This is how the legal references stay sharp without drifting into bloat. Per repo prompt-quality rules, prefer sharpening an existing rule over adding a new one, and apply the cut test to every proposed line.
