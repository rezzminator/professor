# Privacy Notice & Policy (Art. 13/14)

> Distilled from the `gdpr-privacy-notice-eu` (© Oliver Schmidt-Prietz) and `privacy-policy` / politique-confidentialite (© Malik Taiar) skills in github.com/lawve-ai/awesome-legal-skills (both AGPL-3.0) — summarised methodology, not copied. Structured Art. 13/14 guidance, not legal advice; verify currency per the pre-delivery self-check.

The privacy notice/policy is the primary instrument for informing data subjects under Art. 13 (data collected from the subject) and Art. 14 (data obtained otherwise). It must be transparent, accessible, and complete (Art. 12).

## Workflow

Scope → Intake → Draft → Verify → Deliver.

1. **Scope** — pick the notice type first, then jurisdiction(s).
2. **Intake** — collect everything before drafting; the type drives which data categories and legal bases to probe.
3. **Draft** — from a template; if a vetted template exists, preserve its validated wording and only fill placeholders — do not rewrite validated legal language.
4. **Verify** — run the Art. 13/14 checklist + type-specific checks + AI-Act transparency check.

## Notice types (each changes sections, data profile, legal bases, retention defaults)

Website/App · Applicant/Recruiting · Employee · Business Partner (B2B) · B2C Customer · Combined. Sub-classify a Website/App by platform (brochure, e-commerce, SaaS, mobile, marketplace, AI-featured) to anticipate data categories.

## Mandatory disclosures (the verify checklist)

- Controller identity and contact; DPO contact (functional email) if appointed.
- Each **purpose** of processing and its **legal basis** (Art. 6 — and Art. 9(2) exception for special-category data).
- Where legitimate interest is the basis: the interest, and the balancing.
- **Recipients / categories of recipients**, including sub-processors.
- **International transfers** and their safeguard (adequacy / SCCs / BCRs).
- **Retention period** per purpose (or the criteria used to set it).
- Data-subject **rights** (access, rectification, erasure, restriction, portability, objection) and how to exercise them.
- Right to **withdraw consent** (where consent is the basis) and to **complain** to the supervisory authority.
- Whether provision is statutory/contractual and the consequence of not providing.
- Existence of **automated decision-making / profiling** with meaningful information about the logic (Art. 13(2)(f), Art. 22).
- **AI Act transparency** where an AI system interacts with or produces output about the person.
- **Children's data** section with the correct age threshold for the jurisdiction.

## Legal bases (Art. 6) — name the right one

Consent · contract · legal obligation · vital interests · public task · legitimate interests. Special-category data additionally needs an Art. 9(2) exception (for {SENSITIVE_DATA} in a {DOMAIN_ADJ} context, typically explicit consent or the health/social-care provision).

## Multi-language

A translation gap is a compliance gap — every mandatory disclosure must appear in each language version. Name the governing version ("in case of discrepancy, the [X] version prevails"). Each translation should be a standalone notice, not a partial one.

## {PROJECT_NAME} application

{PROJECT_NAME} needs a {SUBJECT_NOUN}-facing consent notice and a {USER_NOUN}/{ORG_UNIT}-facing policy. Universal up-front consent at signup is the architecture — the notice documents and informs that consent, it is not a per-feature runtime flag. The AI-analysis transparency and automated-processing disclosures are mandatory here. The notice is outsider-facing: describe components by function (the AI analysis service, the application database), never internal names.
