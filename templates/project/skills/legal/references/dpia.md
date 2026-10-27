# DPIA Building & Risk Register (Art. 35)

> Distilled from the `dpia-sentinel` skill in github.com/lawve-ai/awesome-legal-skills (AGPL-3.0, © Oliver Schmidt-Prietz) — summarised methodology, not copied. Structured Art. 35 guidance, not legal advice; involve the DPO (Art. 35(2)) and verify currency per the pre-delivery self-check.

## Assessment flow

Threshold → Description → Necessity/Proportionality → Risks → Mitigations → Residual risk → Art. 36 check → Documentation. The sequence is logical, not rigid, and **iterative** — if a later mitigation changes the processing design, revisit the earlier analysis.

## Threshold — do we need a DPIA?

1. **Art. 35(3) triggers are absolute** (no balancing if any apply): systematic extensive automated evaluation with legal/significant effect; large-scale special-category or criminal data; systematic large-scale monitoring of publicly accessible areas.
2. **The two-criteria rule is a presumption, not a mandate.** Meeting 2+ of the 9 EDPB criteria (WP 248 rev.01) strongly presumes a DPIA is needed — but one criterion may suffice, and two may be justified as unnecessary, if thoroughly documented.
3. **National blacklists are additive, not exhaustive** — processing absent from a list may still need a DPIA; a blacklist entry overrides another jurisdiction's whitelist.
4. **Multi-jurisdictional processing checks ALL relevant blacklists** — the obligation triggers if the processing matches a list in any jurisdiction where the controller is established or where data subjects are located. The one-stop-shop (Art. 56) governs enforcement, not which Art. 35(4) lists apply.

## Legal precision points (where model memory tends to be imprecise)

- **Art. 9 is cumulative with Art. 6** — special-category data needs both a legal basis (Art. 6) and an exception (Art. 9(2)); two separate hurdles.
- **"Large scale" has no fixed number** — weigh number of subjects, data volume, duration, geographic extent. Never cite a numeric threshold.
- **DPIA precedes processing** (Art. 35(1)) — a pre-processing obligation; if processing already began, still do it and note the gap.
- **AI needs dual-phase analysis** (EDPB Opinion 28/2024) — training and deployment are distinct activities; a deployer cannot lean solely on the provider's DPIA.
- **Pseudonymisation reduces likelihood** (EDPB Guidelines 01/2025) only if genuine — trivial re-identification reduces nothing.
- **Risk is from the data subject's perspective** (Recital 75) — identity-theft risk to the individual, not reputational risk to the company.
- **AI Act FRIA is distinct** — a Fundamental Rights Impact Assessment for high-risk AI complements, does not replace, the DPIA.

## Risk register format

| Risk ID | Description | Rights category | Likelihood (1–5) | Severity (1–5) | Score | Level |
| ------- | ----------- | ---------------- | ----------------- | ---------------- | ----- | ----- |

Then a **residual-risk overview**: total risks by level before and after mitigation, and the overall position — Acceptable / Acceptable with Conditions / **Art. 36 Consultation Required**.

## Art. 36 prior consultation

Sequential to the DPIA, not part of it. If residual risk stays high after all feasible mitigations, consult the supervisory authority **before** processing begins. The SA has 8 weeks, extendable by 6.

## {PROJECT_NAME} application

{PROJECT_NAME} processes special-category {SENSITIVE_DATA} at scale through an AI analysis service — a DPIA is squarely required, and the AI dual-phase split applies. This is the home for known gaps and deferred controls that must **stay out** of outsider-facing documents (per the self-check).
