# Statutory Interpretation — Reading a New Statute Correctly

> Distilled from the `statute-analysis` skill in github.com/lawve-ai/awesome-legal-skills (AGPL-3.0, © Rafał Stanisław Fryc) — summarised methodology, not copied. Guidance for reading statutes/regulations, not legal advice; verify currency per the pre-delivery self-check.

Use when reading a new statute, regulation, or rule (e.g. a new NZ Information Privacy Principle such as IPP 3A, or s 11) — to extract requirements correctly before relying on them.

## Before reading — verify status

- Check the **effective date** (it may be future) and note multiple effective dates for different provisions.
- Determine whether amendments are pending; find the **consolidated/codified** version, not the original as passed.
- Identify implementing regulations (read the statute **and** them — operational detail often lives in the regulation).
- Identify the enforcing agency and its enforcement posture (the same words mean different things under different enforcers).

## Reading techniques

- **Start with definitions** — terms may carry statutory meanings differing from ordinary usage. Note whether a definition is exhaustive ("means") or illustrative ("includes").
- **Parse the operator words** — they have consistent legal force:

| Term | Force |
| ------------------------------------- | -------------------------------------- |
| **shall** | mandatory |
| **may** | permissive |
| **and** | conjunctive — ALL elements required |
| **or** | disjunctive — ANY one suffices |
| **unless / except** | an exception to the general rule |
| **subject to** | limited by another provision |
| **notwithstanding** | applies despite other provisions |
| **if…then / upon / provided that** | a precondition must be met |

Misreading "and" as "or", or "shall" as "may", changes the obligation entirely.

- **Track cross-references** — read referenced sections; they may expand, limit, or modify the provision.

## Resolving ambiguity (canons)

- **Plain-meaning rule** — clear words need no further inquiry.
- **Whole-act rule** — read the text as a coherent whole.
- **Expressio unius** — expressing one thing implies excluding others.
- **Ejusdem generis** — general terms following specific ones are limited to the same class.
- **Surplusage** — every word should have meaning.
- **Avoid absurdity** — reject readings producing absurd results; prefer the statute's purpose (preamble/findings clauses).

## Applicability and requirement types

- **Who must comply** — read thresholds carefully: **OR** (any threshold = covered) is far broader than **AND** (all required). Note entity vs. data exemptions, and delayed-application grace periods vs. permanent exemptions.
- **Categorise each requirement** so it routes correctly — Disclosure (legal/policy), Operational (process), Technical (engineering), UI/Design (product). A "privacy-policy requirements" list should not contain operational deadlines that never appear in the policy itself.
- **Note what the statute does NOT say** — absence of a private right of action, a safe harbour, or a remedy is as significant as what is present.

## {PROJECT_NAME} application

{PROJECT_NAME} touches multiple regimes (GDPR, EU AI Act, {JURISDICTION} civil law, and — where {ORG_UNIT}s operate abroad — laws like the NZ Privacy Act). When a new provision lands, run this before changing any document or control: confirm it is in force and current, parse its operator words, and categorise what it actually demands of us.
