---
name: legal
repo: 'https://github.com/lawve-ai/awesome-legal-skills'
description: Index of curated legal/compliance playbooks — DPA (Art. 28), DPIA (Art. 35), breach response (Art. 33/34), privacy notices (Art. 13/14), vendor due diligence, red-team QC, NDA/risk triage, statute reading, pre-delivery self-check, skill-optimizer. Load for legal drafting, any DPA/DPIA/privacy/breach/consent doc, vendor vetting or legal QC.
---

# Legal Reference Index

Curated legal/compliance playbooks. This file is the index — load the one reference the task needs, not all of them. Each reference distils a public skill; per-file attribution and license sit at its top (Anthropic skills Apache-2.0 and adapted; the rest AGPL-3.0 and distilled — legal rules in our own words, not copied prose).

None of it is legal advice.

## When to consult what

| If the task is… | Consult |
| ------------------------------------------------------------------- | ---------------------------------------- |
| **About to deliver any drafted/edited legal document** (run first) | `references/pre-delivery-self-check.md` |
| Reviewing a DPA/DPA addendum, a data-subject request, or a transfer | `references/privacy-compliance-dpa.md` |
| Drafting or revising a DPA / Art. 28 processor agreement | `references/dpa-drafting-guide.md` |
| Building a DPIA, a risk register, or an Art. 36 consultation call | `references/dpia.md` |
| Responding to a breach / incident (Art. 33/34, ENISA severity, T0) | `references/breach-response.md` |
| Drafting a privacy notice, policy, or {SUBJECT_NOUN} consent document | `references/privacy-notice-policy.md` |
| Vetting a new sub-processor or vendor before onboarding | `references/vendor-due-diligence.md` |
| Running a dedicated adversarial QC / fact-check pass on a draft | `references/red-team-verifier.md` |
| Triaging an NDA, or tiering any finding by severity × likelihood | `references/nda-risk-triage.md` |
| Reading a new statute/regulation correctly before relying on it | `references/statute-analysis.md` |
| Turning a session's corrections into a sharpened reference rule | `references/skill-optimizer.md` |

## The non-negotiable

Before any legal document leaves our hands, it passes `references/pre-delivery-self-check.md`: verify every fact against the primary source (never from memory), mark our reasoned readings as position not settled law, don't overclaim, state only what we do/commit (gaps live in the DPIA), name parties by defined term, and keep each instrument to its legal scope.
