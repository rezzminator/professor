# Vendor / Sub-Processor Due Diligence

> Distilled from the `vendor-due-diligence` skill in github.com/lawve-ai/awesome-legal-skills (AGPL-3.0, © Patrick Munro) — summarised framework, not copied. Educational framework, not professional due-diligence or legal advice; engage qualified experts for critical vendors and verify regulatory requirements independently.

Use when vetting a sub-processor, technology vendor, or third-party partner before onboarding, or when reassessing one after a triggering event.

## Three-phase process

1. **Initial screening** — financial stability, basic compliance (certifications, licences, regulatory status), preliminary security posture (ISO 27001, SOC 2, cyber insurance), reputational check (news, litigation, sanctions lists), business-continuity basics.
2. **Detailed assessment** — security deep-dive (pen-test results, vulnerability management, incident response), operational deep-dive (SLAs, performance, capacity, change management), compliance audit (GDPR, data residency, cross-border transfers), financial analysis, contractual-risk review (liability caps, indemnification, IP, termination), and **fourth-party** (sub-sub-processor) risk.
3. **Final evaluation** — risk scoring, recommendation (approve / approve-with-conditions / reject), mitigation plan, onboarding requirements, ongoing-monitoring framework.

## Risk scoring (1 = low → 5 = critical) across dimensions

Financial · Operational · Compliance · Security · Reputational · Strategic (criticality, exit difficulty, lock-in). **Weight security and compliance ×2 for critical services** (anything touching {SUBJECT_NOUN} data). A regulatory **gap analysis** classifies each shortfall: blocker / major concern / minor gap / acceptable-with-mitigation.

## Documents to request

Audited financials and insurance certificates; certifications (ISO 27001, SOC 2) and audit reports; **privacy policy, DPA, and sub-processor list**; pen-test and vulnerability-scan reports, incident-response and DR plans; SLA templates and customer references; standard agreement with liability/indemnification/IP terms.

## Red-flag interview prompts

"Describe your three most recent security incidents and your response." "What percentage of revenue comes from your top three clients?" (concentration risk). "Where is our data processed and stored?" "Who are your sub-processors, and how do you notify us of changes?"

## Ongoing monitoring

Quarterly performance/security/compliance reviews; annual full re-scoring and certification-renewal checks; event-triggered reviews (M&A, breach, regulatory fine, leadership change, service disruption). Early-warning indicators that force immediate re-assessment: bankruptcy filing, mass layoffs, major customer loss, data breach, audit failure, regulatory fine.

## Mitigation levers

Financial — parent guarantee, higher insurance, performance bond. Security — mandated controls, required pen-testing, restricted data access. Compliance — certification within a timeframe, audit rights, regulatory-breach termination clause. Operational — stricter SLAs, redundancy, code/IP escrow, backup vendor. Strategic — shorter term, exit provisions, no proprietary lock-in.

## {PROJECT_NAME} application

Run this before adding any new sub-processor to the data path (transcription, AI analysis, cloud, email). The DPA and current-SCC status are non-negotiable gating items for any vendor touching {SESSION_NOUN} data; "plan for exit" means confirming data portability and deletion before signing. Feed the outcome into the officer's `docs/epics/legal/registers/sub-processor-compliance.md`.
