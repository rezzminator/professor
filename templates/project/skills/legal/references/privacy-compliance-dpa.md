# Privacy Compliance & DPA Review ({REGULATION} Art. 28)

> Adapted from the `compliance-anthropic` skill in github.com/lawve-ai/awesome-legal-skills (Apache-2.0, © Anthropic). Use when reviewing a Data Processing Agreement / Addendum, handling a data-subject request, or assessing a cross-border transfer. Assists legal workflow — not legal advice; regulatory requirements change, so verify currency per the pre-delivery self-check.
>
> **Adopter fill-in:** `{REGULATION}` is your primary data-protection regime and `{JURISDICTION}` its territory. Article numbers, deadlines, and transfer mechanisms below carry the shape of a mature regime — re-anchor every citation and every number to the regimes that actually bind you before relying on this card.

## DPA — required elements ({REGULATION} Art. 28(3))

A compliant DPA must define all of:

- **Subject matter and duration** of the processing.
- **Nature and purpose** — what processing occurs and why.
- **Type of personal data** and **categories of data subjects**.
- **Controller's obligations and rights** — its instructions and oversight rights.

## DPA — processor obligations to verify

- **Documented-instructions only** — processor processes solely on the controller's documented instructions (carve-out for legal requirements).
- **Confidentiality** — authorised personnel are bound to confidentiality.
- **Security** — appropriate technical and organisational measures (Art. 32 referenced).
- **Sub-processors** — written authorisation (general or specific); if general, notice of changes with a right to object; sub-processors bound by equivalent terms; processor stays liable for them.
- **Data-subject-rights assistance** — processor helps the controller answer requests.
- **Security & breach assistance** — processor assists with Art. 32–36 (security, breach notice, DPIA, prior consultation).
- **Deletion or return** — on termination, delete or return all data at the controller's choice and delete copies, unless legal retention applies.
- **Audit rights** — controller may audit/inspect, or accept third-party audit reports.
- **Breach notice to controller** — without undue delay (target 24–48h) so the controller can meet its own regulatory deadline (72h under many regimes).

## DPA — international transfers

- **Mechanism identified** — adequacy decision, the current **{JURISDICTION} standard contractual clauses (SCCs)**, or binding corporate rules.
- **Correct SCC module** — C2P, C2C, P2P, or P2C as the relationship requires.
- **Transfer impact assessment** — completed for transfers to non-adequate countries, with supplementary measures for any gap.
- **Secondary-jurisdiction addendum** — included wherever another jurisdiction's personal data is in scope and its regime requires its own rider.

## Common DPA red flags

| Red flag | Risk | Standard position |
| ---------------------------------------------------------- | ---------------------------------- | ---------------------------------------------------------------------------- |
| Blanket sub-processor authorisation, no notice | Loss of control over the chain | Require notice + right to object |
| Breach-notice window > 72h | Blocks timely regulatory notice | Require 24–48h |
| No audit rights (or only third-party reports) | Cannot verify compliance | Accept a recognised third-party security-audit report ({DOMAIN_STANDARDS}) **plus** audit-on-cause |
| Deletion timeline unspecified | Data retained indefinitely | Require deletion within 30–90 days |
| Processing locations unspecified | Data processed anywhere | Require disclosure of locations |
| Outdated SCCs | Invalid transfer mechanism | Require the current {JURISDICTION} SCCs |
| **Liability / insurance / commercial terms in the DPA** | Scope creep, conflicting clauses | Move to the master services agreement — Art. 28 is data-protection only |

## Data-subject request handling

1. **Identify request type** — access, rectification, erasure, restriction, portability, objection ({REGULATION}); plus any regime-specific right a secondary regime adds (opt-out of sale/share, limits on sensitive-data use).
2. **Identify applicable regime** — by where the subject is and what laws bind us.
3. **Verify identity** — proportionate to data sensitivity; do not demand excessive proof.
4. **Log** — date received, type, requester, regime, deadline, handler.
5. **Apply exemptions** — legal-claim defence, statutory retention, third-party rights, freedom of expression (erasure), litigation hold. Cite the basis for any denial.
6. **Respond and record** — fulfil or explain; tell the requester of their right to complain to the supervisory authority.

**Response timelines:** record one line per regime that binds you — `{REGULATION}` first, then each secondary regime. Common shapes to check yours against: 30 days extendable by 60 for complex requests; acknowledge in 10 business days with a substantive response in 45 calendar days, extendable by 45; a flat 15 days.

## {PROJECT_NAME} application

{PROJECT_NAME} is a processor for its {USER_NOUN}-clients on {SESSION_NOUN} data and a controller for account data — confirm which hat applies before drafting. The **controller named in a processor-side DPA is the client {USER_NOUN}**, not the user. Sub-processors to cover: the transcription service, the AI analysis service's cloud provider, and the cloud infrastructure provider — each needs its own DPA and a transfer mechanism. Cross-reference the officer's `docs/epics/legal/registers/sub-processor-compliance.md` for current status.
